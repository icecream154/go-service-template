//go:build business

// Package postdom 提供 post 业务域在业务测试中的单表原子造数映射。
package postdom

import (
	"database/sql"
	"time"

	"go-service-template/tests/testkit/seed"
)

// PostSeed 映射 post_service.post 单行前置记录。
type PostSeed struct {
	ID           int64
	AuthorUserID int64
	Title        string
	Content      string
	LikeCount    int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    sql.NullTime
}

// InsertPost 向 post_service.post 插入单行前置帖子记录（受 seed.Session.Freeze 约束）。
func InsertPost(sd *seed.Session, row PostSeed) {
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = sd.BaseTime()
	}
	updatedAt := row.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var deletedAt sql.NullTime
	if row.DeletedAt.Valid {
		deletedAt = sql.NullTime{Time: row.DeletedAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}
	sd.Exec("post", `
INSERT INTO post_service.post
    (id, author_user_id, title, content, like_count, created_at, updated_at, deleted_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID,
		row.AuthorUserID,
		row.Title,
		row.Content,
		row.LikeCount,
		createdAt.UTC().Truncate(time.Millisecond),
		updatedAt.UTC().Truncate(time.Millisecond),
		deletedAt,
	)
}

// PostLikeSeed 映射 post_service.post_like 单行点赞关系记录。
type PostLikeSeed struct {
	ID        int64
	PostID    int64
	UserID    int64
	CreatedAt time.Time
}

// InsertPostLike 向 post_service.post_like 插入单行点赞关系记录（受 seed.Session.Freeze 约束）。
func InsertPostLike(sd *seed.Session, row PostLikeSeed) {
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = sd.BaseTime()
	}
	sd.Exec("post", `
INSERT INTO post_service.post_like
    (id, post_id, user_id, created_at)
VALUES (?, ?, ?, ?)`,
		row.ID,
		row.PostID,
		row.UserID,
		createdAt.UTC().Truncate(time.Millisecond),
	)
}
