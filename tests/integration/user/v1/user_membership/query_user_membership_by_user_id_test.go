//go:build integration

// 接口集成测试：GET /api/v1/user/userMembership/queryUserMembershipByUserId
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_membership/query_user_membership_by_user_id_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_membership_controller.go -> (*UserMembershipController).QueryUserMembershipByUserID
// 对应服务    ：app/domains/user/service/user_membership_service.go -> (*UserMembershipService).QueryUserMembershipByUserID
//
// 测试场景总览（共 7 个场景）：
//   01_not_opened_when_user_has_no_membership     [正常链路]       用户存在但无权益记录时返回 NOT_OPENED 且 membership 显式为 null
//   02_active_membership_within_effective_window  [正常链路]       当前时刻位于 [effective_at, expired_at) 的标准会员（等级 10，含停用用户）返回 ACTIVE 与完整权益
//   03_expired_membership_retains_snapshot_row    [正常链路]       当前时刻已过 expired_at 时返回 EXPIRED 且仍携带原权益快照，数据库记录不被删除或改写
//   04_user_profile_not_found                     [业务状态冲突]   区分“未开通”与“用户不存在”：用户资料缺失时返回 404 NOT_FOUND + USER_PROFILE_NOT_FOUND
//   05_invalid_stored_membership_returns_500      [业务状态冲突]   持久化权益违反领域不变量（如非标准等级 99 或未来生效时间）时归为 500 INTERNAL_ERROR（不伪装为 400）
//   06_forbid_cross_user_or_reject_anonymous      [认证与权限]     跨用户查询、service/admin 身份或匿名访问分别返回 403 USER_MEMBERSHIP_PERMISSION_DENIED / 401
//   07_reject_invalid_user_id_query               [参数与协议]     缺失或非法 user_id 在 Validator 层返回 400 INVALID_ARGUMENT 并定位字段 user_id
package usermembership_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
)

const queryUserMembershipByUserIDPath = "/api/v1/user/userMembership/queryUserMembershipByUserId"

type wireUserMembership struct {
	MembershipID    string    `json:"membership_id"`
	MembershipLevel int32     `json:"membership_level"`
	EffectiveAt     time.Time `json:"effective_at"`
	ExpiredAt       time.Time `json:"expired_at"`
}

type queryUserMembershipData struct {
	UserID           string          `json:"user_id"`
	MembershipStatus string          `json:"membership_status"`
	QueriedAt        time.Time       `json:"queried_at"`
	Membership       json.RawMessage `json:"membership"`
}

func TestQueryUserMembershipByUserID(t *testing.T) {
	t.Run("01_not_opened_when_user_has_no_membership", func(t *testing.T) {
		// [业务解释]
		// 已注册但从未购买过会员的用户属于正常业务状态（而非 404）：接口返回 HTTP 200，
		// membership_status 为 "NOT_OPENED"，且 membership 字段保持显式 JSON null。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 70001
		seedMembershipUser(t, env, membershipUserSeed{
			ID: userID, AccountSource: "wx", ExternalAccountID: "m_70001", UserName: "FreeUser", IsEnabled: true,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "70001"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[queryUserMembershipData](t, resp)
		if data.UserID != "70001" || data.MembershipStatus != "NOT_OPENED" || string(data.Membership) != "null" || data.QueriedAt.IsZero() {
			t.Fatalf("unexpected NOT_OPENED response: %+v (membership=%s)", data, data.Membership)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_active_membership_within_effective_window", func(t *testing.T) {
		// [业务解释]
		// 当查询时刻满足 effective_at <= queried_at < expired_at 且会员等级为 10（标准会员）时，
		// 即使该账号处于停用状态（is_enabled=0），本人仍可正常读取到 ACTIVE 状态与毫秒级 UTC 期限。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 70002
		const membershipID int64 = 80002
		now := time.Now().UTC().Truncate(time.Millisecond)
		effectiveAt := now.Add(-24 * time.Hour)
		expiredAt := now.Add(30 * 24 * time.Hour)

		seedMembershipUser(t, env, membershipUserSeed{
			ID: userID, AccountSource: "wx", ExternalAccountID: "m_70002", UserName: "DisabledVIP", IsEnabled: false,
		})
		seedUserMembership(t, env, userMembershipSeed{
			ID:              membershipID,
			UserID:          userID,
			MembershipLevel: 10,
			EffectiveAt:     effectiveAt,
			ExpiredAt:       expiredAt,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "70002"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[queryUserMembershipData](t, resp)
		if data.UserID != "70002" || data.MembershipStatus != "ACTIVE" {
			t.Fatalf("unexpected ACTIVE membership status: %+v", data)
		}
		var detail wireUserMembership
		if err := json.Unmarshal(data.Membership, &detail); err != nil {
			t.Fatalf("unmarshal active membership object: %v", err)
		}
		if detail.MembershipID != "80002" || detail.MembershipLevel != 10 ||
			!detail.EffectiveAt.Equal(effectiveAt) || !detail.ExpiredAt.Equal(expiredAt) {
			t.Fatalf("unexpected active membership detail: %+v", detail)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_expired_membership_retains_snapshot_row", func(t *testing.T) {
		// [业务解释]
		// 已到期的会员记录不从数据库物理删除，查询时派生状态为 "EXPIRED" 并继续返回最近一次权益期限供前端展示“已于某时过期”，
		// 查询过程严禁回写或清理数据库中的过期行。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 70003
		const membershipID int64 = 80003
		now := time.Now().UTC().Truncate(time.Millisecond)
		effectiveAt := now.Add(-40 * 24 * time.Hour)
		expiredAt := now.Add(-10 * 24 * time.Hour)

		seedMembershipUser(t, env, membershipUserSeed{
			ID: userID, AccountSource: "wx", ExternalAccountID: "m_70003", UserName: "ExpiredVIP", IsEnabled: true,
		})
		seedUserMembership(t, env, userMembershipSeed{
			ID:              membershipID,
			UserID:          userID,
			MembershipLevel: 10,
			EffectiveAt:     effectiveAt,
			ExpiredAt:       expiredAt,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "70003"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[queryUserMembershipData](t, resp)
		if data.MembershipStatus != "EXPIRED" || string(data.Membership) == "null" {
			t.Fatalf("expected EXPIRED with non-null membership object, got %+v (membership=%s)", data, data.Membership)
		}
		var detail wireUserMembership
		if err := json.Unmarshal(data.Membership, &detail); err != nil {
			t.Fatalf("unmarshal expired membership object: %v", err)
		}
		if detail.MembershipID != "80003" || !detail.ExpiredAt.Equal(expiredAt) {
			t.Fatalf("unexpected expired membership detail: %+v", detail)
		}

		// [5. 存储终态与调用副作用核对]
		storedExp := queryStoredUserMembershipExpiredAt(t, env, membershipID)
		if !storedExp.Equal(expiredAt) {
			t.Fatalf("stored expired_at = %v, want unchanged %v", storedExp, expiredAt)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("04_user_profile_not_found", func(t *testing.T) {
		// [业务解释]
		// Service 先核对 user_profile 是否存在：若主档不存在，不能误判为 NOT_OPENED，必须返回 404 USER_PROFILE_NOT_FOUND。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		memberToken := env.Auth.IssueMemberToken(t, 70999)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "70999"),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusNotFound, "NOT_FOUND", "USER_PROFILE_NOT_FOUND")

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("05_invalid_stored_membership_returns_500", func(t *testing.T) {
		// [业务解释]
		// 当数据库中存在违反当前标准会员规则的脏数据（例如 membership_level != 10 或 effective_at 在未来）时，
		// StatusAt 返回内部状态错误，Controller 将其映射为 500 INTERNAL_ERROR，而非归咎于客户端请求参数（400）。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 70005
		now := time.Now().UTC().Truncate(time.Millisecond)
		seedMembershipUser(t, env, membershipUserSeed{
			ID: userID, AccountSource: "wx", ExternalAccountID: "m_70005", UserName: "DirtyStateUser", IsEnabled: true,
		})
		seedUserMembership(t, env, userMembershipSeed{
			ID:              80005,
			UserID:          userID,
			MembershipLevel: 99, // 非法等级（仅允许 10）
			EffectiveAt:     now.Add(-time.Hour),
			ExpiredAt:       now.Add(time.Hour),
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "70005"),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusInternalServerError, "INTERNAL_ERROR", "")

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("06_forbid_cross_user_or_reject_anonymous", func(t *testing.T) {
		cases := []struct {
			name             string
			businessExplain  string
			authHeader       func(t *testing.T, env *harness.Environment) string
			wantStatus       int
			wantCode         string
			wantBusinessCode string
		}{
			{
				name:             "member_queries_other_user_membership",
				businessExplain:  "成员 70010 尝试读取成员 70011 的会员权益，返回 403 PERMISSION_DENIED + USER_MEMBERSHIP_PERMISSION_DENIED",
				authHeader:       func(t *testing.T, env *harness.Environment) string { return "Bearer " + env.Auth.IssueMemberToken(t, 70010) },
				wantStatus:       http.StatusForbidden,
				wantCode:         "PERMISSION_DENIED",
				wantBusinessCode: "USER_MEMBERSHIP_PERMISSION_DENIED",
			},
			{
				name:            "service_admin_not_permitted",
				businessExplain: "service/admin 服务身份调用本人会员接口同样被拒绝，返回 403 USER_MEMBERSHIP_PERMISSION_DENIED",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueServiceAdminToken(t, "billing-cron")
				},
				wantStatus:       http.StatusForbidden,
				wantCode:         "PERMISSION_DENIED",
				wantBusinessCode: "USER_MEMBERSHIP_PERMISSION_DENIED",
			},
			{
				name:            "anonymous_caller",
				businessExplain: "未携带身份凭证返回 401 UNAUTHENTICATED",
				authHeader:      func(*testing.T, *harness.Environment) string { return "" },
				wantStatus:      http.StatusUnauthorized,
				wantCode:        "UNAUTHENTICATED",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				header := tc.authHeader(t, env)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
					apicall.WithHeaderIfNonEmpty("Authorization", header),
					apicall.WithQuery("user_id", "70011"),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, tc.wantStatus, tc.wantCode, tc.wantBusinessCode)

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("07_reject_invalid_user_id_query", func(t *testing.T) {
		// [业务解释]
		// Query 参数 user_id 缺失或非正整数时，Validator 在进入 Controller 前直接返回 400 INVALID_ARGUMENT 并指明 user_id。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		memberToken := env.Auth.IssueMemberToken(t, 70020)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserMembershipByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "-1"),
		)

		// [4. 预期响应核对]
		errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
		apicall.RequireFieldError(t, errInfo, "user_id")

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})
}
