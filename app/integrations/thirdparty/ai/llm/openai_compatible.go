package llm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"go-service-template/internal/logger"
	"go-service-template/internal/tracing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// openAICompatibleClient 复用 OpenAI 兼容协议，不作为其他 SDK 的强制实现。
// 每个供应商分别构造自己的实例和连接池，构造后不保存单次请求状态。
type openAICompatibleClient struct {
	provider       string
	sdk            openai.ChatCompletionService
	requestTimeout time.Duration
	transport      *http.Transport
}

// newOpenAICompatibleClient 接收供应商已校验的连接参数，不读取配置或发起远端请求。
func newOpenAICompatibleClient(provider, baseURL, apiKey string, connectTimeout, requestTimeout time.Duration) (*openAICompatibleClient, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || defaultTransport == nil {
		return nil, errors.New("default HTTP transport must be an *http.Transport")
	}
	transport := defaultTransport.Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// 仅构造所需的 SDK 接口，避免继承进程中的 OPENAI_* 环境配置及自定义头。
	sdk := openai.NewChatCompletionService(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	return &openAICompatibleClient{
		provider:       provider,
		sdk:            sdk,
		requestTimeout: requestTimeout,
		transport:      transport,
	}, nil
}

// closeIdleConnections 释放自有连接池的空闲连接，不取消正在执行的请求。
func (c *openAICompatibleClient) closeIdleConnections() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// generateText 统一创建一次第三方 Client Span，完成校验、SDK 调用、供应商检查和文本提取。
// 具体 Client 提供操作 Context、模型范围及可选的响应校验；不在这里重新识别 operation。
// 各结果分支直接结束 Span 并记录原有日志，超时、错误链和无重试约定保持原样。
func (c *openAICompatibleClient) generateText(ctx context.Context, provider string, params GenerateTextParams, validateResponse func(*openai.ChatCompletion) error, supportedModels ...string) (*GenerateTextResult, error) {
	// 1. 取得共享 Logger 并开始计时，覆盖校验、SDK 执行和结果处理，不保存请求状态到共享 Client。
	lg := logger.Current()
	startedAt := time.Now()
	// 1.1. 复用具体入口自动识别的 operation 创建唯一的 Client Span，日志和 SDK 共用派生 Context。
	// nil Context 不补造 Background，继续保留初始化、模型、Context 的原有检查顺序及参数错误。
	var span *tracing.Span
	if ctx != nil {
		ctx, span = tracing.Current().StartSDKClient(ctx, logger.Operation(ctx))
	}
	// 1.2. 直接登记 Span 的异常退出兜底，panic 使用固定说明标记并按原值继续传播。
	// 业务日志仍在各结果分支直接输出，不使用日志 defer、完成标志或额外生命周期日志。
	defer span.End()
	// 1.3. 开始日志使用本次 Client SpanId，不预填耗时或结果，也不重复建立操作作用域。
	lg.Info(ctx, "LLM text generation started", logger.Fields{Event: logger.EventStarted})
	// 2. 首先检查初始化，保持优先于模型及 Context 检查的错误顺序，并在失败位置直接打印。
	if c == nil {
		// 2.1. 未发出请求时不填状态码；先以相同结束时刻标记 Span 失败，再直接打印原有日志。
		// 固定阶段说明不包含供应商实参，返回错误继续保留原有供应商文案。
		finishedAt := time.Now()
		durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
		span.EndSDKClient(0, "LLM client is not initialized", finishedAt)
		lg.Error(ctx, "LLM text generation finished", logger.Fields{
			Event:      logger.EventFinished,
			DurationMS: &durationMS,
			Error:      "LLM client is not initialized",
		})
		return nil, errors.New(provider + " client is not initialized")
	}
	// 3. 按供应商提供的模型范围精确校验，不改写实参或补充默认模型。
	if err := params.validate(supportedModels...); err != nil {
		// 3.1. 输入失败先以固定说明结束 Span，再直接记录并原样返回，不保存模型实参或提示词。
		finishedAt := time.Now()
		durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
		span.EndSDKClient(0, "LLM text generation parameters are invalid", finishedAt)
		lg.Error(ctx, "LLM text generation finished", logger.Fields{
			Event:      logger.EventFinished,
			DurationMS: &durationMS,
			Error:      "LLM text generation parameters are invalid",
		})
		return nil, err
	}
	// 4. 模型校验通过后要求调用方提供 Context；缺少时不创建 Background 或补造根 Span。
	if ctx == nil {
		// 4.1. 此时仍未调用 SDK，也没有创建 Span；空句柄安全跳过收尾，保留原日志和错误文案。
		finishedAt := time.Now()
		durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
		span.EndSDKClient(0, "LLM request context is required", finishedAt)
		lg.Error(ctx, "LLM text generation finished", logger.Fields{
			Event:      logger.EventFinished,
			DurationMS: &durationMS,
			Error:      "LLM request context is required",
		})
		return nil, errors.New("LLM request context is required")
	}
	// 5. 从本次 Client Context 派生 SDK 请求预算，保留 TraceId、Client SpanId、operation 和更早期限。
	// cancel 的 defer 只释放计时资源；追踪与日志在各结果分支收尾，不增加 SDK 或 Transport 子 Span。
	callCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	// 6. 调用 SDK 并取得实际 HTTP 响应；只读取状态，正文读取和关闭仍由 SDK 负责。
	// WithResponseInto 不替换默认文本反序列化目标，也不附加自定义 TraceId 请求头。
	var response *http.Response
	completion, err := c.sdk.New(callCtx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(params.Prompt)},
		Model:    openai.ChatModel(params.Model),
	}, option.WithResponseInto(&response))
	// 7. 准备本次实际结果字段，已收到响应后的读取、解码及供应商校验失败均保留相同 HTTP 状态。
	var statusCode int
	if response != nil {
		// 7.1. SDK 在某些取消路径不提供响应，未知状态保持 0，稍后按缺失处理，不推断为 200。
		statusCode = response.StatusCode
	}
	fields := logger.Fields{Event: logger.EventFinished}
	if statusCode != 0 {
		// 7.2. 状态确实可知时才写入字段，不用 0 代表某个虚构的 HTTP 交互结果。
		fields.StatusCode = &statusCode
	}
	// 8. SDK 失败时保留原有 ClientError 转换及错误链，在当前分支直接输出固定阶段日志。
	if err != nil {
		// 8.1. 原始 SDK 原因仅保存在私有错误链，不输出请求地址、正文或 SDK 错误对象。
		failure := &ClientError{Provider: c.provider, cause: err}
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			// 8.2. 业务错误的 StatusCode 仍只采用 SDK API 错误，保持已有返回契约。
			failure.StatusCode = apiError.StatusCode
			if statusCode == 0 && apiError.StatusCode != 0 {
				// 8.3. SDK 未交回响应对象时，使用 API 错误实际提供的状态补齐日志，仍不填默认值。
				statusCode = apiError.StatusCode
				fields.StatusCode = &statusCode
			}
		}
		// 8.4. 共用一个结束时刻保存耗时及 Span 结果，再直接记录原有失败日志，不输出原始异常。
		finishedAt := time.Now()
		durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
		fields.DurationMS = &durationMS
		fields.Error = "LLM SDK request failed"
		span.EndSDKClient(statusCode, fields.Error, finishedAt)
		lg.Error(ctx, "LLM text generation finished", fields)
		return nil, failure
	}
	// 9. 提取文本前执行可选供应商校验，保留 MiniMax 的 base_resp 检查和 ProviderCode。
	if validateResponse != nil {
		// 9.1. HTTP 成功但供应商协议或业务状态失败时，仍标记 Span Error 并保留真实 HTTP 状态。
		// MiniMax 的业务状态由原有钩子判断，公共层不把 HTTP 200 或 SDK 无错误直接当作成功。
		if err := validateResponse(completion); err != nil {
			finishedAt := time.Now()
			durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
			fields.DurationMS = &durationMS
			fields.Error = "LLM provider response validation failed"
			span.EndSDKClient(statusCode, fields.Error, finishedAt)
			lg.Error(ctx, "LLM text generation finished", fields)
			return nil, err
		}
	}
	// 10. 复用已有候选和 content 检查，协议错误原样返回，合法空字符串仍沿用原有成功语义。
	result, err := c.textResult(completion)
	if err != nil {
		// 10.1. 文本协议错误以固定说明标记 Span 并直接记录，保留已知状态，不记录生成内容。
		finishedAt := time.Now()
		durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
		fields.DurationMS = &durationMS
		fields.Error = "LLM text generation response is invalid"
		span.EndSDKClient(statusCode, fields.Error, finishedAt)
		lg.Error(ctx, "LLM text generation finished", fields)
		return nil, err
	}
	// 11. 全部协议处理完成后，先按实际结束时刻结束 Span，再直接记录成功并返回原结果。
	// 成功不强制写 OK，日志耗时与 Span 使用同一结束时刻，不含结束日志自身的输出开销。
	finishedAt := time.Now()
	durationMS := float64(finishedAt.Sub(startedAt)) / float64(time.Millisecond)
	fields.DurationMS = &durationMS
	span.EndSDKClient(statusCode, "", finishedAt)
	lg.Info(ctx, "LLM text generation finished", fields)
	return result, nil
}

// textResult 在供应商响应校验完成后提取首个候选文本，不裁剪或改写内容。
// 空字符串属于合法文本；缺少候选或 content 为缺失、null、非字符串时返回协议错误。
func (c *openAICompatibleClient) textResult(completion *openai.ChatCompletion) (*GenerateTextResult, error) {
	if completion == nil || len(completion.Choices) == 0 {
		return nil, newInvalidResponseError(c.provider)
	}
	message := completion.Choices[0].Message
	if !message.JSON.Content.Valid() {
		return nil, newInvalidResponseError(c.provider)
	}
	return &GenerateTextResult{Content: message.Content}, nil
}
