//go:build business

// Package userdom 提供 user 业务域在业务测试中的单表原子造数映射、单接口 1:1 薄调用及单表持久化回读辅助。
// 本包严禁封装跨多接口的黑盒业务流程，所有状态流转须在业务测试剧本中显式按步骤编排。
package userdom

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/seed"
)

// ProfileSeed 映射 user_service.user_profile 单行存量记录。
type ProfileSeed struct {
	ID                int64
	AccountSource     string
	ExternalAccountID string
	UserName          string
	AvatarURL         sql.NullString
	IsEnabled         bool
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// StoredProfile 保存从独立数据库连接回读的 user_service.user_profile 持久化快照。
type StoredProfile struct {
	ID                int64
	AccountSource     string
	ExternalAccountID string
	UserName          string
	AvatarURL         sql.NullString
	IsEnabled         bool
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// InsertProfile 向 user_service.user_profile 插入单行前置记录（受 seed.Session.Freeze 约束）。
func InsertProfile(sd *seed.Session, row ProfileSeed) {
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = sd.BaseTime()
	}
	updatedAt := row.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	enabled := 0
	if row.IsEnabled {
		enabled = 1
	}
	sd.Exec("user", `
INSERT INTO user_service.user_profile
    (id, account_source, external_account_id, user_name, avatar_url, is_enabled, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID,
		row.AccountSource,
		row.ExternalAccountID,
		row.UserName,
		row.AvatarURL,
		enabled,
		row.Version,
		createdAt.UTC().Truncate(time.Millisecond),
		updatedAt.UTC().Truncate(time.Millisecond),
	)
}

// FollowSeed 映射 user_service.user_follow 单行有向关注记录。
type FollowSeed struct {
	ID             int64
	FollowerUserID int64
	FollowedUserID int64
	CreatedAt      time.Time
}

// InsertFollow 向 user_service.user_follow 插入单行关注关系。
func InsertFollow(sd *seed.Session, row FollowSeed) {
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = sd.BaseTime()
	}
	sd.Exec("user", `
INSERT INTO user_service.user_follow
    (id, follower_user_id, followed_user_id, created_at)
VALUES (?, ?, ?, ?)`,
		row.ID,
		row.FollowerUserID,
		row.FollowedUserID,
		createdAt.UTC().Truncate(time.Millisecond),
	)
}

// MembershipSeed 映射 user_service.user_membership 单行会员权益快照。
type MembershipSeed struct {
	ID              int64
	UserID          int64
	MembershipLevel int32
	EffectiveAt     time.Time
	ExpiredAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// StoredMembership 保存从独立数据库连接回读的 user_service.user_membership 持久化快照。
type StoredMembership struct {
	ID              int64
	UserID          int64
	MembershipLevel int32
	EffectiveAt     time.Time
	ExpiredAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// InsertMembership 向 user_service.user_membership 插入单行当前会员权益记录。
func InsertMembership(sd *seed.Session, row MembershipSeed) {
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = row.EffectiveAt
	}
	updatedAt := row.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	sd.Exec("user", `
INSERT INTO user_service.user_membership
    (id, user_id, membership_level, effective_at, expired_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.ID,
		row.UserID,
		row.MembershipLevel,
		row.EffectiveAt.UTC().Truncate(time.Millisecond),
		row.ExpiredAt.UTC().Truncate(time.Millisecond),
		createdAt.UTC().Truncate(time.Millisecond),
		updatedAt.UTC().Truncate(time.Millisecond),
	)
}

// ChangeRecordSeed 映射 user_service.membership_change_record 单行权益变更流水。
type ChangeRecordSeed struct {
	ID                    int64
	UserID                int64
	MembershipID          int64
	OrderID               int64
	ChangeType            int32
	BeforeMembershipLevel sql.NullInt32
	BeforeEffectiveAt     sql.NullTime
	BeforeExpiredAt       sql.NullTime
	AfterMembershipLevel  int32
	AfterEffectiveAt      time.Time
	AfterExpiredAt        time.Time
	CreatedAt             time.Time
}

// InsertChangeRecord 向 user_service.membership_change_record 插入单行权益变更历史记录。
func InsertChangeRecord(sd *seed.Session, row ChangeRecordSeed) {
	var beforeEff sql.NullTime
	if row.BeforeEffectiveAt.Valid {
		beforeEff = sql.NullTime{Time: row.BeforeEffectiveAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}
	var beforeExp sql.NullTime
	if row.BeforeExpiredAt.Valid {
		beforeExp = sql.NullTime{Time: row.BeforeExpiredAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}
	sd.Exec("user", `
INSERT INTO user_service.membership_change_record
    (id, user_id, membership_id, order_id, change_type,
     before_membership_level, before_effective_at, before_expired_at,
     after_membership_level, after_effective_at, after_expired_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID,
		row.UserID,
		row.MembershipID,
		row.OrderID,
		row.ChangeType,
		row.BeforeMembershipLevel,
		beforeEff,
		beforeExp,
		row.AfterMembershipLevel,
		row.AfterEffectiveAt.UTC().Truncate(time.Millisecond),
		row.AfterExpiredAt.UTC().Truncate(time.Millisecond),
		row.CreatedAt.UTC().Truncate(time.Millisecond),
	)
}

// QueryStoredProfile 通过独立数据库连接读取指定 userID 的最新持久化行。
func QueryStoredProfile(t testing.TB, env *harness.Environment, userID int64) StoredProfile {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var row StoredProfile
	var enabledInt int
	err := env.DB(t, "user").QueryRowContext(ctx, `
SELECT id, account_source, external_account_id, user_name, avatar_url, is_enabled, version, created_at, updated_at
FROM user_service.user_profile
WHERE id = ?`, userID).Scan(
		&row.ID,
		&row.AccountSource,
		&row.ExternalAccountID,
		&row.UserName,
		&row.AvatarURL,
		&enabledInt,
		&row.Version,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("userdom.QueryStoredProfile(id=%d): %v", userID, err)
	}
	row.IsEnabled = enabledInt == 1
	row.CreatedAt = row.CreatedAt.UTC()
	row.UpdatedAt = row.UpdatedAt.UTC()
	return row
}

// QueryStoredMembership 通过独立数据库连接读取指定 userID 的当前会员权益快照行。
func QueryStoredMembership(t testing.TB, env *harness.Environment, userID int64) StoredMembership {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var row StoredMembership
	err := env.DB(t, "user").QueryRowContext(ctx, `
SELECT id, user_id, membership_level, effective_at, expired_at, created_at, updated_at
FROM user_service.user_membership
WHERE user_id = ?`, userID).Scan(
		&row.ID,
		&row.UserID,
		&row.MembershipLevel,
		&row.EffectiveAt,
		&row.ExpiredAt,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("userdom.QueryStoredMembership(user_id=%d): %v", userID, err)
	}
	row.EffectiveAt = row.EffectiveAt.UTC()
	row.ExpiredAt = row.ExpiredAt.UTC()
	row.CreatedAt = row.CreatedAt.UTC()
	row.UpdatedAt = row.UpdatedAt.UTC()
	return row
}
