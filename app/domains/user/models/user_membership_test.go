package models_test

import (
	"errors"
	"testing"
	"time"

	"go-service-template/app/domains/user/models"
	"go-service-template/internal/check"
)

func TestUserMembershipStatusAt(t *testing.T) {
	effectiveAt := time.Date(2026, 11, 2, 2, 0, 0, 123000000, time.UTC)
	expiredAt := effectiveAt.Add(30 * 24 * time.Hour)
	base := models.UserMembership{ID: 456, UserID: 123, MembershipLevel: 10, EffectiveAt: effectiveAt, ExpiredAt: expiredAt}
	tests := []struct {
		name   string
		at     time.Time
		modify func(*models.UserMembership)
		nilRow bool
		status string
		failed bool
	}{
		// 无权益记录是正常的未开通状态。
		{name: "not opened", at: effectiveAt, nilRow: true, status: "NOT_OPENED"},
		// 生效时刻属于有效区间，验证左边界包含。
		{name: "effective boundary", at: effectiveAt, status: "ACTIVE"},
		// 到期前最后一毫秒仍有效。
		{name: "before expiry", at: expiredAt.Add(-time.Millisecond), status: "ACTIVE"},
		// 到期时刻立即失效，验证右边界不包含。
		{name: "expiry boundary", at: expiredAt, status: "EXPIRED"},
		// 过期后原记录仍存在并保持等级。
		{name: "after expiry", at: expiredAt.Add(time.Millisecond), status: "EXPIRED"},
		// 当前业务不支持未来生效，不能显示为普通未开通。
		{name: "future effective time", at: effectiveAt.Add(-time.Millisecond), failed: true},
		// 未知等级是持久化异常，不是查询参数不合法。
		{name: "unknown level", at: effectiveAt, modify: func(m *models.UserMembership) { m.MembershipLevel = 20 }, failed: true},
		// 相同的起止时刻不能形成有效权益区间。
		{name: "empty validity interval", at: effectiveAt, modify: func(m *models.UserMembership) { m.ExpiredAt = m.EffectiveAt }, failed: true},
		// 反向的起止时刻不能被当作已过期。
		{name: "reversed validity interval", at: effectiveAt, modify: func(m *models.UserMembership) { m.ExpiredAt = m.EffectiveAt.Add(-time.Millisecond) }, failed: true},
		// 零日期不能当作历史有效时间。
		{name: "zero effective time", at: effectiveAt, modify: func(m *models.UserMembership) { m.EffectiveAt = time.Time{} }, failed: true},
		// 纳秒尾数不能通过读时截断偷偷修复。
		{name: "submillisecond expiry", at: effectiveAt, modify: func(m *models.UserMembership) { m.ExpiredAt = m.ExpiredAt.Add(time.Nanosecond) }, failed: true},
		// 空查询时刻属于服务内部调用异常。
		{name: "zero query time", failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := base
			if tt.modify != nil {
				tt.modify(&value)
			}
			before := value
			membership := &value
			if tt.nilRow {
				membership = nil
			}
			status, err := membership.StatusAt(tt.at)
			if tt.failed {
				if !errors.Is(err, models.ErrInvalidUserMembershipState) || errors.Is(err, check.ErrInvalidValue) || status != "" {
					t.Fatalf("StatusAt = (%q, %v), want internal state error", status, err)
				}
			} else if err != nil || status != tt.status {
				t.Fatalf("StatusAt = (%q, %v), want (%q, nil)", status, err, tt.status)
			}
			if value != before {
				t.Fatal("StatusAt modified membership")
			}
		})
	}
}
