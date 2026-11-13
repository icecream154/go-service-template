//go:build integration

// 接口集成测试：POST /api/v1/user/userProfile/createUser
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_profile/create_user_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_profile_controller.go -> (*UserProfileController).CreateUser
// 对应服务    ：app/domains/user/service/user_profile_service.go -> (*UserProfileService).CreateUser
//
// 测试场景总览（共 6 个场景）：
//   01_anonymous_registers_default_profile     [正常链路]       匿名提交新外部账户创建启用资料，返回 Cache-Control: no-store、可验签的 Bearer JWT 与初始版本 0
//   02_register_with_explicit_avatar_variants  [正常链路]       显式提供合法头像 URL 或空字符串 "" 时均按契约原样落库并返回（区分 "" 与 null）
//   03_reject_duplicate_external_account       [业务状态冲突]   同一 (account_source, external_account_id) 重复创建返回 409 USER_ACCOUNT_ALREADY_EXISTS 且不覆盖原资料
//   04_concurrent_duplicate_creation_race      [并发与事务]     多协程同时注册同一外部账户时唯一约束保证恰好 1 次成功（200）、其余全部返回 409，数据库仅落 1 行
//   05_reject_invalid_json_or_fields           [参数与协议]     非法 JSON、超长命名空间、空白昵称或非法协议头像在 Validator 层返回 400 INVALID_ARGUMENT，数据库零写入
//   06_reject_invalid_authorization_header     [认证与权限]     虽支持匿名调用，但若携带了损坏或无效的 Authorization 头仍由公共认证入口拦截返回 401/400
package userprofile_test

import (
	"database/sql"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"go-service-template/app/identity"
	"go-service-template/internal/authn"
	"go-service-template/tests/testkit/apicall"
)

const createUserPath = "/api/v1/user/userProfile/createUser"

type createUserData struct {
	UserProfile wireUserProfile `json:"user_profile"`
	AccessToken string          `json:"access_token"`
	TokenType   string          `json:"token_type"`
	ExpiresAt   time.Time       `json:"expires_at"`
}

func TestCreateUser(t *testing.T) {
	t.Run("01_anonymous_registers_default_profile", func(t *testing.T) {
		// [业务解释]
		// 首次外部账户注册无需已有凭证：服务端生成正数雪花 ID、落库初始启用资料（avatar_url=NULL, version=0），
		// 并当场为该内部用户 ID 签发 user/member JWT；由于响应含敏感凭证，必须设置 Cache-Control: no-store。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		// 数据库为空；采用匿名身份发起请求。

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, createUserPath,
			apicall.WithRawJSON(`{
				"account_source": "wechat_mini",
				"external_account_id": "000789",
				"user_name": "Integration User"
			}`),
		)

		// [4. 预期响应核对]
		if gotCache := resp.Header.Get("Cache-Control"); gotCache != "no-store" {
			t.Fatalf("Cache-Control = %q, want %q", gotCache, "no-store")
		}
		data := apicall.RequireSuccess[createUserData](t, resp)
		userID, err := strconv.ParseInt(data.UserProfile.UserID, 10, 64)
		if err != nil || userID <= 0 {
			t.Fatalf("expected positive decimal user_id string, got %q", data.UserProfile.UserID)
		}
		if data.UserProfile.UserName != "Integration User" ||
			string(data.UserProfile.AvatarURL) != "null" ||
			!data.UserProfile.IsEnabled ||
			data.UserProfile.Version == nil || *data.UserProfile.Version != 0 {
			t.Fatalf("unexpected created user_profile: %+v (avatar=%s)", data.UserProfile, data.UserProfile.AvatarURL)
		}
		if data.TokenType != "Bearer" || data.AccessToken == "" || !data.ExpiresAt.After(time.Now().UTC()) {
			t.Fatal("expected non-empty Bearer token and future UTC expires_at")
		}

		// 用测试轮次信任域验签返回的 JWT，确认主体严格等于刚生成的内部雪花 ID 且命名空间为 user/member。
		principal := env.Auth.AuthenticateToken(t, data.AccessToken)
		wantPrincipal := authn.Principal{
			Kind:      authn.PrincipalKindUser,
			Namespace: identity.UserNamespaceMember,
			SubjectID: data.UserProfile.UserID,
		}
		if principal != wantPrincipal {
			t.Fatalf("issued token principal = %+v, want %+v", principal, wantPrincipal)
		}

		// [5. 存储终态与调用副作用核对]
		stored := queryStoredUserProfileByAccount(t, env, "wechat_mini", "000789")
		if stored.ID != userID || stored.UserName != "Integration User" || stored.AvatarURL.Valid || !stored.IsEnabled || stored.Version != 0 {
			t.Fatalf("unexpected stored user_profile row: %+v", stored)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("02_register_with_explicit_avatar_variants", func(t *testing.T) {
		// [业务解释]
		// createUser 的 avatar_url 支持三种语义：省略/null 存为 SQL NULL，空字符串 "" 原样存为空串，合法 HTTP(S) URL 原样保存。
		env := suite.Shared(t)

		// [1. 前置数据与身份] 匿名调用。

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入] 分别注册显式 URL 用户与显式空字符串 "" 用户。
		respWithURL := env.HTTP.PostJSON(t, createUserPath,
			apicall.WithRawJSON(`{
				"account_source": "github",
				"external_account_id": "gh_101",
				"user_name": "URL User",
				"avatar_url": "https://cdn.example.com/gh_101.png"
			}`),
		)
		respWithEmpty := env.HTTP.PostJSON(t, createUserPath,
			apicall.WithRawJSON(`{
				"account_source": "github",
				"external_account_id": "gh_102",
				"user_name": "Empty Avatar User",
				"avatar_url": ""
			}`),
		)

		// [4. 预期响应核对]
		dataURL := apicall.RequireSuccess[createUserData](t, respWithURL)
		if string(dataURL.UserProfile.AvatarURL) != `"https://cdn.example.com/gh_101.png"` {
			t.Fatalf("avatar_url = %s, want quoted URL", dataURL.UserProfile.AvatarURL)
		}
		dataEmpty := apicall.RequireSuccess[createUserData](t, respWithEmpty)
		if string(dataEmpty.UserProfile.AvatarURL) != `""` {
			t.Fatalf("avatar_url = %s, want empty JSON string \"\"", dataEmpty.UserProfile.AvatarURL)
		}

		// [5. 存储终态与调用副作用核对]
		storedURL := queryStoredUserProfileByAccount(t, env, "github", "gh_101")
		if !storedURL.AvatarURL.Valid || storedURL.AvatarURL.String != "https://cdn.example.com/gh_101.png" {
			t.Fatalf("stored gh_101 avatar = %+v, want valid URL", storedURL.AvatarURL)
		}
		storedEmpty := queryStoredUserProfileByAccount(t, env, "github", "gh_102")
		if !storedEmpty.AvatarURL.Valid || storedEmpty.AvatarURL.String != "" {
			t.Fatalf("stored gh_102 avatar = %+v, want non-NULL empty string", storedEmpty.AvatarURL)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("03_reject_duplicate_external_account", func(t *testing.T) {
		// [业务解释]
		// 同一 (account_source, external_account_id) 只允许绑定一个内部用户；重复提交注册必须返回 409 USER_ACCOUNT_ALREADY_EXISTS，
		// 绝不能为已有账户补发 Token，也不能覆盖已有用户的昵称、头像或版本号。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		seedUserProfile(t, env, userProfileSeed{
			ID:                30001,
			AccountSource:     "feishu",
			ExternalAccountID: "fs_dup_01",
			UserName:          "Original Owner",
			AvatarURL:         sql.NullString{String: "https://cdn.example.com/orig.png", Valid: true},
			IsEnabled:         true,
			Version:           2,
		})

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, createUserPath,
			apicall.WithRawJSON(`{
				"account_source": "feishu",
				"external_account_id": "fs_dup_01",
				"user_name": "Attacker Overwrite"
			}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusConflict, "CONFLICT", "USER_ACCOUNT_ALREADY_EXISTS")

		// [5. 存储终态与调用副作用核对]
		if gotCount := countStoredUserProfiles(t, env); gotCount != 1 {
			t.Fatalf("user_profile row count = %d, want 1", gotCount)
		}
		stored := queryStoredUserProfileByID(t, env, 30001)
		if stored.UserName != "Original Owner" || stored.Version != 2 || stored.AvatarURL.String != "https://cdn.example.com/orig.png" {
			t.Fatalf("existing user_profile was unexpectedly modified: %+v", stored)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("04_concurrent_duplicate_creation_race", func(t *testing.T) {
		// [业务解释]
		// 高并发场景下客户端可能同时触发多个相同外部账户的首次注册请求；依赖 MySQL uk_user_profile_account_source_external_account_id
		// 唯一索引与 Service 冲突转换，必须保证恰好 1 个请求返回 200 成功创建，其余全部确定性返回 409 CONFLICT。
		env := suite.Shared(t)

		// [1. 前置数据与身份] 空表，8 个并发匿名请求竞争同一外部账户。
		const workers = 8

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		type result struct {
			resp *apicall.Response
		}
		results := make([]result, workers)
		startGate := make(chan struct{})
		var wg sync.WaitGroup
		for i := range workers {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-startGate
				results[idx] = result{
					resp: env.HTTP.PostJSON(t, createUserPath, apicall.WithRawJSON(`{
						"account_source": "wechat",
						"external_account_id": "race_ext_99",
						"user_name": "Race User"
					}`)),
				}
			}(i)
		}
		close(startGate)
		wg.Wait()

		// [4. 预期响应核对]
		var successCount, conflictCount int
		var winnerID int64
		for _, item := range results {
			switch item.resp.StatusCode {
			case http.StatusOK:
				successCount++
				data := apicall.RequireSuccess[createUserData](t, item.resp)
				parsedID, err := strconv.ParseInt(data.UserProfile.UserID, 10, 64)
				if err != nil || parsedID <= 0 {
					t.Fatalf("invalid winner user_id: %q", data.UserProfile.UserID)
				}
				winnerID = parsedID
			case http.StatusConflict:
				conflictCount++
				apicall.RequireError(t, item.resp, http.StatusConflict, "CONFLICT", "USER_ACCOUNT_ALREADY_EXISTS")
			default:
				t.Fatalf("unexpected status code in concurrent create race: %d", item.resp.StatusCode)
			}
		}
		if successCount != 1 || conflictCount != workers-1 {
			t.Fatalf("concurrent create outcomes: success=%d conflict=%d, want 1 and %d",
				successCount, conflictCount, workers-1)
		}

		// [5. 存储终态与调用副作用核对]
		if gotCount := countStoredUserProfiles(t, env); gotCount != 1 {
			t.Fatalf("user_profile count after concurrent race = %d, want 1", gotCount)
		}
		stored := queryStoredUserProfileByAccount(t, env, "wechat", "race_ext_99")
		if stored.ID != winnerID {
			t.Fatalf("stored winner ID = %d, want HTTP winner ID %d", stored.ID, winnerID)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})

	t.Run("05_reject_invalid_json_or_fields", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			rawBody         string
			wantField       string // 空串表示语法级 JSON 绑定错误（无字段明细）
		}{
			{
				name:            "malformed_json_body",
				businessExplain: "损坏的 JSON 正文在绑定阶段立即返回 400 INVALID_ARGUMENT，不写入任何用户记录",
				rawBody:         `{"account_source":"wx",`,
				wantField:       "",
			},
			{
				name:            "oversized_account_source",
				businessExplain: "account_source 超过 32 个 Unicode 码点上限时被 Validator 拒绝，返回 400 并定位字段 account_source",
				rawBody:         `{"account_source":"namespace_exceeding_32_characters","external_account_id":"1","user_name":"Alice"}`,
				wantField:       "account_source",
			},
			{
				name:            "blank_user_name",
				businessExplain: "纯空白 user_name 被 Validator 拒绝，返回 400 并定位字段 user_name",
				rawBody:         `{"account_source":"wx","external_account_id":"1","user_name":"   "}`,
				wantField:       "user_name",
			},
			{
				name:            "non_http_avatar_url",
				businessExplain: "非空 avatar_url 使用 javascript: 等非 HTTP/HTTPS 协议时被拒绝，定位字段 avatar_url",
				rawBody:         `{"account_source":"wx","external_account_id":"1","user_name":"Alice","avatar_url":"ftp://example.com/a.png"}`,
				wantField:       "avatar_url",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份] 空表，匿名请求。

				// [2. 外部依赖模拟] 预期 0 次外部调用。

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, createUserPath, apicall.WithRawJSON(tc.rawBody))

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				if tc.wantField != "" {
					apicall.RequireFieldError(t, errInfo, tc.wantField)
				}

				// [5. 存储终态与调用副作用核对]
				if gotCount := countStoredUserProfiles(t, env); gotCount != 0 {
					t.Fatalf("failed validation must not insert rows, got count=%d", gotCount)
				}
				env.CompanyStub("python-service-template").RequireNoCalls(t)
				env.SDKMock.RequireNoCalls(t)
			})
		}
	})

	t.Run("06_reject_invalid_authorization_header", func(t *testing.T) {
		// [业务解释]
		// 尽管 createUser 支持无头匿名访问，但全局 AuthenticateIfPresent 中间件规定：
		// 只要请求显式携带了 Authorization 头，就必须通过完整认证校验，伪造凭证返回 401 且不执行注册落库。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		badToken := env.Auth.IssueWrongKeyMemberToken(t, 39999)

		// [2. 外部依赖模拟] 预期 0 次外部调用。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, createUserPath,
			apicall.WithBearerToken(badToken),
			apicall.WithRawJSON(`{
				"account_source": "wechat",
				"external_account_id": "should_not_create",
				"user_name": "Blocked User"
			}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusUnauthorized, "UNAUTHENTICATED", "")

		// [5. 存储终态与调用副作用核对]
		if gotCount := countStoredUserProfiles(t, env); gotCount != 0 {
			t.Fatalf("rejected authentication must not create user, got count=%d", gotCount)
		}
		env.CompanyStub("python-service-template").RequireNoCalls(t)
		env.SDKMock.RequireNoCalls(t)
	})
}
