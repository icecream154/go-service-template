package usermembership

import "time"

// MembershipChangeRecord 表示一笔订单履约引发的权益快照，不包含订单支付信息。
type MembershipChangeRecord struct {
	// RecordID 是变更记录标识，JSON 使用十进制字符串。
	RecordID int64 `json:"record_id,string"`
	// MembershipID 是所属当前会员权益标识，续费时保持不变。
	MembershipID int64 `json:"membership_id,string"`
	// OrderID 是对应订单的内部标识，不是对外业务订单号。
	OrderID int64 `json:"order_id,string"`
	// ChangeType 为 10 开通或 20 续费，过期后再次购买仍为续费。
	ChangeType int32 `json:"change_type"`
	// BeforeMembershipLevel 在首次开通时为 null，字段始终保留。
	BeforeMembershipLevel *int32 `json:"before_membership_level"`
	// BeforeEffectiveAt 是变更前的 UTC 毫秒生效时间，首次开通时为 null。
	BeforeEffectiveAt *time.Time `json:"before_effective_at"`
	// BeforeExpiredAt 是变更前的 UTC 毫秒到期时间，首次开通时为 null。
	BeforeExpiredAt *time.Time `json:"before_expired_at"`
	// AfterMembershipLevel 是本次变更后的会员等级快照。
	AfterMembershipLevel int32 `json:"after_membership_level"`
	// AfterEffectiveAt 是本次变更后的 UTC 毫秒生效时间快照。
	AfterEffectiveAt time.Time `json:"after_effective_at"`
	// AfterExpiredAt 是本次变更后的 UTC 毫秒到期时间快照。
	AfterExpiredAt time.Time `json:"after_expired_at"`
	// CreatedAt 是本次变更记录创建的 UTC 毫秒时间。
	CreatedAt time.Time `json:"created_at"`
}
