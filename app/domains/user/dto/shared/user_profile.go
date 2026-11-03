// Package shared 定义可复用的用户数据结构。
package shared

// UserProfile 表示用户的基本资料，包括内部 ID、展示昵称和头像。
type UserProfile struct {
	// UserID 是用户的内部唯一标识，JSON 使用十进制字符串，避免大整数精度丢失。
	UserID int64 `json:"user_id,string"`

	// UserName 是用户的展示昵称，允许重复。
	UserName string `json:"user_name"`

	// AvatarURL 是可空的头像地址：nil 输出 null，指向空字符串时输出 ""。
	// 字段始终保留在 JSON 中。
	AvatarURL *string `json:"avatar_url"`
}
