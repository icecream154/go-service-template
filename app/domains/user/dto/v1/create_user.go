package v1

import "go-service-template/app/domains/user/dto/v1/shared"

// CreateUserRequest 表示首次自注册的身份和初始资料，不要求已有内部用户 ID。
type CreateUserRequest struct {
	// AccountSource 必填，表示外部账户的身份命名空间，保留原始大小写。
	AccountSource string `json:"account_source"`
	// ExternalAccountID 必填，表示该来源内的外部账户原始标识，不转换为数字。
	ExternalAccountID string `json:"external_account_id"`
	// UserName 必填，表示初始展示昵称，允许重名。
	UserName string `json:"user_name"`
	// AvatarURL 可选；省略或 null 表示尚未提供，空字符串原样保留。
	AvatarURL *string `json:"avatar_url"`
}

// CreateUserResponse 表示创建成功后的用户资料，内部 ID 由服务端生成。
type CreateUserResponse = shared.UserProfile
