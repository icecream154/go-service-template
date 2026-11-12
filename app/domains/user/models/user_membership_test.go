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
	minimumTime := time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)
	maximumTime := time.Date(9999, 12, 31, 23, 59, 59, 999000000, time.UTC)
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
		// 仅权益 ID 为零，其余状态合法，验证不能删除 ID 的正数检查。
		{name: "zero membership ID", at: effectiveAt, modify: func(m *models.UserMembership) { m.ID = 0 }, failed: true},
		// 负权益 ID 也应拒绝，避免将正数检查弱化成只排除零。
		{name: "negative membership ID", at: effectiveAt, modify: func(m *models.UserMembership) { m.ID = -1 }, failed: true},
		// 仅用户 ID 为零，验证权益 ID 合法不能掩盖所属用户无效。
		{name: "zero user ID", at: effectiveAt, modify: func(m *models.UserMembership) { m.UserID = 0 }, failed: true},
		// 仅用户 ID 为负数，验证所属用户必须为正数而不是非零。
		{name: "negative user ID", at: effectiveAt, modify: func(m *models.UserMembership) { m.UserID = -1 }, failed: true},
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
		// 查询仍位于有效区间内，仅亚毫秒尾数无效，不能靠期限判断拒绝此输入。
		{name: "submillisecond query time", at: effectiveAt.Add(time.Nanosecond), failed: true},
		// 将生效时刻提前一纳秒，保证期限及查询先后顺序合法，只验证精度要求。
		{name: "submillisecond effective time", at: effectiveAt, modify: func(m *models.UserMembership) { m.EffectiveAt = m.EffectiveAt.Add(-time.Nanosecond) }, failed: true},
		// 纳秒尾数不能通过读时截断偷偷修复。
		{name: "submillisecond expiry", at: effectiveAt, modify: func(m *models.UserMembership) { m.ExpiredAt = m.ExpiredAt.Add(time.Nanosecond) }, failed: true},
		// 最小 UTC 时刻同时作为生效和查询时刻合法，验证年份下界包含 1000。
		{name: "minimum UTC date", at: minimumTime, modify: func(m *models.UserMembership) {
			m.EffectiveAt, m.ExpiredAt = minimumTime, minimumTime.Add(time.Millisecond)
		}, status: "ACTIVE"},
		// 最大 UTC 毫秒同时作为到期和查询时刻合法，到期状态仍应正常返回。
		{name: "maximum UTC date", at: maximumTime, modify: func(m *models.UserMembership) {
			m.EffectiveAt, m.ExpiredAt = maximumTime.Add(-time.Millisecond), maximumTime
		}, status: "EXPIRED"},
		// 999 年的查询时刻单独无效；无权益对象避免其他状态检查掩盖时间下界。
		{name: "query before minimum date", at: minimumTime.Add(-time.Millisecond), nilRow: true, failed: true},
		// 10000 年的查询时刻单独无效，不能因尚未开通而跳过时间上界检查。
		{name: "query after maximum date", at: maximumTime.Add(time.Millisecond), nilRow: true, failed: true},
		// 生效时间仅比下界早一毫秒，查询与到期时间均合法且保持先后顺序。
		{name: "effective time before minimum date", at: effectiveAt, modify: func(m *models.UserMembership) {
			m.EffectiveAt = minimumTime.Add(-time.Millisecond)
		}, failed: true},
		// 到期时间仅比上界晚一毫秒，其他状态合法，不能把异常数据当作有效会员。
		{name: "expiry after maximum date", at: effectiveAt, modify: func(m *models.UserMembership) {
			m.ExpiredAt = maximumTime.Add(time.Millisecond)
		}, failed: true},
		// 最小 UTC 时刻在 UTC-8 表示为 999 年，按同一时刻判断仍应合法且有效。
		{name: "minimum date in another timezone", at: minimumTime.In(time.FixedZone("UTC-8", -8*60*60)), modify: func(m *models.UserMembership) {
			m.EffectiveAt = minimumTime.In(time.FixedZone("UTC-8", -8*60*60))
			m.ExpiredAt = minimumTime.Add(time.Millisecond)
		}, status: "ACTIVE"},
		// 最大 UTC 时刻在 UTC+8 表示为 10000 年，时区不应改变合法范围和到期状态。
		{name: "maximum date in another timezone", at: maximumTime.In(time.FixedZone("UTC+8", 8*60*60)), modify: func(m *models.UserMembership) {
			m.EffectiveAt = maximumTime.Add(-time.Millisecond)
			m.ExpiredAt = maximumTime.In(time.FixedZone("UTC+8", 8*60*60))
		}, status: "EXPIRED"},
		// 即使没有权益对象，空查询时刻仍属于内部异常，不能提前返回未开通。
		{name: "zero query time", nilRow: true, failed: true},
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
