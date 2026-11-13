//go:build integration

// 接口集成测试：GET /api/v1/user/userProfile/queryUserProfileByUserId
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_profile/query_user_profile_by_user_id_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_profile_controller.go -> (*UserProfileController).QueryUserProfileByUserID
// 对应服务    ：app/domains/user/service/user_profile_service.go -> (*UserProfileService).QueryUserProfileByUserID
//
// 测试场景总览（共 6 个场景）：
//   01_member_queries_self_enabled_profile    [正常链路]       已认证成员查询本人启用资料，正确返回十进制字符串 user_id、空头像 null 与版本号
//   02_member_queries_self_disabled_profile   [正常链路]       已停用成员仍允许查询本人资料（is_enabled=false 及非空头像原样返回）
//   03_member_self_profile_not_found          [业务状态冲突]   有效成员凭证对应的资料行不存在时返回 404 NOT_FOUND + USER_PROFILE_NOT_FOUND
//   04_forbid_cross_user_and_service_caller   [认证与权限]     查询他人资料或使用 service/admin 身份调用返回 403 PERMISSION_DENIED + USER_PROFILE_PERMISSION_DENIED
//   05_reject_unauthenticated_or_bad_token    [认证与权限]     匿名、已过期或异构密钥签名凭证返回 401 UNAUTHENTICATED 及标准 WWW-Authenticate 挑战头
//   06_reject_invalid_user_id_query           [参数与协议]     缺失、零、负数或非数字 user_id 在 Validator 层返回 400 INVALID_ARGUMENT 并定位字段 user_id
package userprofile_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
)

const queryUserProfileByUserIDPath = "/api/v1/user/userProfile/queryUserProfileByUserId"

func TestQueryUserProfileByUserID(t *testing.T) {
	t.Run("01_member_queries_self_enabled_profile", func(t *testing.T) {
		// [业务解释]
		// 普通成员读取本人资料时，响应 data 直接承载 UserProfile 对象；
		// 内部 64 位整数 ID 必须序列化为十进制字符串避免前端精度丢失，数据库中 NULL 头像序列化为显式 JSON null。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const selfID int64 = 100001
		seedUserProfile(t, env, userProfileSeed{
			ID:                selfID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_100001",
			UserName:          "Alice Chen",
			AvatarURL:         sql.NullString{Valid: false},
			IsEnabled:         true,
			Version:           3,
		})
		memberToken := env.Auth.IssueMemberToken(t, selfID)

		// [2. 外部依赖模拟]
		// 纯本库读取接口：Company HTTP Client 与第三方 SDK 均不注册规则（预期 0 次调用）。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserProfileByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "100001"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[wireUserProfile](t, resp)
		if data.UserID != "100001" || data.UserName != "Alice Chen" || string(data.AvatarURL) != "null" ||
			!data.IsEnabled || data.Version == nil || *data.Version != 3 {
			t.Fatalf("unexpected profile payload: %+v (avatar=%s)", data, data.AvatarURL)
		}
		if traceID := resp.Header.Get("TraceId"); len(traceID) != 32 {
			t.Fatalf("expected 32-char TraceId response header, got %q", traceID)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_member_queries_self_disabled_profile", func(t *testing.T) {
		// [业务解释]
		// 根据用户资料契约，账号停用（is_enabled=0）仅禁止修改资料，本人仍可正常查询当前资料与停用状态以向前端展示封禁提示。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const disabledID int64 = 100002
		const avatar = "https://cdn.example.com/u/100002.png"
		seedUserProfile(t, env, userProfileSeed{
			ID:                disabledID,
			AccountSource:     "apple",
			ExternalAccountID: "ap_100002",
			UserName:          "Disabled Member",
			AvatarURL:         sql.NullString{String: avatar, Valid: true},
			IsEnabled:         false,
			Version:           1,
		})
		memberToken := env.Auth.IssueMemberToken(t, disabledID)

		// [2. 外部依赖模拟]
		// 无外部调用（预期 0 次）。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserProfileByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "100002"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[wireUserProfile](t, resp)
		if data.UserID != "100002" || data.IsEnabled || string(data.AvatarURL) != `"`+avatar+`"` ||
			data.Version == nil || *data.Version != 1 {
			t.Fatalf("unexpected disabled profile payload: %+v (avatar=%s)", data, data.AvatarURL)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_member_self_profile_not_found", func(t *testing.T) {
		// [业务解释]
		// JWT 仅证明签名可信的身份声明而不提前查库；当携带合法本人 Token 但数据库中无对应用户行时，应返回 404 USER_PROFILE_NOT_FOUND。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		// 数据库为空，不预置 ID=999001 的记录。
		memberToken := env.Auth.IssueMemberToken(t, 999001)

		// [2. 外部依赖模拟]
		// 无外部调用（预期 0 次）。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserProfileByUserIDPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "999001"),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusNotFound, "NOT_FOUND", "USER_PROFILE_NOT_FOUND")

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("04_forbid_cross_user_and_service_caller", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			token           func(t *testing.T, env *harness.Environment) string
			queryUserID     string
		}{
			{
				name:            "member_queries_another_member",
				businessExplain: "成员 100010 尝试读取成员 100011 的资料，Controller 在调用 Service 前按本人访问规则拦截并返回 403 USER_PROFILE_PERMISSION_DENIED",
				token: func(t *testing.T, env *harness.Environment) string {
					return env.Auth.IssueMemberToken(t, 100010)
				},
				queryUserID: "100011",
			},
			{
				name:            "service_admin_principal_not_allowed",
				businessExplain: "service/admin 虽是受信任的后台服务凭证，但本接口仅面向 user/member 本人访问，必须拒绝越类调用并返回 403 USER_PROFILE_PERMISSION_DENIED",
				token: func(t *testing.T, env *harness.Environment) string {
					return env.Auth.IssueServiceAdminToken(t, "user-admin-portal")
				},
				queryUserID: "100011",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				seedUserProfile(t, env, userProfileSeed{
					ID:                100011,
					AccountSource:     "wechat",
					ExternalAccountID: "wx_100011",
					UserName:          "Target User",
					IsEnabled:         true,
				})
				bearer := tc.token(t, env)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.Get(t, queryUserProfileByUserIDPath,
					apicall.WithBearerToken(bearer),
					apicall.WithQuery("user_id", tc.queryUserID),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, http.StatusForbidden, "PERMISSION_DENIED", "USER_PROFILE_PERMISSION_DENIED")

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("05_reject_unauthenticated_or_bad_token", func(t *testing.T) {
		cases := []struct {
			name              string
			businessExplain   string
			authHeader        func(t *testing.T, env *harness.Environment) string
			wantWWWAuthSubstr string
		}{
			{
				name:              "anonymous_request",
				businessExplain:   "未提供 Authorization 头时进入 Controller，因缺少登录身份返回 401 UNAUTHENTICATED 与基础 Bearer 挑战头",
				authHeader:        func(*testing.T, *harness.Environment) string { return "" },
				wantWWWAuthSubstr: `Bearer realm="api"`,
			},
			{
				name:            "expired_member_token",
				businessExplain: "携带已过期的 user/member JWT 在认证中间件即被拦截，返回 401 UNAUTHENTICATED 与 invalid_token 挑战头",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueExpiredMemberToken(t, 100020)
				},
				wantWWWAuthSubstr: `error="invalid_token"`,
			},
			{
				name:            "wrong_signing_key_token",
				businessExplain: "携带异构密钥签名的伪造 JWT 在认证中间件验签失败，返回 401 UNAUTHENTICATED 与 invalid_token 挑战头",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueWrongKeyMemberToken(t, 100020)
				},
				wantWWWAuthSubstr: `error="invalid_token"`,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份] 由 tc.authHeader 提供
				headerVal := tc.authHeader(t, env)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.Get(t, queryUserProfileByUserIDPath,
					apicall.WithHeaderIfNonEmpty("Authorization", headerVal),
					apicall.WithQuery("user_id", "100020"),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, http.StatusUnauthorized, "UNAUTHENTICATED", "")
				if gotChallenge := resp.Header.Get("WWW-Authenticate"); !strings.Contains(gotChallenge, tc.wantWWWAuthSubstr) {
					t.Fatalf("WWW-Authenticate = %q, want substring %q", gotChallenge, tc.wantWWWAuthSubstr)
				}

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("06_reject_invalid_user_id_query", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			rawUserID       *string
		}{
			{
				name:            "missing_user_id",
				businessExplain: "省略必填 Query 参数 user_id 时，Validator 返回 400 并定位字段 user_id",
				rawUserID:       nil,
			},
			{
				name:            "zero_user_id",
				businessExplain: "user_id=0 不满足正 64 位整数要求，Validator 返回 400 并定位字段 user_id",
				rawUserID:       new("0"),
			},
			{
				name:            "non_numeric_user_id",
				businessExplain: "user_id 为非十进制数字字符串时，Validator 返回 400 并定位字段 user_id",
				rawUserID:       new("abc"),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				memberToken := env.Auth.IssueMemberToken(t, 100030)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				var opts []apicall.Option
				opts = append(opts, apicall.WithBearerToken(memberToken))
				if tc.rawUserID != nil {
					opts = append(opts, apicall.WithQuery("user_id", *tc.rawUserID))
				}
				resp := env.HTTP.Get(t, queryUserProfileByUserIDPath, opts...)

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				apicall.RequireFieldError(t, errInfo, "user_id")

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})
}
