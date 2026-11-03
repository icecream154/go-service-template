package models

import "time"

// MembershipOrder 表示一笔会员订单的套餐快照及当前支付履约状态。
// 字段按业务含义声明，数据库映射由 tables.MembershipOrder 承担。
// 字段声明不负责校验取值或推进支付、履约状态。
type MembershipOrder struct {
	// ID 是会员订单的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// OrderNo 是对外展示和查询的会员订单业务编号，在会员订单业务范围内唯一。
	OrderNo string

	// UserID 是购买会员的用户内部标识。
	UserID int64

	// PlanCode 是下单时的套餐编码快照，不随套餐配置变化。
	PlanCode string

	// PlanVersion 是下单时的套餐版本快照，用于定位购买时的套餐定义。
	PlanVersion int64

	// MembershipLevel 是下单时的会员等级快照，10 表示标准会员。
	MembershipLevel int32

	// DurationSeconds 是下单时的会员有效时长快照，单位为秒。
	DurationSeconds int64

	// PayableAmount 是下单时的应付金额快照，单位为 CNY 分。
	PayableAmount int64

	// CurrencyCode 是下单时的币种代码快照，本例固定为 CNY。
	CurrencyCode string

	// PaymentStatus 表示支付及履约状态：10 待支付，20 已支付且权益发放完成。
	PaymentStatus int32

	// PaymentChannel 是下单时选定的支付渠道，本例每个渠道对应唯一固定商户账户。
	PaymentChannel string

	// PaymentTransactionNo 为 nil 表示尚未取得渠道交易流水号。
	PaymentTransactionNo *string

	// PaidAt 是渠道确认的实际支付成功 UTC 时间；nil 表示尚未确认支付。
	PaidAt *time.Time

	// FulfilledAt 是本地完成权益履约时取用的 UTC 处理时间，不代表提交时间；nil 表示尚未完成履约。
	FulfilledAt *time.Time

	// CreatedAt、UpdatedAt 表示订单创建和最近修改的 UTC 时间，精度为毫秒。
	CreatedAt time.Time
	UpdatedAt time.Time
}
