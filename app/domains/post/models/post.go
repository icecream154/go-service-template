package models

import "time"

// Post 表示帖子的当前内容、删除状态及尚未取消的点赞数量。
// 字段按业务含义声明，数据库映射由 tables.Post 承担。
type Post struct {
	// ID 是帖子的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// AuthorUserID 是作者的用户标识。
	AuthorUserID int64

	// Title 是帖子标题，非空及长度限制由应用校验。
	Title string

	// Content 是帖子正文，内容及大小限制由应用校验。
	Content string

	// LikeCount 是尚未取消的点赞数量，由应用与点赞关系在同一事务中维护。
	// 帖子软删除时保留计数及关系。
	LikeCount int64

	// CreatedAt、UpdatedAt 表示创建和最近修改的 UTC 时间，精度为毫秒。
	// 正文、点赞计数或删除状态变更时均须维护 UpdatedAt。
	CreatedAt time.Time
	UpdatedAt time.Time

	// DeletedAt 为 nil 表示未删除，非 nil 表示软删除的 UTC 毫秒时间。
	DeletedAt *time.Time
}
