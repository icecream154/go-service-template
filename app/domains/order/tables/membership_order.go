package tables

import (
	"database/sql"
	"time"
)

// MembershipOrder 严格映射 order_service.membership_order 表。
// 表结构、默认值和唯一索引以 database/order/mysql/membership_order.sql 为准。
// 该模型用于持久化，内部业务处理使用 models.MembershipOrder。
// 字段声明不负责校验取值或推进支付、履约状态，初始值由应用显式赋值。
type MembershipOrder struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// OrderNo 是对外展示和查询的会员订单业务编号，在本表内唯一。
	OrderNo string `gorm:"column:order_no;type:varchar(64);not null"`

	// UserID 引用 user_service.user_profile.id，关联关系由应用维护。
	UserID int64 `gorm:"column:user_id;type:bigint;not null"`

	// PlanCode 是下单时的套餐编码快照，不随套餐配置变化。
	PlanCode string `gorm:"column:plan_code;type:varchar(64);not null"`

	// PlanVersion 是下单时的套餐版本快照，用于定位购买时的套餐定义。
	PlanVersion int64 `gorm:"column:plan_version;type:bigint;not null"`

	// MembershipLevel 是下单时的会员等级快照，10 表示标准会员。
	MembershipLevel int32 `gorm:"column:membership_level;type:int;not null"`

	// DurationSeconds 是下单时的会员有效时长快照，单位为秒，由应用校验为正数。
	DurationSeconds int64 `gorm:"column:duration_seconds;type:bigint;not null"`

	// PayableAmount 是下单时的应付金额快照，单位为 CNY 分，由应用校验为正数。
	PayableAmount int64 `gorm:"column:payable_amount;type:bigint;not null"`

	// CurrencyCode 是下单时的币种代码快照，本例由应用校验为 CNY。
	CurrencyCode string `gorm:"column:currency_code;type:varchar(3);not null"`

	// PaymentStatus 为 10 时待支付，为 20 时已支付且权益发放完成；创建时由应用显式赋值。
	PaymentStatus int32 `gorm:"column:payment_status;type:int;not null"`

	// PaymentChannel 是下单时选定的支付渠道，本例每个渠道对应唯一固定商户账户。
	PaymentChannel string `gorm:"column:payment_channel;type:varchar(32);not null"`

	// PaymentTransactionNo.Valid=false 表示 SQL NULL，即尚未取得渠道交易流水号。
	PaymentTransactionNo sql.NullString `gorm:"column:payment_transaction_no;type:varchar(128)"`

	// PaidAt 是渠道确认的实际支付成功 UTC 时间；Valid=false 表示尚未确认支付，不自动维护。
	PaidAt sql.NullTime `gorm:"column:paid_at;type:datetime(3)"`

	// FulfilledAt 是本地完成权益履约时取用的 UTC 处理时间，不代表提交时间；Valid=false 表示尚未完成履约，不自动维护。
	FulfilledAt sql.NullTime `gorm:"column:fulfilled_at;type:datetime(3)"`

	// CreatedAt、UpdatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:datetime(3);not null;autoUpdateTime"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 order_service。
func (MembershipOrder) TableName() string {
	return "membership_order"
}
