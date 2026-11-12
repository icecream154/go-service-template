package usermembership_test

import (
	"math"
	"testing"
	"time"

	usermembership "go-service-template/app/domains/user/dto/v1/user_membership"
)

func TestMembershipChangeRecordJSON(t *testing.T) {
	createdAt := time.Date(2026, 11, 12, 2, 0, 0, 123000000, time.UTC)
	// 首次开通保留全部三个 null，三个 ID 均按字符串编码。
	record := usermembership.MembershipChangeRecord{
		RecordID: math.MaxInt64, MembershipID: math.MaxInt64 - 1, OrderID: math.MaxInt64 - 2,
		ChangeType: 10, AfterMembershipLevel: 10, AfterEffectiveAt: createdAt,
		AfterExpiredAt: createdAt.Add(30 * 24 * time.Hour), CreatedAt: createdAt,
	}
	fields := membershipJSONFields(t, record)
	assertMembershipJSONField(t, fields, "record_id", `"9223372036854775807"`)
	assertMembershipJSONField(t, fields, "membership_id", `"9223372036854775806"`)
	assertMembershipJSONField(t, fields, "order_id", `"9223372036854775805"`)
	assertMembershipJSONField(t, fields, "change_type", "10")
	assertMembershipJSONField(t, fields, "before_membership_level", "null")
	assertMembershipJSONField(t, fields, "before_effective_at", "null")
	assertMembershipJSONField(t, fields, "before_expired_at", "null")
	assertMembershipJSONField(t, fields, "after_membership_level", "10")
	assertMembershipJSONField(t, fields, "after_effective_at", `"2026-11-12T02:00:00.123Z"`)
	assertMembershipJSONField(t, fields, "after_expired_at", `"2026-12-12T02:00:00.123Z"`)
	assertMembershipJSONField(t, fields, "created_at", `"2026-11-12T02:00:00.123Z"`)

	// 续费的 before 快照有值时保持实际数字和时间，不能误编码为 null。
	level := int32(10)
	record.ChangeType = 20
	record.BeforeMembershipLevel = &level
	record.BeforeEffectiveAt = &createdAt
	record.BeforeExpiredAt = &record.AfterExpiredAt
	fields = membershipJSONFields(t, record)
	assertMembershipJSONField(t, fields, "change_type", "20")
	assertMembershipJSONField(t, fields, "before_membership_level", "10")
	assertMembershipJSONField(t, fields, "before_effective_at", `"2026-11-12T02:00:00.123Z"`)
	assertMembershipJSONField(t, fields, "before_expired_at", `"2026-12-12T02:00:00.123Z"`)
}

func TestQueryMembershipChangeRecordPageResponseJSON(t *testing.T) {
	// 空页沿用已构造的空集合，下一页令牌字段保留 null。
	fields := membershipJSONFields(t, usermembership.QueryMembershipChangeRecordPageResponse{
		MembershipChangeRecordList: []usermembership.MembershipChangeRecord{}, PageSize: 20,
	})
	assertMembershipJSONField(t, fields, "membership_change_record_list", "[]")
	assertMembershipJSONField(t, fields, "page_size", "20")
	assertMembershipJSONField(t, fields, "has_more", "false")
	assertMembershipJSONField(t, fields, "next_page_token", "null")
}
