package userprofile

// UpdateUserProfileRequest 表示完整替换昵称、头像的请求，所有字段均须出现。
// 操作发起方由已认证的 Principal 确定，不从请求中绑定 operator_user_id。
type UpdateUserProfileRequest struct {
	// UserID 是待修改用户的内部 ID，必填且为正数，JSON 使用十进制字符串。
	UserID int64 `json:"user_id,string"`
	// ExpectedVersion 是调用方读取的资料版本，必填且非负；指针区分未提供与有效的版本 0。
	ExpectedVersion *int64 `json:"expected_version"`
	// UserName 是完整替换后的展示昵称，必填，允许重名。
	UserName string `json:"user_name"`
	// AvatarURL 必填且为非空的 HTTP/HTTPS 地址，更新资料不支持清空头像。
	// 缺失、null 和空字符串统一绑定为零值，由 Validator 拒绝。
	AvatarURL string `json:"avatar_url"`
}

// UpdateUserProfileResponse 表示更新成功后的资料及递增后的版本。
type UpdateUserProfileResponse = UserProfile
