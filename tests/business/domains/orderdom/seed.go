//go:build business

// Package orderdom 提供 order 业务域在业务测试中的单表原子造数映射与持久化回读辅助。
package orderdom

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/seed"
)

const (
	// PaymentStatusPending 表示订单待支付（10）。
	PaymentStatusPending int32 = 10
	// PaymentStatusPaidAndFulfilled 表示订单已支付且已完成权益发放（20）。
	PaymentStatusPaidAndFulfilled int32 = 20
)

// MembershipOrderSeed 映射 order_service.membership_order 单行前置记录。
type MembershipOrderSeed struct {
	ID                   int64
	OrderNo              string
	UserID               int64
	PlanCode             string
	PlanVersion          int64
	MembershipLevel      int32
	DurationSeconds      int64
	PayableAmount        int64
	CurrencyCode         string
	PaymentStatus        int32
	PaymentChannel       string
	PaymentTransactionNo sql.NullString
	PaidAt               sql.NullTime
	FulfilledAt          sql.NullTime
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// StoredMembershipOrder 保存从独立数据库连接回读的 order_service.membership_order 行快照。
type StoredMembershipOrder struct {
	ID                   int64
	OrderNo              string
	UserID               int64
	PlanCode             string
	PlanVersion          int64
	MembershipLevel      int32
	DurationSeconds      int64
	PayableAmount        int64
	CurrencyCode         string
	PaymentStatus        int32
	PaymentChannel       string
	PaymentTransactionNo sql.NullString
	PaidAt               sql.NullTime
	FulfilledAt          sql.NullTime
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// InsertMembershipOrder 向 order_service.membership_order 插入单行前置订单记录（受 seed.Session.Freeze 约束）。
func InsertMembershipOrder(sd *seed.Session, row MembershipOrderSeed) {
	currency := row.CurrencyCode
	if currency == "" {
		currency = "CNY"
	}
	createdAt := row.CreatedAt
	if createdAt.IsZero() {
		createdAt = sd.BaseTime()
	}
	updatedAt := row.UpdatedAt
	if updatedAt.IsZero() {
		if row.FulfilledAt.Valid {
			updatedAt = row.FulfilledAt.Time
		} else {
			updatedAt = createdAt
		}
	}

	var paidAt sql.NullTime
	if row.PaidAt.Valid {
		paidAt = sql.NullTime{Time: row.PaidAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}
	var fulfilledAt sql.NullTime
	if row.FulfilledAt.Valid {
		fulfilledAt = sql.NullTime{Time: row.FulfilledAt.Time.UTC().Truncate(time.Millisecond), Valid: true}
	}

	sd.Exec("order", `
INSERT INTO order_service.membership_order
    (id, order_no, user_id, plan_code, plan_version, membership_level, duration_seconds,
     payable_amount, currency_code, payment_status, payment_channel, payment_transaction_no,
     paid_at, fulfilled_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID,
		row.OrderNo,
		row.UserID,
		row.PlanCode,
		row.PlanVersion,
		row.MembershipLevel,
		row.DurationSeconds,
		row.PayableAmount,
		currency,
		row.PaymentStatus,
		row.PaymentChannel,
		row.PaymentTransactionNo,
		paidAt,
		fulfilledAt,
		createdAt.UTC().Truncate(time.Millisecond),
		updatedAt.UTC().Truncate(time.Millisecond),
	)
}

// QueryStoredMembershipOrder 通过独立数据库连接读取指定订单 ID 的持久化行。
func QueryStoredMembershipOrder(t testing.TB, env *harness.Environment, orderID int64) StoredMembershipOrder {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var row StoredMembershipOrder
	err := env.DB(t, "order").QueryRowContext(ctx, `
SELECT id, order_no, user_id, plan_code, plan_version, membership_level, duration_seconds,
       payable_amount, currency_code, payment_status, payment_channel, payment_transaction_no,
       paid_at, fulfilled_at, created_at, updated_at
FROM order_service.membership_order
WHERE id = ?`, orderID).Scan(
		&row.ID,
		&row.OrderNo,
		&row.UserID,
		&row.PlanCode,
		&row.PlanVersion,
		&row.MembershipLevel,
		&row.DurationSeconds,
		&row.PayableAmount,
		&row.CurrencyCode,
		&row.PaymentStatus,
		&row.PaymentChannel,
		&row.PaymentTransactionNo,
		&row.PaidAt,
		&row.FulfilledAt,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("orderdom.QueryStoredMembershipOrder(id=%d): %v", orderID, err)
	}
	return row
}
