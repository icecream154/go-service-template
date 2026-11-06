package authn

import (
	"context"
	"time"
)

// IssuedCredential 保存签发后的凭证及其有效时间，不是接口响应 DTO。
// 仅在签发成功时使用；Credential.Value 是敏感内容，不记录整个返回值。
type IssuedCredential struct {
	// Credential 是后续交给认证器验证的凭证，方案和内容由具体签发实现确定。
	Credential Credential
	// IssuedAt 是凭证签发时间，使用 UTC，与凭证内的签发时间一致。
	IssuedAt time.Time
	// ExpiresAt 是凭证失效时间，使用 UTC，必须晚于 IssuedAt。
	ExpiresAt time.Time
}

// Issuer 为可信身份签发凭证，不负责从用户输入认定身份，也不替代业务授权。
// 调用方必须先确认该身份可被签发；不能将外部请求构造的任意 Principal 直接交给本接口。
// 成功返回完整凭证和 nil；失败返回 IssuedCredential{} 和非 nil 错误。
// 身份不完整或不符合签发实现的允许范围时返回 ErrInvalidPrincipal；技术故障原样返回。
// ctx 必须非 nil；实现应支持并发使用，并在取消或超时后返回错误而非成功凭证。
type Issuer interface {
	Issue(ctx context.Context, principal Principal) (IssuedCredential, error)
}
