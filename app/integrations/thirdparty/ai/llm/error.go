package llm

import "errors"

// ClientError 表达 SDK 或供应商调用故障，公开文案不包含远端正文、请求内容或密钥。
// Provider 为供应商名称；StatusCode 为 SDK HTTP 错误状态；ProviderCode 为供应商错误码。
// 这些信息仅供上层分类，不直接决定对外状态。
type ClientError struct {
	Provider     string
	StatusCode   int
	ProviderCode int
	cause        error
}

func (err *ClientError) Error() string {
	if err.Provider == "" {
		return "LLM text generation request failed"
	}
	return err.Provider + " text generation request failed"
}

// Unwrap 保留原始原因，供 errors.Is 识别取消、超时及 ErrInvalidResponse。
func (err *ClientError) Unwrap() error {
	return err.cause
}

// ErrInvalidResponse 表示远端响应缺少有效文本，或供应商扩展状态格式不合法。
// Client 返回的 ClientError 通过错误链保留此值，供 errors.Is 统一判断。
var ErrInvalidResponse = errors.New("LLM text generation response is invalid")

func newInvalidResponseError(provider string) *ClientError {
	return &ClientError{Provider: provider, cause: ErrInvalidResponse}
}
