//go:build integration

// 接口集成测试：POST /api/v1/user/userProfile/batchQueryUserProfiles
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_profile/batch_query_user_profiles_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_profile_controller.go -> (*UserProfileController).BatchQueryUserProfiles
// 对应服务    ：app/domains/user/service/user_profile_service.go -> (*UserProfileService).BatchQueryUserProfiles
//
// 测试场景总览（共 4 个场景）：
//   01_deduplicate_and_preserve_first_seen_order  [正常链路]     service/admin 提交含重复项、停用用户与缺失 ID 的列表，按首次出现顺序分别返回资料与缺失列表
//   02_all_missing_returns_empty_profile_array    [正常链路]     全部目标 ID 均不存在时返回 200，user_profile_list 为非 null 的 []，missing_user_id_list 保留去重顺序
//   03_forbid_member_and_reject_anonymous         [认证与权限]   普通成员 user/member 调用返回 403，匿名调用返回 401
//   04_reject_empty_oversized_or_invalid_id_list  [参数与协议]   空数组、去重前超过 100 个元素或含非正整数 ID 在 Validator 层返回 400 并定位 user_id_list
package userprofile_test

import (
	"net/http"
	"slices"
	"testing"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
)

const batchQueryUserProfilesPath = "/api/v1/user/userProfile/batchQueryUserProfiles"

type batchQueryUserProfilesData struct {
	UserProfileList   []wireUserProfile `json:"user_profile_list"`
	MissingUserIDList []string          `json:"missing_user_id_list"`
}

func TestBatchQueryUserProfiles(t *testing.T) {
	t.Run("01_deduplicate_and_preserve_first_seen_order", func(t *testing.T) {
		// [业务解释]
		// 内部管理服务批量查询用户资料时，输入列表可能包含重复 ID、停用用户以及不存在的用户 ID：
		// Service 对重复 ID 去重，且 user_profile_list 与 missing_user_id_list 均严格按照请求中首次出现的顺序返回。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		seedUserProfile(t, env, userProfileSeed{ID: 60001, AccountSource: "wx", ExternalAccountID: "b1", UserName: "First", IsEnabled: true, Version: 0})
		seedUserProfile(t, env, userProfileSeed{ID: 60002, AccountSource: "wx", ExternalAccountID: "b2", UserName: "SecondDisabled", IsEnabled: false, Version: 5})
		adminToken := env.Auth.IssueServiceAdminToken(t, "order-admin-sync")

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入] 故意乱序并夹带重复项：["60099", "60002", "60099", "60001", "60088", "60002"]
		resp := env.HTTP.PostJSON(t, batchQueryUserProfilesPath,
			apicall.WithBearerToken(adminToken),
			apicall.WithJSONBody(map[string]any{
				"user_id_list": []string{"60099", "60002", "60099", "60001", "60088", "60002"},
			}),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[batchQueryUserProfilesData](t, resp)
		gotFoundIDs := make([]string, 0, len(data.UserProfileList))
		for _, item := range data.UserProfileList {
			gotFoundIDs = append(gotFoundIDs, item.UserID)
		}
		wantFoundIDs := []string{"60002", "60001"}
		if !slices.Equal(gotFoundIDs, wantFoundIDs) {
			t.Fatalf("user_profile_list IDs = %v, want %v", gotFoundIDs, wantFoundIDs)
		}
		if data.UserProfileList[0].IsEnabled {
			t.Fatal("disabled user 60002 must retain is_enabled=false")
		}
		wantMissing := []string{"60099", "60088"}
		if !slices.Equal(data.MissingUserIDList, wantMissing) {
			t.Fatalf("missing_user_id_list = %v, want %v", data.MissingUserIDList, wantMissing)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_all_missing_returns_empty_profile_array", func(t *testing.T) {
		// [业务解释]
		// 批量查询不存在的用户不属于 404 错误：应正常返回 200，其中 user_profile_list 为非 null 空切片 []，全部目标归入 missing_user_id_list。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		adminToken := env.Auth.IssueServiceAdminToken(t, "order-admin-sync")

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, batchQueryUserProfilesPath,
			apicall.WithBearerToken(adminToken),
			apicall.WithRawJSON(`{"user_id_list":["70001","70002","70001"]}`),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[batchQueryUserProfilesData](t, resp)
		if data.UserProfileList == nil || len(data.UserProfileList) != 0 {
			t.Fatalf("expected non-nil empty user_profile_list, got %+v", data.UserProfileList)
		}
		if !slices.Equal(data.MissingUserIDList, []string{"70001", "70002"}) {
			t.Fatalf("missing_user_id_list = %v, want [70001 70002]", data.MissingUserIDList)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_forbid_member_and_reject_anonymous", func(t *testing.T) {
		cases := []struct {
			name             string
			businessExplain  string
			authHeader       func(t *testing.T, env *harness.Environment) string
			wantStatus       int
			wantCode         string
			wantBusinessCode string
		}{
			{
				name:            "anonymous_caller",
				businessExplain: "未携带凭证调用批量查询接口返回 401 UNAUTHENTICATED",
				authHeader:      func(*testing.T, *harness.Environment) string { return "" },
				wantStatus:      http.StatusUnauthorized,
				wantCode:        "UNAUTHENTICATED",
			},
			{
				name:             "member_caller_forbidden",
				businessExplain:  "普通成员 user/member 无权批量读取任意用户资料，返回 403 PERMISSION_DENIED + USER_PROFILE_PERMISSION_DENIED",
				authHeader:       func(t *testing.T, env *harness.Environment) string { return "Bearer " + env.Auth.IssueMemberToken(t, 61001) },
				wantStatus:       http.StatusForbidden,
				wantCode:         "PERMISSION_DENIED",
				wantBusinessCode: "USER_PROFILE_PERMISSION_DENIED",
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
				resp := env.HTTP.PostJSON(t, batchQueryUserProfilesPath,
					apicall.WithHeaderIfNonEmpty("Authorization", header),
					apicall.WithRawJSON(`{"user_id_list":["61001"]}`),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, tc.wantStatus, tc.wantCode, tc.wantBusinessCode)

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("04_reject_empty_oversized_or_invalid_id_list", func(t *testing.T) {
		oversized := make([]string, 101)
		for i := range oversized {
			// 即使全部是同一个重复 ID，数量上限（100）也在去重之前由 Validator 检查。
			oversized[i] = "62001"
		}

		cases := []struct {
			name            string
			businessExplain string
			payload         any
		}{
			{
				name:            "empty_list",
				businessExplain: "user_id_list 为空切片违反至少 1 个 ID 的契约，返回 400 并定位 user_id_list",
				payload:         map[string]any{"user_id_list": []string{}},
			},
			{
				name:            "more_than_100_items_before_dedup",
				businessExplain: "列表长度在去重前计算，提交 101 个重复 ID 同样违反上限 100，返回 400 并定位 user_id_list",
				payload:         map[string]any{"user_id_list": oversized},
			},
			{
				name:            "contains_non_positive_id",
				businessExplain: "列表含 \"-5\" 等非正整数字符串时，返回 400 并定位 user_id_list",
				payload:         map[string]any{"user_id_list": []string{"62001", "-5"}},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				adminToken := env.Auth.IssueServiceAdminToken(t, "ops-console")

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, batchQueryUserProfilesPath,
					apicall.WithBearerToken(adminToken),
					apicall.WithJSONBody(tc.payload),
				)

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				apicall.RequireFieldError(t, errInfo, "user_id_list")

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})
}
