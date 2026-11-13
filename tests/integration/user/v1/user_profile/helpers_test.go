//go:build integration

package userprofile_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"go-service-template/tests/harness"
)

// wireUserProfile 用于独立核对线上 JSON 协议中的用户资料结构（例如 user_id 必须编码为十进制字符串、version 显式存在）。
type wireUserProfile struct {
	UserID    string          `json:"user_id"`
	UserName  string          `json:"user_name"`
	AvatarURL json.RawMessage `json:"avatar_url"`
	IsEnabled bool            `json:"is_enabled"`
	Version   *int64          `json:"version"`
}

// userProfileSeed 描述向 user_service.user_profile 插入单行前置测试数据的完整字段。
type userProfileSeed struct {
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

// storedUserProfile 保存从数据库独立连接回读的 user_service.user_profile 行快照。
type storedUserProfile struct {
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

func seedUserProfile(t testing.TB, env *harness.Environment, seed userProfileSeed) {
	t.Helper()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	createdAt := seed.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := seed.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	var enabledTinyInt int
	if seed.IsEnabled {
		enabledTinyInt = 1
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := env.DB(t, "user").ExecContext(ctx, `
INSERT INTO user_service.user_profile
    (id, account_source, external_account_id, user_name, avatar_url, is_enabled, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seed.ID,
		seed.AccountSource,
		seed.ExternalAccountID,
		seed.UserName,
		seed.AvatarURL,
		enabledTinyInt,
		seed.Version,
		createdAt.UTC().Truncate(time.Millisecond),
		updatedAt.UTC().Truncate(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("seed user_profile (id=%d): %v", seed.ID, err)
	}
}

func queryStoredUserProfileByID(t testing.TB, env *harness.Environment, userID int64) storedUserProfile {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var row storedUserProfile
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
		t.Fatalf("query stored user_profile by id=%d: %v", userID, err)
	}
	row.IsEnabled = enabledInt == 1
	return row
}

func queryStoredUserProfileByAccount(t testing.TB, env *harness.Environment, accountSource, externalAccountID string) storedUserProfile {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var row storedUserProfile
	var enabledInt int
	err := env.DB(t, "user").QueryRowContext(ctx, `
SELECT id, account_source, external_account_id, user_name, avatar_url, is_enabled, version, created_at, updated_at
FROM user_service.user_profile
WHERE account_source = ? AND external_account_id = ?`, accountSource, externalAccountID).Scan(
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
		t.Fatalf("query stored user_profile by account=(%q, %q): %v", accountSource, externalAccountID, err)
	}
	row.IsEnabled = enabledInt == 1
	return row
}

func countStoredUserProfiles(t testing.TB, env *harness.Environment) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var count int
	if err := env.DB(t, "user").QueryRowContext(ctx, `SELECT COUNT(*) FROM user_service.user_profile`).Scan(&count); err != nil {
		t.Fatalf("count user_service.user_profile rows: %v", err)
	}
	return count
}
