package models

import "time"

// UserFollow 表示用户之间一条尚未解除的有向关注关系。
// 字段按业务含义声明，数据库映射由 tables.UserFollow 承担。
type UserFollow struct {
	// ID 是本次关注关系的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// FollowerUserID 是发起关注的用户内部标识。
	FollowerUserID int64

	// FollowedUserID 是被关注用户的内部标识。
	FollowedUserID int64

	// CreatedAt 是本次关注建立的 UTC 时间，精度为毫秒。
	CreatedAt time.Time
}
