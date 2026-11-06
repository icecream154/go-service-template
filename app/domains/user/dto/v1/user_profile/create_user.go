package userprofile

import "time"

// CreateUserRequest 表示首次自注册的身份和初始资料，不要求已有内部用户 ID。
type CreateUserRequest struct {
	// AccountSource 必填，表示外部账户的身份命名空间，保留原始大小写。
	AccountSource string `form:"account_source" json:"account_source"`
	// ExternalAccountID 必填，表示该来源内的外部账户原始标识，不转换为数字。
	ExternalAccountID string `form:"external_account_id" json:"external_account_id"`
	// UserName 必填，表示初始展示昵称，允许重名。
	UserName string `form:"user_name" json:"user_name"`
	// AvatarURL 可选；省略或 null 表示尚未提供，空字符串原样保留。
	AvatarURL *string `form:"avatar_url" json:"avatar_url"`
}

// CreateUserResponse 同时返回本次创建的用户资料和该用户的身份凭证。
// 仅在创建与签发均成功时输出；包含敏感凭证，不记录整个 DTO。
type CreateUserResponse struct {
	// UserProfile 必填，表示本次创建的用户资料，内部 ID 由服务端生成。
	UserProfile UserProfile `json:"user_profile"`
	// AccessToken 必填，表示新用户的 JWT 原文，不包含 Bearer 前缀。
	AccessToken string `json:"access_token"`
	// TokenType 必填，表示凭证方案，当前为 Bearer。
	TokenType string `json:"token_type"`
	// ExpiresAt 必填，直接采用签发结果的失效时间，以 UTC RFC3339 字符串输出。
	ExpiresAt time.Time `json:"expires_at"`
}
