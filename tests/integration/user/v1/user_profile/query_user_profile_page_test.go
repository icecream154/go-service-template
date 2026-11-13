//go:build integration

// 接口集成测试：GET /api/v1/user/userProfile/queryUserProfilePage
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_profile/query_user_profile_page_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_profile_controller.go -> (*UserProfileController).QueryUserProfilePage
// 对应服务    ：app/domains/user/service/user_profile_service.go -> (*UserProfileService).QueryUserProfilePage
//
// 测试场景总览（共 5 个场景）：
//   01_service_admin_paginates_descending_by_id  [正常链路]     service/admin 分页读取全部资料，严格按 id DESC 排序并返回准确的 total_count
//   02_filter_by_exact_name_and_enabled_status   [正常链路]     支持按 user_name 精确匹配（区分大小写、不做模糊匹配）与 is_enabled 联合过滤
//   03_out_of_range_page_returns_empty_array     [正常链路]     超出总页数的页码返回非 null 空数组 []，同时保留真实的匹配总条数 total_count
//   04_forbid_member_and_reject_anonymous        [认证与权限]   普通成员 user/member 越权调用返回 403，匿名调用返回 401
//   05_reject_invalid_pagination_or_bool_filter  [参数与协议]   非法页码（0）、超限页大小（101）或非布尔字符串（is_enabled=1）返回 400 及字段明细
package userprofile_test

import (
	"database/sql"
	"net/http"
	"testing"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
)

const queryUserProfilePagePath = "/api/v1/user/userProfile/queryUserProfilePage"

type queryUserProfilePageData struct {
	UserProfileList []wireUserProfile `json:"user_profile_list"`
	PageNumber      int               `json:"page_number"`
	PageSize        int               `json:"page_size"`
	TotalCount      int64             `json:"total_count"`
}

func TestQueryUserProfilePage(t *testing.T) {
	t.Run("01_service_admin_paginates_descending_by_id", func(t *testing.T) {
		// [业务解释]
		// 管理后台服务（service/admin）通过页码分页检索全量用户资料时，结果按主键 id 严格降序排列，
		// 跨页顺序稳定且每页均返回完整符合条件的总记录数 total_count。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		seedUserProfile(t, env, userProfileSeed{ID: 20001, AccountSource: "wx", ExternalAccountID: "u1", UserName: "User-1", IsEnabled: true, Version: 0})
		seedUserProfile(t, env, userProfileSeed{ID: 20003, AccountSource: "wx", ExternalAccountID: "u3", UserName: "User-3", IsEnabled: false, Version: 2})
		seedUserProfile(t, env, userProfileSeed{ID: 20002, AccountSource: "wx", ExternalAccountID: "u2", UserName: "User-2", AvatarURL: sql.NullString{String: "https://cdn.example.com/2.png", Valid: true}, IsEnabled: true, Version: 1})
		adminToken := env.Auth.IssueServiceAdminToken(t, "ops-console")

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入] 第 1 页，page_size=2；随后请求第 2 页，page_size=2。
		page1Resp := env.HTTP.Get(t, queryUserProfilePagePath,
			apicall.WithBearerToken(adminToken),
			apicall.WithQuery("page_number", "1"),
			apicall.WithQuery("page_size", "2"),
		)
		page2Resp := env.HTTP.Get(t, queryUserProfilePagePath,
			apicall.WithBearerToken(adminToken),
			apicall.WithQuery("page_number", "2"),
			apicall.WithQuery("page_size", "2"),
		)

		// [4. 预期响应核对]
		page1 := apicall.RequireSuccess[queryUserProfilePageData](t, page1Resp)
		if page1.TotalCount != 3 || page1.PageNumber != 1 || page1.PageSize != 2 || len(page1.UserProfileList) != 2 {
			t.Fatalf("unexpected page1 metadata: %+v", page1)
		}
		if page1.UserProfileList[0].UserID != "20003" || page1.UserProfileList[1].UserID != "20002" {
			t.Fatalf("page1 order = [%s, %s], want [20003, 20002]",
				page1.UserProfileList[0].UserID, page1.UserProfileList[1].UserID)
		}

		page2 := apicall.RequireSuccess[queryUserProfilePageData](t, page2Resp)
		if page2.TotalCount != 3 || page2.PageNumber != 2 || page2.PageSize != 2 || len(page2.UserProfileList) != 1 {
			t.Fatalf("unexpected page2 metadata: %+v", page2)
		}
		if page2.UserProfileList[0].UserID != "20001" {
			t.Fatalf("page2 item user_id = %q, want 20001", page2.UserProfileList[0].UserID)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_filter_by_exact_name_and_enabled_status", func(t *testing.T) {
		// [业务解释]
		// user_name 按 utf8mb4_0900_bin 精确等值过滤（既不匹配子串也不忽略大小写），并可与 is_enabled 组合筛选；
		// 省略 page_number 与 page_size 时默认采用第 1 页、每页 20 条。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		seedUserProfile(t, env, userProfileSeed{ID: 21001, AccountSource: "wx", ExternalAccountID: "a1", UserName: "Bob", IsEnabled: true})
		seedUserProfile(t, env, userProfileSeed{ID: 21002, AccountSource: "wx", ExternalAccountID: "a2", UserName: "Bob", IsEnabled: false})
		seedUserProfile(t, env, userProfileSeed{ID: 21003, AccountSource: "wx", ExternalAccountID: "a3", UserName: "bob", IsEnabled: true})
		seedUserProfile(t, env, userProfileSeed{ID: 21004, AccountSource: "wx", ExternalAccountID: "a4", UserName: "Bobby", IsEnabled: true})
		adminToken := env.Auth.IssueServiceAdminToken(t, "ops-console")

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserProfilePagePath,
			apicall.WithBearerToken(adminToken),
			apicall.WithQuery("user_name", "Bob"),
			apicall.WithQuery("is_enabled", "false"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[queryUserProfilePageData](t, resp)
		if data.PageNumber != 1 || data.PageSize != 20 || data.TotalCount != 1 || len(data.UserProfileList) != 1 {
			t.Fatalf("unexpected filtered page response: %+v", data)
		}
		if data.UserProfileList[0].UserID != "21002" || data.UserProfileList[0].IsEnabled {
			t.Fatalf("unexpected matched user: %+v", data.UserProfileList[0])
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_out_of_range_page_returns_empty_array", func(t *testing.T) {
		// [业务解释]
		// 请求页码超出已有数据时不属于错误：user_profile_list 必须编码为空 JSON 数组 []（绝不能是 null），且 total_count 仍反映真实总行数。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		seedUserProfile(t, env, userProfileSeed{ID: 22001, AccountSource: "wx", ExternalAccountID: "x1", UserName: "OnlyOne", IsEnabled: true})
		adminToken := env.Auth.IssueServiceAdminToken(t, "ops-console")

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryUserProfilePagePath,
			apicall.WithBearerToken(adminToken),
			apicall.WithQuery("page_number", "5"),
			apicall.WithQuery("page_size", "10"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[queryUserProfilePageData](t, resp)
		if data.UserProfileList == nil || len(data.UserProfileList) != 0 || data.TotalCount != 1 {
			t.Fatalf("expected non-nil empty list with total_count=1, got %+v", data)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("04_forbid_member_and_reject_anonymous", func(t *testing.T) {
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
				businessExplain: "未携带身份凭证调用后台分页接口返回 401 UNAUTHENTICATED",
				authHeader:      func(*testing.T, *harness.Environment) string { return "" },
				wantStatus:      http.StatusUnauthorized,
				wantCode:        "UNAUTHENTICATED",
			},
			{
				name:             "member_caller_forbidden",
				businessExplain:  "普通成员 user/member 凭证不允许枚举全站用户列表，返回 403 PERMISSION_DENIED + USER_PROFILE_PERMISSION_DENIED",
				authHeader:       func(t *testing.T, env *harness.Environment) string { return "Bearer " + env.Auth.IssueMemberToken(t, 23001) },
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
				resp := env.HTTP.Get(t, queryUserProfilePagePath,
					apicall.WithHeaderIfNonEmpty("Authorization", header),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, tc.wantStatus, tc.wantCode, tc.wantBusinessCode)

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("05_reject_invalid_pagination_or_bool_filter", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			queryKey        string
			queryVal        string
			wantField       string
		}{
			{
				name:            "zero_page_number",
				businessExplain: "显式传入 page_number=0 违反最小页码 1 的要求，返回 400 并定位字段 page_number",
				queryKey:        "page_number",
				queryVal:        "0",
				wantField:       "page_number",
			},
			{
				name:            "oversized_page_size",
				businessExplain: "page_size=101 超出单页上限 100，返回 400 并定位字段 page_size",
				queryKey:        "page_size",
				queryVal:        "101",
				wantField:       "page_size",
			},
			{
				name:            "non_boolean_is_enabled",
				businessExplain: "is_enabled 仅接受字面量 true 或 false，传入 1 时返回 400 并定位字段 is_enabled",
				queryKey:        "is_enabled",
				queryVal:        "1",
				wantField:       "is_enabled",
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
				resp := env.HTTP.Get(t, queryUserProfilePagePath,
					apicall.WithBearerToken(adminToken),
					apicall.WithQuery(tc.queryKey, tc.queryVal),
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
