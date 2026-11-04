package v1

import "go-service-template/app/domains/user/dto/v1/shared"

// UpdateUserProfileRequest 表示完整替换昵称、头像的请求，所有字段均须出现。
type UpdateUserProfileRequest struct {
	// OperatorUserID 是发起方的内部用户 ID，必填且为正数，JSON 使用十进制字符串。
	OperatorUserID int64 `json:"operator_user_id,string"`
	// UserID 是待修改用户的内部 ID，必填且为正数，JSON 使用十进制字符串。
	UserID int64 `json:"user_id,string"`
	// ExpectedVersion 是调用方读取的资料版本，必填且非负，0 是有效初始版本。
	ExpectedVersion int64 `json:"expected_version"`
	// UserName 是完整替换后的展示昵称，必填，允许重名。
	UserName string `json:"user_name"`
	// AvatarURL 必须出现；null 表示清空，空字符串与 null 保持不同语义。
	AvatarURL *string `json:"avatar_url"`
}

// UpdateUserProfileResponse 表示更新成功后的资料及递增后的版本。
type UpdateUserProfileResponse = shared.UserProfile
