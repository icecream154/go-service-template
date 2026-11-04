// Package shared 定义用户 V1 接口复用的数据结构。
package shared

// UserProfile 表示用户的基本资料及修改资料所需的当前状态、版本。
type UserProfile struct {
	// UserID 是用户的内部唯一标识，JSON 使用十进制字符串，避免大整数精度丢失。
	UserID int64 `json:"user_id,string"`

	// UserName 是用户的展示昵称，允许重复。
	UserName string `json:"user_name"`

	// AvatarURL 是可空的头像地址：nil 输出 null，指向空字符串时输出 ""。
	// 字段始终保留在 JSON 中。
	AvatarURL *string `json:"avatar_url"`

	// IsEnabled 表示用户当前是否启用，停用用户仍可被查询。
	IsEnabled bool `json:"is_enabled"`

	// Version 是当前资料版本，更新时作为 expected_version 提交；初始值为 0。
	Version int64 `json:"version"`
}
