package llm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"go-service-template/internal/logger"

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

// generateText 按顺序完成校验、SDK 调用、供应商检查和文本提取，在各结果分支直接记录日志。
// 具体 Client 提供操作 Context、模型范围及可选的响应校验；不在这里重新识别 operation。
// 超时、错误链和无重试约定保持原样，HTTP 状态只采用 SDK 实际提供的值。
func (c *openAICompatibleClient) generateText(ctx context.Context, provider string, params GenerateTextParams, validateResponse func(*openai.ChatCompletion) error, supportedModels ...string) (*GenerateTextResult, error) {
	// 1. 取得共享 Logger 并记录开始；计时覆盖完整调用，Context 和实参不存入共享 Client。
	lg := logger.Current()
	startedAt := time.Now()
	lg.Info(ctx, "LLM text generation started", logger.Fields{Event: logger.EventStarted})
	// 2. 首先检查初始化，保持优先于模型及 Context 检查的错误顺序，并在失败位置直接打印。
	if c == nil {
		// 2.1. 未发出请求时不填状态码；日志为固定阶段说明，返回错误保留原有供应商文案。
		durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
		lg.Error(ctx, "LLM text generation finished", logger.Fields{
			Event:      logger.EventFinished,
			DurationMS: &durationMS,
			Error:      "LLM client is not initialized",
		})
		return nil, errors.New(provider + " client is not initialized")
	}
	// 3. 按供应商提供的模型范围精确校验，不改写实参或补充默认模型。
	if err := params.validate(supportedModels...); err != nil {
		// 3.1. 输入失败直接记录并原样返回，不把模型实参、提示词或任意错误文本写入日志。
		durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
		lg.Error(ctx, "LLM text generation finished", logger.Fields{
			Event:      logger.EventFinished,
			DurationMS: &durationMS,
			Error:      "LLM text generation parameters are invalid",
		})
		return nil, err
	}
	// 4. 模型校验通过后要求调用方提供 Context，不创建 Background 或生成 TraceId。
	if ctx == nil {
		// 4.1. 此时仍未调用 SDK，直接记录缺少上下文并沿用原有错误文案。
		durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
		lg.Error(ctx, "LLM text generation finished", logger.Fields{
			Event:      logger.EventFinished,
			DurationMS: &durationMS,
			Error:      "LLM request context is required",
		})
		return nil, errors.New("LLM request context is required")
	}
	// 5. 派生 SDK 的有限请求预算，保留 TraceId、操作作用域及更早的取消期限。
	// defer 仅释放 Context 计时资源，日志在实际结果分支打印，不使用延迟日志收尾。
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
		// 8.4. 即时计算耗时并记录失败，日志输出故障由 Logger 内部处理，返回错误不受影响。
		durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
		fields.DurationMS = &durationMS
		fields.Error = "LLM SDK request failed"
		lg.Error(ctx, "LLM text generation finished", fields)
		return nil, failure
	}
	// 9. 提取文本前执行可选供应商校验，保留 MiniMax 的 base_resp 检查和 ProviderCode。
	if validateResponse != nil {
		// 9.1. HTTP 成功但供应商协议或业务状态失败时，直接记录失败，不提前输出成功日志。
		if err := validateResponse(completion); err != nil {
			durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
			fields.DurationMS = &durationMS
			fields.Error = "LLM provider response validation failed"
			lg.Error(ctx, "LLM text generation finished", fields)
			return nil, err
		}
	}
	// 10. 复用已有候选和 content 检查，协议错误原样返回，合法空字符串仍沿用原有成功语义。
	result, err := c.textResult(completion)
	if err != nil {
		// 10.1. HTTP 200 后的文本协议错误仍记录为失败，保留已知状态且不记录生成内容。
		durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
		fields.DurationMS = &durationMS
		fields.Error = "LLM text generation response is invalid"
		lg.Error(ctx, "LLM text generation finished", fields)
		return nil, err
	}
	// 11. 所有处理完成后直接记录成功并返回结果，耗时不含结束日志自身的输出开销。
	durationMS := float64(time.Since(startedAt)) / float64(time.Millisecond)
	fields.DurationMS = &durationMS
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
