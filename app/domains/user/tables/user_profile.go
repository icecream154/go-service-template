package tables

import (
	"database/sql"
	"time"
)

// UserProfile 严格映射 user_service.user_profile 表。
// 表结构、默认值和唯一索引以 database/user/mysql/user_profile.sql 为准。
// 对应的 UserProfileRepository 直接使用本结构进行数据库读写。
// 业务层如需其他模型，由业务层自行转换，仓储不依赖 DDD 模型。
type UserProfile struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// AccountSource 是服务端配置的完整外部身份命名空间。
	AccountSource string `gorm:"column:account_source;type:varchar(32);not null"`

	// ExternalAccountID 保留可信来源中的原始标识，不转为数字。
	ExternalAccountID string `gorm:"column:external_account_id;type:varchar(128);not null"`

	// UserName 是展示昵称，允许重复。
	UserName string `gorm:"column:user_name;type:varchar(64);not null"`

	// AvatarURL.Valid=false 表示 SQL NULL，即尚未提供头像。
	AvatarURL sql.NullString `gorm:"column:avatar_url;type:varchar(512)"`

	// IsEnabled 在注册创建时显式设为 true；不声明 default:true，避免替换显式 false。
	IsEnabled bool `gorm:"column:is_enabled;type:tinyint;not null"`

	// Version 初始为 0；受保护更新须检查旧版本并递增，GORM 不会自动维护。
	Version int64 `gorm:"column:version;type:bigint;not null"`

	// CreatedAt、UpdatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:datetime(3);not null;autoUpdateTime"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 user_service。
func (UserProfile) TableName() string {
	return "user_profile"
}
