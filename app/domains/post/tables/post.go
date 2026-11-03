package tables

import (
	"database/sql"
	"time"
)

// Post 严格映射 post_service.post 表。
// 表结构、默认值和索引以 database/post/mysql/post.sql 为准。
// 该模型用于持久化，内部业务处理使用 models.Post。
type Post struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// AuthorUserID 引用 user_service.user_profile.id。
	AuthorUserID int64 `gorm:"column:author_user_id;type:bigint;not null"`

	// Title 的非空及长度限制由应用校验。
	Title string `gorm:"column:title;type:varchar(200);not null"`

	// Content 的内容及字节容量由应用校验。
	Content string `gorm:"column:content;type:text;not null"`

	// LikeCount 初始为 0，由应用与点赞关系在同一事务中维护。
	// 帖子软删除时保留计数及关系，GORM 不会自动维护该计数。
	LikeCount int64 `gorm:"column:like_count;type:bigint;not null"`

	// CreatedAt、UpdatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	// 正文、点赞计数或删除状态变更时均须维护 UpdatedAt。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:datetime(3);not null;autoUpdateTime"`

	// DeletedAt.Valid=false 表示未删除，由应用显式维护软删除状态。
	// 使用 sql.NullTime，不启用 GORM 自动软删除或查询过滤。
	DeletedAt sql.NullTime `gorm:"column:deleted_at;type:datetime(3)"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 post_service。
func (Post) TableName() string {
	return "post"
}
