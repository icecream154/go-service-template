package tables

import "time"

// UserFollow 严格映射 user_service.user_follow 表。
// 表结构、默认值和唯一索引以 database/user/mysql/user_follow.sql 为准。
// 该模型用于持久化，内部业务处理使用 models.UserFollow。
type UserFollow struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// FollowerUserID 是发起关注的用户内部标识。
	FollowerUserID int64 `gorm:"column:follower_user_id;type:bigint;not null"`

	// FollowedUserID 是被关注用户的内部标识，应用须拒绝自关注。
	FollowedUserID int64 `gorm:"column:followed_user_id;type:bigint;not null"`

	// CreatedAt 是本次关注建立时间，自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 user_service。
func (UserFollow) TableName() string {
	return "user_follow"
}
