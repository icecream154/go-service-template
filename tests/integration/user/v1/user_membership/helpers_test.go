//go:build integration

package usermembership_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go-service-template/tests/harness"
)

type membershipUserSeed struct {
	ID                int64
	AccountSource     string
	ExternalAccountID string
	UserName          string
	IsEnabled         bool
}

type userMembershipSeed struct {
	ID              int64
	UserID          int64
	MembershipLevel int32
	EffectiveAt     time.Time
	ExpiredAt       time.Time
}

type membershipChangeRecordSeed struct {
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

func seedMembershipUser(t testing.TB, env *harness.Environment, seed membershipUserSeed) {
	t.Helper()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	enabled := 0
	if seed.IsEnabled {
		enabled = 1
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := env.DB(t, "user").ExecContext(ctx, `
INSERT INTO user_service.user_profile
    (id, account_source, external_account_id, user_name, avatar_url, is_enabled, version, created_at, updated_at)
VALUES (?, ?, ?, ?, NULL, ?, 0, ?, ?)`,
		seed.ID, seed.AccountSource, seed.ExternalAccountID, seed.UserName, enabled, now, now,
	)
	if err != nil {
		t.Fatalf("seed user_profile for membership test (id=%d): %v", seed.ID, err)
	}
}

func seedUserMembership(t testing.TB, env *harness.Environment, seed userMembershipSeed) {
	t.Helper()
	now := seed.EffectiveAt.UTC().Truncate(time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := env.DB(t, "user").ExecContext(ctx, `
INSERT INTO user_service.user_membership
    (id, user_id, membership_level, effective_at, expired_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		seed.ID,
		seed.UserID,
		seed.MembershipLevel,
		seed.EffectiveAt.UTC().Truncate(time.Millisecond),
		seed.ExpiredAt.UTC().Truncate(time.Millisecond),
		now,
		now,
	)
	if err != nil {
		t.Fatalf("seed user_membership (id=%d): %v", seed.ID, err)
	}
}

func queryStoredUserMembershipExpiredAt(t testing.TB, env *harness.Environment, membershipID int64) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var expiredAt time.Time
	if err := env.DB(t, "user").QueryRowContext(ctx,
		`SELECT expired_at FROM user_service.user_membership WHERE id = ?`, membershipID,
	).Scan(&expiredAt); err != nil {
		t.Fatalf("query user_membership (id=%d): %v", membershipID, err)
	}
	return expiredAt.UTC()
}

func seedMembershipChangeRecord(t testing.TB, env *harness.Environment, seed membershipChangeRecordSeed) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var beforeEff sql.NullTime
	if seed.BeforeEffectiveAt.Valid {
		beforeEff = sql.NullTime{Time: seed.BeforeEffectiveAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}
	var beforeExp sql.NullTime
	if seed.BeforeExpiredAt.Valid {
		beforeExp = sql.NullTime{Time: seed.BeforeExpiredAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}

	_, err := env.DB(t, "user").ExecContext(ctx, `
INSERT INTO user_service.membership_change_record
    (id, user_id, membership_id, order_id, change_type,
     before_membership_level, before_effective_at, before_expired_at,
     after_membership_level, after_effective_at, after_expired_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seed.ID,
		seed.UserID,
		seed.MembershipID,
		seed.OrderID,
		seed.ChangeType,
		seed.BeforeMembershipLevel,
		beforeEff,
		beforeExp,
		seed.AfterMembershipLevel,
		seed.AfterEffectiveAt.UTC().Truncate(time.Millisecond),
		seed.AfterExpiredAt.UTC().Truncate(time.Millisecond),
		seed.CreatedAt.UTC().Truncate(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("seed membership_change_record (id=%d): %v", seed.ID, err)
	}
}
