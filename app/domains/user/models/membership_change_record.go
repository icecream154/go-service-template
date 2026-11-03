package models

import "time"

// MembershipChangeRecord 表示一笔会员订单引发的权益变更前后快照。
// 字段按业务含义声明，数据库映射由 tables.MembershipChangeRecord 承担。
type MembershipChangeRecord struct {
	// ID 是权益变更记录的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// UserID 是权益所属用户的内部标识。
	UserID int64

	// MembershipID 是当前会员权益记录的内部标识。
	MembershipID int64

	// OrderID 是引发本次权益变更的会员订单内部标识。
	OrderID int64

	// ChangeType 是权益变更类型：10 表示开通，20 表示续费。
	ChangeType int32

	// BeforeMembershipLevel 是变更前会员等级，首次开通时为 nil。
	BeforeMembershipLevel *int32

	// BeforeEffectiveAt 是变更前的 UTC 生效时间，精度为毫秒；首次开通时为 nil。
	BeforeEffectiveAt *time.Time

	// BeforeExpiredAt 是变更前的 UTC 到期时间，精度为毫秒；首次开通时为 nil。
	BeforeExpiredAt *time.Time

	// AfterMembershipLevel 是变更后会员等级，本例 10 表示标准会员。
	AfterMembershipLevel int32

	// AfterEffectiveAt 是变更后的 UTC 生效时间，精度为毫秒。
	AfterEffectiveAt time.Time

	// AfterExpiredAt 是变更后的 UTC 到期时间，精度为毫秒。
	AfterExpiredAt time.Time

	// CreatedAt 是本次权益变更记录创建的 UTC 时间，精度为毫秒。
	CreatedAt time.Time
}
