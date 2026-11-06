package userprofile

// QueryUserProfileByUserIDRequest 表示按内部用户 ID 查询用户资料的请求参数。
type QueryUserProfileByUserIDRequest struct {
	// UserID 保留请求中的十进制文本，由 Validator 校验为正 int64 后交给 Controller 转换。
	UserID string `form:"user_id" json:"user_id"`
}

// QueryUserProfileByUserIDResponse 表示查询成功后返回的用户基本资料。
// 通过类型别名复用同包 UserProfile，作为统一响应的 data 字段输出。
type QueryUserProfileByUserIDResponse = UserProfile
