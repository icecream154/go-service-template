package models

import "time"

// UserProfile 表示用户当前资料及其绑定的外部账户身份。
// 字段按业务含义声明，数据库映射由 tables.UserProfile 承担。
type UserProfile struct {
	// ID 是用户的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// AccountSource 是外部账户的完整身份命名空间，与外部标识共同表达注册身份。
	AccountSource string

	// ExternalAccountID 是该来源内的外部账户原始标识。
	ExternalAccountID string

	// UserName 是展示昵称，允许重复。
	UserName string

	// AvatarURL 为 nil 表示尚未提供头像；非 nil 时保留实际值，包括空字符串。
	AvatarURL *string

	// IsEnabled 表示用户是否启用。
	IsEnabled bool

	// Version 是资料版本，用于更新时检测并发修改。
	Version int64

	// CreatedAt、UpdatedAt 表示资料创建和最近修改的 UTC 时间，精度为毫秒。
	CreatedAt time.Time
	UpdatedAt time.Time
}
