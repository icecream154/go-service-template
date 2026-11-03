package tables

import "time"

// PostLike 严格映射 post_service.post_like 表。
// 表结构、默认值和索引以 database/post/mysql/post_like.sql 为准。
// 取消点赞时物理删除，关系增删与帖子点赞计数由应用在同一事务中维护。
// 内部业务处理使用 models.PostLike。
type PostLike struct {
	// ID 由应用通过统一雪花组件生成，写入前须验证为正数。
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement:false;not null"`

	// PostID 引用 post_service.post.id。
	PostID int64 `gorm:"column:post_id;type:bigint;not null"`

	// UserID 引用 user_service.user_profile.id。
	UserID int64 `gorm:"column:user_id;type:bigint;not null"`

	// CreatedAt 的自动维护依赖统一 NowFunc 提供 UTC 毫秒时间。
	CreatedAt time.Time `gorm:"column:created_at;type:datetime(3);not null;autoCreateTime"`
}

// TableName 固定使用单数表名；普通访问连接的默认数据库须为 post_service。
func (PostLike) TableName() string {
	return "post_like"
}
