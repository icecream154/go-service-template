package models

import "time"

// UserMembership 表示用户当前会员权益，到期后仍保留记录。
// 字段按业务含义声明，数据库映射由 tables.UserMembership 承担。
type UserMembership struct {
	// ID 是当前会员权益记录的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// UserID 是权益所属用户的内部标识。
	UserID int64

	// MembershipLevel 是当前会员等级，本例 10 表示标准会员。
	MembershipLevel int32

	// EffectiveAt 是当前权益的 UTC 生效时间，精度为毫秒。
	EffectiveAt time.Time

	// ExpiredAt 是当前权益的 UTC 到期时间，精度为毫秒。
	ExpiredAt time.Time

	// CreatedAt 是权益记录的 UTC 创建时间，精度为毫秒。
	CreatedAt time.Time

	// UpdatedAt 是权益记录最近修改的 UTC 时间，精度为毫秒。
	UpdatedAt time.Time
}
