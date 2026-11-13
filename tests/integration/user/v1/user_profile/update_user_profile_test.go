//go:build integration

// 接口集成测试：POST /api/v1/user/userProfile/updateUserProfile
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_profile/update_user_profile_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_profile_controller.go -> (*UserProfileController).UpdateUserProfile
// 对应服务    ：app/domains/user/service/user_profile_service.go -> (*UserProfileService).UpdateUserProfile
//
// 测试场景总览（共 7 个场景）：
//   01_member_replaces_own_profile_and_bumps_version  [正常链路]       本人提交匹配的 expected_version 完整替换昵称与头像，HTTP 与落库 version 同步 +1
//   02_reject_stale_expected_version                  [业务状态冲突]   expected_version 与当前持久化版本不一致时返回 409 USER_PROFILE_VERSION_CONFLICT，原记录不变
//   03_concurrent_updates_with_same_expected_version  [并发与事务]     多协程基于同一 expected_version 并发更新同一用户时，行锁与版本检查保证仅 1 次成功、其余返回 409
//   04_reject_disabled_user_modification              [业务状态冲突]   账号处于停用状态（is_enabled=0）时禁止修改资料，返回 409 USER_DISABLED 且不改库
//   05_target_user_not_found                          [业务状态冲突]   本人凭证合法但目标用户行不存在时返回 404 NOT_FOUND + USER_PROFILE_NOT_FOUND
//   06_forbid_updating_other_user_or_unauthenticated  [认证与权限]     修改他人资料、service/admin 调用或匿名请求分别返回 403 / 401，原资料零改动
//   07_reject_missing_version_or_empty_avatar         [参数与协议]     缺失 expected_version、负版本号或清空头像（""/null）在 Validator 层返回 400 及字段明细
package userprofile_test

import (
	"database/sql"
	"net/http"
	"sync"
	"testing"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
)

const updateUserProfilePath = "/api/v1/user/userProfile/updateUserProfile"

func TestUpdateUserProfile(t *testing.T) {
	t.Run("01_member_replaces_own_profile_and_bumps_version", func(t *testing.T) {
		// [业务解释]
		// 更新资料采用基于 expected_version 的完整替换语义：本人携带当前最新版本号（如初始 0）提交新昵称与非空 HTTP(S) 头像，
		// 事务内锁定记录校验通过后将持久化 version 递增为 1，并在响应中返回更新后的完整资料。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 50001
		seedUserProfile(t, env, userProfileSeed{
			ID:                userID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_50001",
			UserName:          "Before Name",
			AvatarURL:         sql.NullString{Valid: false},
			IsEnabled:         true,
			Version:           0,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, updateUserProfilePath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{
				"user_id": "50001",
				"expected_version": 0,
				"user_name": "After Name",
				"avatar_url": "https://cdn.example.com/avatars/50001-v1.png"
			}`),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[wireUserProfile](t, resp)
		if data.UserID != "50001" ||
			data.UserName != "After Name" ||
			string(data.AvatarURL) != `"https://cdn.example.com/avatars/50001-v1.png"` ||
			!data.IsEnabled ||
			data.Version == nil || *data.Version != 1 {
			t.Fatalf("unexpected updated profile response: %+v (avatar=%s)", data, data.AvatarURL)
		}

		// [5. 存储终态与调用副作用核对]
		stored := queryStoredUserProfileByID(t, env, userID)
		if stored.UserName != "After Name" || !stored.AvatarURL.Valid ||
			stored.AvatarURL.String != "https://cdn.example.com/avatars/50001-v1.png" || stored.Version != 1 {
			t.Fatalf("unexpected stored profile after update: %+v", stored)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_reject_stale_expected_version", func(t *testing.T) {
		// [业务解释]
		// 当客户端持有的 expected_version 落后于数据库当前版本时，必须返回 409 USER_PROFILE_VERSION_CONFLICT 防止静默覆盖他人或前序修改。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 50002
		seedUserProfile(t, env, userProfileSeed{
			ID:                userID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_50002",
			UserName:          "Latest Name",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/latest.png", Valid: true},
			IsEnabled:         true,
			Version:           4,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入] 提交过期的 expected_version=3。
		resp := env.HTTP.PostJSON(t, updateUserProfilePath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{
				"user_id": "50002",
				"expected_version": 3,
				"user_name": "Stale Attempt",
				"avatar_url": "https://cdn.example.com/stale.png"
			}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusConflict, "CONFLICT", "USER_PROFILE_VERSION_CONFLICT")

		// [5. 存储终态与调用副作用核对]
		stored := queryStoredUserProfileByID(t, env, userID)
		if stored.UserName != "Latest Name" || stored.Version != 4 || stored.AvatarURL.String != "https://cdn.example.com/latest.png" {
			t.Fatalf("stale update must not alter stored row, got %+v", stored)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_concurrent_updates_with_same_expected_version", func(t *testing.T) {
		// [业务解释]
		// 多个终端同时基于同一初始版本（version=0）提交更新请求时，SELECT ... FOR UPDATE 行级排他锁与版本对比共同保证
		// 只有 1 个并发请求成功将版本推进到 1，其余全部返回 409 USER_PROFILE_VERSION_CONFLICT，不会出现丢失更新或版本跳变。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 50003
		seedUserProfile(t, env, userProfileSeed{
			ID:                userID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_50003",
			UserName:          "Initial",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/init.png", Valid: true},
			IsEnabled:         true,
			Version:           0,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入] 6 个并发协程均携带 expected_version=0 提交各自的新昵称。
		const workers = 6
		responses := make([]*apicall.Response, workers)
		startGate := make(chan struct{})
		var wg sync.WaitGroup
		for i := range workers {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-startGate
				responses[idx] = env.HTTP.PostJSON(t, updateUserProfilePath,
					apicall.WithBearerToken(memberToken),
					apicall.WithJSONBody(map[string]any{
						"user_id":          "50003",
						"expected_version": 0,
						"user_name":        "Winner-" + string(rune('A'+idx)),
						"avatar_url":       "https://cdn.example.com/winner.png",
					}),
				)
			}(i)
		}
		close(startGate)
		wg.Wait()

		// [4. 预期响应核对]
		var okCount, conflictCount int
		var winningName string
		for _, r := range responses {
			switch r.StatusCode {
			case http.StatusOK:
				okCount++
				updated := apicall.RequireSuccess[wireUserProfile](t, r)
				if updated.Version == nil || *updated.Version != 1 {
					t.Fatalf("winner response version = %v, want 1", updated.Version)
				}
				winningName = updated.UserName
			case http.StatusConflict:
				conflictCount++
				apicall.RequireError(t, r, http.StatusConflict, "CONFLICT", "USER_PROFILE_VERSION_CONFLICT")
			default:
				t.Fatalf("unexpected status in concurrent update: %d", r.StatusCode)
			}
		}
		if okCount != 1 || conflictCount != workers-1 {
			t.Fatalf("concurrent update outcomes: ok=%d conflict=%d, want 1 and %d", okCount, conflictCount, workers-1)
		}

		// [5. 存储终态与调用副作用核对]
		stored := queryStoredUserProfileByID(t, env, userID)
		if stored.Version != 1 || stored.UserName != winningName {
			t.Fatalf("stored row after race = {name:%q version:%d}, want {name:%q version:1}",
				stored.UserName, stored.Version, winningName)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("04_reject_disabled_user_modification", func(t *testing.T) {
		// [业务解释]
		// 已停用用户（is_enabled=0）虽可读取本人资料，但执行更新时必须被 Service 拦截并返回 409 USER_DISABLED，版本号与原资料保持不变。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 50004
		seedUserProfile(t, env, userProfileSeed{
			ID:                userID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_50004",
			UserName:          "Frozen Name",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/frozen.png", Valid: true},
			IsEnabled:         false,
			Version:           2,
		})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, updateUserProfilePath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{
				"user_id": "50004",
				"expected_version": 2,
				"user_name": "Try Change",
				"avatar_url": "https://cdn.example.com/new.png"
			}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusConflict, "CONFLICT", "USER_DISABLED")

		// [5. 存储终态与调用副作用核对]
		stored := queryStoredUserProfileByID(t, env, userID)
		if stored.UserName != "Frozen Name" || stored.Version != 2 || stored.IsEnabled {
			t.Fatalf("disabled user profile must remain untouched, got %+v", stored)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("05_target_user_not_found", func(t *testing.T) {
		// [业务解释]
		// 携带合法本人 Token 但数据库中不存在对应用户行时，返回 404 NOT_FOUND + USER_PROFILE_NOT_FOUND。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		memberToken := env.Auth.IssueMemberToken(t, 50999)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, updateUserProfilePath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{
				"user_id": "50999",
				"expected_version": 0,
				"user_name": "Ghost",
				"avatar_url": "https://cdn.example.com/ghost.png"
			}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusNotFound, "NOT_FOUND", "USER_PROFILE_NOT_FOUND")

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("06_forbid_updating_other_user_or_unauthenticated", func(t *testing.T) {
		cases := []struct {
			name             string
			businessExplain  string
			authHeader       func(t *testing.T, env *harness.Environment) string
			wantStatus       int
			wantCode         string
			wantBusinessCode string
		}{
			{
				name:             "member_updates_another_user",
				businessExplain:  "成员 50010 尝试修改成员 50011 的资料，Controller 按本人操作约束直接拦截返回 403 USER_PROFILE_PERMISSION_DENIED",
				authHeader:       func(t *testing.T, env *harness.Environment) string { return "Bearer " + env.Auth.IssueMemberToken(t, 50010) },
				wantStatus:       http.StatusForbidden,
				wantCode:         "PERMISSION_DENIED",
				wantBusinessCode: "USER_PROFILE_PERMISSION_DENIED",
			},
			{
				name:            "service_admin_forbidden",
				businessExplain: "service/admin 服务身份不允许代替用户修改个人资料，返回 403 USER_PROFILE_PERMISSION_DENIED",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueServiceAdminToken(t, "ops-admin")
				},
				wantStatus:       http.StatusForbidden,
				wantCode:         "PERMISSION_DENIED",
				wantBusinessCode: "USER_PROFILE_PERMISSION_DENIED",
			},
			{
				name:            "anonymous_caller",
				businessExplain: "未登录匿名请求返回 401 UNAUTHENTICATED",
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
				seedUserProfile(t, env, userProfileSeed{
					ID:                50011,
					AccountSource:     "wx",
					ExternalAccountID: "wx_50011",
					UserName:          "Protected Name",
					AvatarURL:         sql.NullString{String: "https://cdn.example.com/protected.png", Valid: true},
					IsEnabled:         true,
					Version:           0,
				})
				header := tc.authHeader(t, env)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, updateUserProfilePath,
					apicall.WithHeaderIfNonEmpty("Authorization", header),
					apicall.WithRawJSON(`{
						"user_id": "50011",
						"expected_version": 0,
						"user_name": "Tampered",
						"avatar_url": "https://cdn.example.com/tampered.png"
					}`),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, tc.wantStatus, tc.wantCode, tc.wantBusinessCode)

				// [5. 存储终态与调用副作用核对]
				stored := queryStoredUserProfileByID(t, env, 50011)
				if stored.UserName != "Protected Name" || stored.Version != 0 {
					t.Fatalf("unauthorized update mutated target profile: %+v", stored)
				}
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("07_reject_missing_version_or_empty_avatar", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			rawBody         string
			wantField       string
		}{
			{
				name:            "missing_expected_version",
				businessExplain: "省略 expected_version（不能默认当作 0）时返回 400 并定位字段 expected_version",
				rawBody:         `{"user_id":"50020","user_name":"Alice","avatar_url":"https://cdn.example.com/a.png"}`,
				wantField:       "expected_version",
			},
			{
				name:            "negative_expected_version",
				businessExplain: "expected_version 为负数时违反非负版本范围，返回 400 并定位字段 expected_version",
				rawBody:         `{"user_id":"50020","expected_version":-1,"user_name":"Alice","avatar_url":"https://cdn.example.com/a.png"}`,
				wantField:       "expected_version",
			},
			{
				name:            "empty_avatar_url_not_allowed",
				businessExplain: "updateUserProfile 不支持把头像清空为空串或 null，必须提供合法 HTTP(S) 地址，返回 400 并定位字段 avatar_url",
				rawBody:         `{"user_id":"50020","expected_version":0,"user_name":"Alice","avatar_url":""}`,
				wantField:       "avatar_url",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				memberToken := env.Auth.IssueMemberToken(t, 50020)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, updateUserProfilePath,
					apicall.WithBearerToken(memberToken),
					apicall.WithRawJSON(tc.rawBody),
				)

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				apicall.RequireFieldError(t, errInfo, tc.wantField)

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})
}
