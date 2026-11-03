package models

import "time"

// PostLike 表示用户对帖子的当前点赞关系。
// 取消点赞时物理删除；字段的数据库映射由 tables.PostLike 承担。
type PostLike struct {
	// ID 是点赞关系的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// PostID 是被点赞帖子的标识。
	PostID int64

	// UserID 是点赞用户的标识。
	UserID int64

	// CreatedAt 是本次点赞建立的 UTC 时间，精度为毫秒。
	CreatedAt time.Time
}
