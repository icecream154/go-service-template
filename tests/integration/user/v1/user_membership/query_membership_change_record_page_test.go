//go:build integration

// 接口集成测试：GET /api/v1/user/userMembership/queryMembershipChangeRecordPage
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_membership/query_membership_change_record_page_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_membership_controller.go -> (*UserMembershipController).QueryMembershipChangeRecordPage
// 对应服务    ：app/domains/user/service/user_membership_service.go -> (*UserMembershipService).QueryMembershipChangeRecordPage
//
// 测试场景总览（共 6 个场景）：
//   01_cursor_pagination_with_same_ms_tie_breaker  [正常链路]       按 created_at DESC, id DESC 游标翻页（含同毫秒按 id DESC 决胜、首次开通 null 快照与续费非空快照）
//   02_empty_history_returns_empty_array           [正常链路]       用户存在但无任何变更历史时返回 []、has_more=false 且 next_page_token 为 null
//   03_user_profile_not_found                      [业务状态冲突]   目标用户主档不存在时返回 404 NOT_FOUND + USER_PROFILE_NOT_FOUND
//   04_reject_cross_user_or_malformed_page_token   [参数与协议]     翻页令牌被篡改、损坏或绑定到其他 user_id 时返回 400 INVALID_ARGUMENT 并定位 page_token
//   05_reject_invalid_page_size                    [参数与协议]     显式传入 page_size=0 或 101 时返回 400 INVALID_ARGUMENT 并定位 page_size
//   06_forbid_cross_user_or_reject_anonymous       [认证与权限]     跨用户查询、service/admin 身份或匿名访问分别返回 403 USER_MEMBERSHIP_PERMISSION_DENIED / 401
package usermembership_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"go-service-template/app/domains/user/models"
	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
)

const queryMembershipChangeRecordPagePath = "/api/v1/user/userMembership/queryMembershipChangeRecordPage"

type wireMembershipChangeRecord struct {
	RecordID              string          `json:"record_id"`
	MembershipID          string          `json:"membership_id"`
	OrderID               string          `json:"order_id"`
	ChangeType            int32           `json:"change_type"`
	BeforeMembershipLevel json.RawMessage `json:"before_membership_level"`
	BeforeEffectiveAt     json.RawMessage `json:"before_effective_at"`
	BeforeExpiredAt       json.RawMessage `json:"before_expired_at"`
	AfterMembershipLevel  int32           `json:"after_membership_level"`
	AfterEffectiveAt      time.Time       `json:"after_effective_at"`
	AfterExpiredAt        time.Time       `json:"after_expired_at"`
	CreatedAt             time.Time       `json:"created_at"`
}

type queryMembershipChangeRecordPageData struct {
	MembershipChangeRecordList []wireMembershipChangeRecord `json:"membership_change_record_list"`
	PageSize                   int                          `json:"page_size"`
	HasMore                    bool                         `json:"has_more"`
	NextPageToken              *string                      `json:"next_page_token"`
}

func TestQueryMembershipChangeRecordPage(t *testing.T) {
	t.Run("01_cursor_pagination_with_same_ms_tie_breaker", func(t *testing.T) {
		// [业务解释]
		// 会员权益变更历史采用基于 (created_at DESC, id DESC) 的不透明游标分页（多探测 1 行判断 has_more）：
		// - 当两条续费记录落在同一个 UTC 毫秒时，必须以 id DESC 稳定决胜且跨页不丢不重；
		// - 首次开通（change_type=10）的 before_* 三个字段必须显式输出 JSON null；续费（change_type=20）则携带变更前快照；
		// - 最后一页的 has_more 为 false 且 next_page_token 为 null。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 90001
		const otherUserID int64 = 90002
		const membershipID int64 = 88001

		t0 := time.Date(2026, 3, 1, 8, 0, 0, 123_000_000, time.UTC)
		tSameMS := time.Date(2026, 4, 1, 9, 30, 0, 456_000_000, time.UTC)
		exp1 := t0.Add(30 * 24 * time.Hour)
		exp2 := exp1.Add(30 * 24 * time.Hour)
		exp3 := exp2.Add(30 * 24 * time.Hour)

		seedMembershipUser(t, env, membershipUserSeed{ID: userID, AccountSource: "wx", ExternalAccountID: "h_90001", UserName: "HistoryOwner", IsEnabled: true})
		seedMembershipUser(t, env, membershipUserSeed{ID: otherUserID, AccountSource: "wx", ExternalAccountID: "h_90002", UserName: "OtherUser", IsEnabled: true})

		// 本人 3 条记录：record 9101（最早，首次开通）、record 9102 与 9103（同一毫秒 tSameMS，续费，预期排序 9103 -> 9102 -> 9101）。
		seedMembershipChangeRecord(t, env, membershipChangeRecordSeed{
			ID: 9101, UserID: userID, MembershipID: membershipID, OrderID: 5001, ChangeType: 10,
			AfterMembershipLevel: 10, AfterEffectiveAt: t0, AfterExpiredAt: exp1, CreatedAt: t0,
		})
		seedMembershipChangeRecord(t, env, membershipChangeRecordSeed{
			ID: 9102, UserID: userID, MembershipID: membershipID, OrderID: 5002, ChangeType: 20,
			BeforeMembershipLevel: sql.NullInt32{Int32: 10, Valid: true},
			BeforeEffectiveAt:     sql.NullTime{Time: t0, Valid: true},
			BeforeExpiredAt:       sql.NullTime{Time: exp1, Valid: true},
			AfterMembershipLevel:  10, AfterEffectiveAt: t0, AfterExpiredAt: exp2, CreatedAt: tSameMS,
		})
		seedMembershipChangeRecord(t, env, membershipChangeRecordSeed{
			ID: 9103, UserID: userID, MembershipID: membershipID, OrderID: 5003, ChangeType: 20,
			BeforeMembershipLevel: sql.NullInt32{Int32: 10, Valid: true},
			BeforeEffectiveAt:     sql.NullTime{Time: t0, Valid: true},
			BeforeExpiredAt:       sql.NullTime{Time: exp2, Valid: true},
			AfterMembershipLevel:  10, AfterEffectiveAt: t0, AfterExpiredAt: exp3, CreatedAt: tSameMS,
		})
		// 干扰数据：其他用户的更晚记录，绝不能混入本人的分页结果。
		seedMembershipChangeRecord(t, env, membershipChangeRecordSeed{
			ID: 9999, UserID: otherUserID, MembershipID: 88002, OrderID: 5999, ChangeType: 10,
			AfterMembershipLevel: 10, AfterEffectiveAt: tSameMS, AfterExpiredAt: exp3, CreatedAt: tSameMS.Add(time.Hour),
		})

		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入] 以 page_size=2 连续翻两页（注意第 1 页与第 2 页正好切开同一毫秒的 9102 和后续记录，验证游标复合边界）。
		// 更进一步：用 page_size=1 逐条翻完 3 页，彻底检验同毫秒 (9103 -> 9102) 切页精度！
		var collectedIDs []string
		var collectedRecords []wireMembershipChangeRecord
		pageToken := ""
		for pageIdx := range 3 {
			opts := []apicall.Option{
				apicall.WithBearerToken(memberToken),
				apicall.WithQuery("user_id", "90001"),
				apicall.WithQuery("page_size", "1"),
			}
			if pageToken != "" {
				opts = append(opts, apicall.WithQuery("page_token", pageToken))
			}
			resp := env.HTTP.Get(t, queryMembershipChangeRecordPagePath, opts...)
			page := apicall.RequireSuccess[queryMembershipChangeRecordPageData](t, resp)

			if page.PageSize != 1 || len(page.MembershipChangeRecordList) != 1 {
				t.Fatalf("page %d unexpected size/items: %+v", pageIdx+1, page)
			}
			collectedIDs = append(collectedIDs, page.MembershipChangeRecordList[0].RecordID)
			collectedRecords = append(collectedRecords, page.MembershipChangeRecordList[0])

			if pageIdx < 2 {
				if !page.HasMore || page.NextPageToken == nil || *page.NextPageToken == "" {
					t.Fatalf("page %d expected has_more=true and non-empty next_page_token, got %+v", pageIdx+1, page)
				}
				pageToken = *page.NextPageToken
			} else {
				if page.HasMore || page.NextPageToken != nil {
					t.Fatalf("final page expected has_more=false and nil next_page_token, got %+v", page)
				}
			}
		}

		// [4. 预期响应核对]
		wantOrder := []string{"9103", "9102", "9101"}
		if !slices.Equal(collectedIDs, wantOrder) {
			t.Fatalf("cursor paginated record IDs = %v, want %v", collectedIDs, wantOrder)
		}
		// 续费记录（9103）的 before_* 必须非 null。
		if string(collectedRecords[0].BeforeMembershipLevel) != "10" || string(collectedRecords[0].BeforeExpiredAt) == "null" {
			t.Fatalf("renewal record 9103 must carry non-null before_* snapshot: %+v", collectedRecords[0])
		}
		// 首次开通记录（9101）的 before_* 三个字段必须全部为显式 JSON null。
		firstOpen := collectedRecords[2]
		if firstOpen.ChangeType != 10 ||
			string(firstOpen.BeforeMembershipLevel) != "null" ||
			string(firstOpen.BeforeEffectiveAt) != "null" ||
			string(firstOpen.BeforeExpiredAt) != "null" {
			t.Fatalf("first-open record 9101 must keep null before_* fields, got %+v", firstOpen)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_empty_history_returns_empty_array", func(t *testing.T) {
		// [业务解释]
		// 用户存在但尚无任何订单履约记录时，返回 200，列表为非 null 的 []，默认 page_size=20，has_more=false，next_page_token=null。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const userID int64 = 90010
		seedMembershipUser(t, env, membershipUserSeed{ID: userID, AccountSource: "wx", ExternalAccountID: "h_90010", UserName: "NoHistory", IsEnabled: true})
		memberToken := env.Auth.IssueMemberToken(t, userID)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryMembershipChangeRecordPagePath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "90010"),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[queryMembershipChangeRecordPageData](t, resp)
		if data.MembershipChangeRecordList == nil || len(data.MembershipChangeRecordList) != 0 ||
			data.PageSize != 20 || data.HasMore || data.NextPageToken != nil {
			t.Fatalf("unexpected empty history response: %+v", data)
		}

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_user_profile_not_found", func(t *testing.T) {
		// [业务解释]
		// 目标用户资料不存在时返回 404 NOT_FOUND + USER_PROFILE_NOT_FOUND，不返回空历史列表。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		memberToken := env.Auth.IssueMemberToken(t, 90999)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.Get(t, queryMembershipChangeRecordPagePath,
			apicall.WithBearerToken(memberToken),
			apicall.WithQuery("user_id", "90999"),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusNotFound, "NOT_FOUND", "USER_PROFILE_NOT_FOUND")

		// [5. 存储终态与调用副作用核对]
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("04_reject_cross_user_or_malformed_page_token", func(t *testing.T) {
		foreignToken, err := models.EncodeMembershipChangeRecordPageToken(models.MembershipChangeRecordCursor{
			UserID:    90022, // 与请求目标 90021 不一致
			CreatedAt: time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
			ID:        9500,
		})
		if err != nil {
			t.Fatalf("encode foreign page token: %v", err)
		}

		cases := []struct {
			name            string
			businessExplain string
			pageToken       string
		}{
			{
				name:            "malformed_base64_token",
				businessExplain: "损坏或不满足 25 字节二进制游标格式的 page_token 在 Validator 层返回 400 并定位字段 page_token",
				pageToken:       "not-a-valid-cursor-token",
			},
			{
				name:            "token_bound_to_different_user",
				businessExplain: "游标内部编码的 UserID（90022）与当前查询 user_id（90021）不匹配时拒绝串用，返回 400 并定位字段 page_token",
				pageToken:       foreignToken,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				memberToken := env.Auth.IssueMemberToken(t, 90021)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.Get(t, queryMembershipChangeRecordPagePath,
					apicall.WithBearerToken(memberToken),
					apicall.WithQuery("user_id", "90021"),
					apicall.WithQuery("page_token", tc.pageToken),
				)

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				apicall.RequireFieldError(t, errInfo, "page_token")

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("05_reject_invalid_page_size", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			pageSize        string
		}{
			{
				name:            "explicit_zero_page_size",
				businessExplain: "显式传入 page_size=0 不会回退为默认值 20，而是违反 [1, 100] 区间返回 400 并定位 page_size",
				pageSize:        "0",
			},
			{
				name:            "oversized_page_size",
				businessExplain: "page_size=101 超出单页上限 100，返回 400 并定位 page_size",
				pageSize:        "101",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				memberToken := env.Auth.IssueMemberToken(t, 90030)

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.Get(t, queryMembershipChangeRecordPagePath,
					apicall.WithBearerToken(memberToken),
					apicall.WithQuery("user_id", "90030"),
					apicall.WithQuery("page_size", tc.pageSize),
				)

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				apicall.RequireFieldError(t, errInfo, "page_size")

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
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
				name:             "member_queries_other_user_history",
				businessExplain:  "成员 90040 尝试读取成员 90041 的权益变更历史，返回 403 USER_MEMBERSHIP_PERMISSION_DENIED",
				authHeader:       func(t *testing.T, env *harness.Environment) string { return "Bearer " + env.Auth.IssueMemberToken(t, 90040) },
				wantStatus:       http.StatusForbidden,
				wantCode:         "PERMISSION_DENIED",
				wantBusinessCode: "USER_MEMBERSHIP_PERMISSION_DENIED",
			},
			{
				name:            "service_admin_not_permitted",
				businessExplain: "service/admin 服务身份调用本人历史接口返回 403 USER_MEMBERSHIP_PERMISSION_DENIED",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueServiceAdminToken(t, "audit-worker")
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
				resp := env.HTTP.Get(t, queryMembershipChangeRecordPagePath,
					apicall.WithHeaderIfNonEmpty("Authorization", header),
					apicall.WithQuery("user_id", "90041"),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, tc.wantStatus, tc.wantCode, tc.wantBusinessCode)

				// [5. 存储终态与调用副作用核对]
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})
}
