package llm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

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

// createCompletion 负责公共请求转换和 SDK 调用；模型校验由供应商入口先行完成。
// 超时覆盖完整 SDK 调用，保留传入 Context 的值和更早的取消期限，不额外重试。
func (c *openAICompatibleClient) createCompletion(ctx context.Context, params GenerateTextParams) (*openai.ChatCompletion, error) {
	if ctx == nil {
		return nil, errors.New("LLM request context is required")
	}
	callCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	completion, err := c.sdk.New(callCtx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(params.Prompt)},
		Model:    openai.ChatModel(params.Model),
	})
	if err != nil {
		failure := &ClientError{Provider: c.provider, cause: err}
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			failure.StatusCode = apiError.StatusCode
		}
		return nil, failure
	}
	return completion, nil
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
