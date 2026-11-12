// Package usermembership 定义用户 V1 会员权益查询接口的请求、响应及组内复用结构。
package usermembership

import "time"

// UserMembership 表示当前会员权益；到期后的记录仍保留原等级和时间。
type UserMembership struct {
	// MembershipID 是会员权益记录标识，JSON 使用十进制字符串。
	MembershipID int64 `json:"membership_id,string"`
	// MembershipLevel 是会员等级，当前 10 表示标准会员。
	MembershipLevel int32 `json:"membership_level"`
	// EffectiveAt 是 UTC 毫秒精度的生效时间。
	EffectiveAt time.Time `json:"effective_at"`
	// ExpiredAt 是 UTC 毫秒精度的到期时间，到达该时刻即已过期。
	ExpiredAt time.Time `json:"expired_at"`
}
