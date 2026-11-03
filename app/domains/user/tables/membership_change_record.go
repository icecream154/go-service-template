package tables

import (
	"database/sql"
	"time"
)

// MembershipChangeRecord 严格映射 user_service.membership_change_record 表。
// 表结构、默认值和唯一索引以 database/user/mysql/membership_change_record.sql 为准。
// 该模型用于持久化且历史只追加，内部业务处理使用 models.MembershipChangeRecord。
type MembershipChangeRecord struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// UserID 是权益所属用户的内部标识。
	UserID int64 `gorm:"column:user_id;type:bigint;not null"`

	// MembershipID 是当前会员权益记录的内部标识。
	MembershipID int64 `gorm:"column:membership_id;type:bigint;not null"`

	// OrderID 是引发本次权益变更的会员订单内部标识，同一订单只生成一条历史。
	OrderID int64 `gorm:"column:order_id;type:bigint;not null"`

	// ChangeType 是权益变更类型：10 表示开通，20 表示续费，由应用校验。
	ChangeType int32 `gorm:"column:change_type;type:int;not null"`

	// BeforeMembershipLevel.Valid=false 表示 SQL NULL，首次开通时没有变更前等级。
	BeforeMembershipLevel sql.NullInt32 `gorm:"column:before_membership_level;type:int"`

	// BeforeEffectiveAt 是变更前的 UTC 毫秒生效时间，首次开通时 Valid=false。
	BeforeEffectiveAt sql.NullTime `gorm:"column:before_effective_at;type:datetime(3)"`

	// BeforeExpiredAt 是变更前的 UTC 毫秒到期时间，首次开通时 Valid=false。
	BeforeExpiredAt sql.NullTime `gorm:"column:before_expired_at;type:datetime(3)"`

	// AfterMembershipLevel 是变更后会员等级，本例 10 表示标准会员。
	AfterMembershipLevel int32 `gorm:"column:after_membership_level;type:int;not null"`

	// AfterEffectiveAt 是应用填写的变更后 UTC 毫秒生效时间，不由 GORM 自动修改。
	AfterEffectiveAt time.Time `gorm:"column:after_effective_at;type:datetime(3);not null"`

	// AfterExpiredAt 是应用填写的变更后 UTC 毫秒到期时间，不由 GORM 自动修改。
	AfterExpiredAt time.Time `gorm:"column:after_expired_at;type:datetime(3);not null"`

	// CreatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 user_service。
func (MembershipChangeRecord) TableName() string {
	return "membership_change_record"
}
