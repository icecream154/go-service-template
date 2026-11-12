package usermembership

import "time"

// QueryUserMembershipByUserIDRequest 表示按用户 ID 查询本人当前会员权益的 Query 参数。
type QueryUserMembershipByUserIDRequest struct {
	// UserID 必填，保留正 int64 的十进制文本；发起方身份不从请求绑定。
	UserID string `form:"user_id" json:"user_id"`
}

// QueryUserMembershipByUserIDResponse 同时表达未开通、有效和过期三种查询结果。
type QueryUserMembershipByUserIDResponse struct {
	// UserID 是目标用户标识，JSON 使用十进制字符串以保留整数精度。
	UserID int64 `json:"user_id,string"`
	// MembershipStatus 为 NOT_OPENED、ACTIVE 或 EXPIRED，由本次查询时间计算。
	MembershipStatus string `json:"membership_status"`
	// QueriedAt 是计算本次状态所采用的 UTC 毫秒时间。
	QueriedAt time.Time `json:"queried_at"`
	// Membership 在从未开通时为 null；已过期时仍返回保留的权益。
	Membership *UserMembership `json:"membership"`
}
