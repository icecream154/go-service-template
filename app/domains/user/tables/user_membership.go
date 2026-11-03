package tables

import "time"

// UserMembership 严格映射 user_service.user_membership 表。
// 表结构、默认值和唯一索引以 database/user/mysql/user_membership.sql 为准。
// 该模型用于持久化，内部业务处理使用 models.UserMembership。
type UserMembership struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// UserID 是权益所属用户的内部标识，每个用户至多一份当前权益。
	UserID int64 `gorm:"column:user_id;type:bigint;not null"`

	// MembershipLevel 是当前会员等级，本例 10 表示标准会员。
	MembershipLevel int32 `gorm:"column:membership_level;type:int;not null"`

	// EffectiveAt 是应用填写的 UTC 生效时间，精度为毫秒，不由 GORM 自动修改。
	EffectiveAt time.Time `gorm:"column:effective_at;type:datetime(3);not null"`

	// ExpiredAt 是应用填写的 UTC 到期时间，精度为毫秒，不由 GORM 自动修改。
	ExpiredAt time.Time `gorm:"column:expired_at;type:datetime(3);not null"`

	// CreatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`

	// UpdatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	UpdatedAt time.Time `gorm:"column:updated_at;type:datetime(3);not null;autoUpdateTime"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 user_service。
func (UserMembership) TableName() string {
	return "user_membership"
}
