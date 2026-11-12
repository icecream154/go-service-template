package usermembership_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	usermembership "go-service-template/app/domains/user/dto/v1/user_membership"
)

func TestQueryUserMembershipByUserIDResponseJSON(t *testing.T) {
	queriedAt := time.Date(2026, 11, 12, 2, 0, 0, 123000000, time.UTC)
	// 未开通仍有用户 ID 和查询时间，membership 必须显式为 null。
	unopened := membershipJSONFields(t, usermembership.QueryUserMembershipByUserIDResponse{
		UserID: math.MaxInt64, MembershipStatus: "NOT_OPENED", QueriedAt: queriedAt,
	})
	assertMembershipJSONField(t, unopened, "user_id", `"9223372036854775807"`)
	assertMembershipJSONField(t, unopened, "membership_status", `"NOT_OPENED"`)
	assertMembershipJSONField(t, unopened, "queried_at", `"2026-11-12T02:00:00.123Z"`)
	assertMembershipJSONField(t, unopened, "membership", "null")

	// 过期权益仍输出大整数会员标识、等级及原时间，不转为零值。
	expired := membershipJSONFields(t, usermembership.QueryUserMembershipByUserIDResponse{
		UserID: 42, MembershipStatus: "EXPIRED", QueriedAt: queriedAt,
		Membership: &usermembership.UserMembership{
			MembershipID: math.MaxInt64, MembershipLevel: 10,
			EffectiveAt: queriedAt.Add(-30 * 24 * time.Hour), ExpiredAt: queriedAt,
		},
	})
	var membership map[string]json.RawMessage
	if err := json.Unmarshal(expired["membership"], &membership); err != nil {
		t.Fatalf("decode membership: %v", err)
	}
	assertMembershipJSONField(t, expired, "membership_status", `"EXPIRED"`)
	assertMembershipJSONField(t, membership, "membership_id", `"9223372036854775807"`)
	assertMembershipJSONField(t, membership, "membership_level", "10")
	assertMembershipJSONField(t, membership, "effective_at", `"2026-10-13T02:00:00.123Z"`)
	assertMembershipJSONField(t, membership, "expired_at", `"2026-11-12T02:00:00.123Z"`)
}

func membershipJSONFields(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode DTO fields: %v", err)
	}
	return fields
}

func assertMembershipJSONField(t *testing.T, fields map[string]json.RawMessage, name, want string) {
	t.Helper()
	if got := string(fields[name]); got != want {
		t.Errorf("JSON %s = %s, want %s", name, got, want)
	}
}
