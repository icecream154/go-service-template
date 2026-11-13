//go:build integration

// 接口集成测试：POST /api/v1/user/userProfile/generateUserAvatar
// 对应路由    ：app/domains/user/routers/router.go
// 对应校验器  ：app/domains/user/validator/v1/user_profile/generate_user_avatar_validator.go
// 对应控制器  ：app/domains/user/controller/v1/user_profile_controller.go -> (*UserProfileController).GenerateUserAvatar
// 对应服务    ：app/domains/user/service/user_profile_service.go -> (*UserProfileService).GenerateUserAvatar
//
// 测试场景总览（共 6 个场景）：
//   01_member_generates_avatar_success      [正常链路]     已认证成员提交描述，经 Qwen(qwen3.8-flash) 优化后交由图片服务生成并返回 URL，不修改用户资料表
//   02_reject_blank_or_invalid_prompt       [参数与协议]   空白提示词或非法 JSON 在 Validator 层返回 400 INVALID_ARGUMENT，不触发任何外部调用
//   03_reject_unauthenticated_and_service   [认证与权限]   匿名、过期凭证或 service/admin 越权调用被拦截（401 / 403），不触发外部依赖
//   04_propagate_qwen_sdk_failure           [外部依赖]     Qwen SDK 返回调用失败时接口返回 500 INTERNAL_ERROR，且阻断后续图片生成服务调用
//   05_abort_when_qwen_returns_empty_text   [外部依赖]     Qwen SDK 返回纯空白文本时接口返回 500 INTERNAL_ERROR，阻断公司图片生成服务调用
//   06_propagate_company_image_gen_failure  [外部依赖]     Qwen 优化成功但公司图片服务返回 502 或损坏 JSON 时，接口返回 500 且不污染用户资料表
package userprofile_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"go-service-template/tests/harness"
	"go-service-template/tests/testkit/apicall"
	"go-service-template/tests/testkit/httpstub"
	"go-service-template/tests/testkit/sdkmock"
)

const generateUserAvatarPath = "/api/v1/user/userProfile/generateUserAvatar"

type generateUserAvatarData struct {
	AvatarURL string `json:"avatar_url"`
}

func TestGenerateUserAvatar(t *testing.T) {
	t.Run("01_member_generates_avatar_success", func(t *testing.T) {
		// [业务解释]
		// 生成头像接口仅供已认证的 user/member 预览 AI 生成头像：
		// 1. Service 将固定指令与用户原始描述拼装后调用第三方 Qwen SDK（固定模型 qwen3.8-flash）；
		// 2. 将 Qwen 返回的优化英文提示词转发至公司内部 python-service-template 的图片生成接口并透传同一 TraceId；
		// 3. 仅返回生成的图片 URL，不直接写回 user_service.user_profile（头像保存由后续 updateUserProfile 完成）。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		const operatorID int64 = 40001
		seedUserProfile(t, env, userProfileSeed{
			ID:                operatorID,
			AccountSource:     "wechat",
			ExternalAccountID: "wx_40001",
			UserName:          "AvatarCreator",
			AvatarURL:         sql.NullString{Valid: false},
			IsEnabled:         true,
			Version:           0,
		})
		memberToken := env.Auth.IssueMemberToken(t, operatorID)

		// [2. 外部依赖模拟]
		// - 第三方 SDK：注册 ai.llm.qwen 的 GenerateText 返回受控优化提示词；MiniMax 未注册（误调自动判败）。
		const optimizedPrompt = "A warm flat-style portrait of an orange tabby cat wearing glasses, centered square avatar"
		qwenRule := env.SDKMock.Expect("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{
			"content": optimizedPrompt,
		}))
		// - 公司内部 HTTP 服务：注册 python-service-template 的图片生成路由返回头像 URL；其余路由未注册。
		const expectedAvatarURL = "https://cdn.example.com/generated/cat-glasses.png"
		imageGenRoute := env.CompanyStub("python-service-template").Expect(
			http.MethodPost, "/api/v1/image/imageGen/generateImage",
			httpstub.JSONResponse(http.StatusOK, map[string]any{
				"image_url": expectedAvatarURL,
				"width":     512,
				"height":    512,
			}),
		)

		// [3. 请求输入]
		const rawUserPrompt = "一只戴眼镜的橘猫"
		resp := env.HTTP.PostJSON(t, generateUserAvatarPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{"prompt":"一只戴眼镜的橘猫"}`),
		)

		// [4. 预期响应核对]
		data := apicall.RequireSuccess[generateUserAvatarData](t, resp)
		if data.AvatarURL != expectedAvatarURL {
			t.Fatalf("avatar_url = %q, want %q", data.AvatarURL, expectedAvatarURL)
		}
		inboundTraceID := resp.Header.Get("TraceId")
		if len(inboundTraceID) != 32 {
			t.Fatalf("expected 32-char hex TraceId header, got %q", inboundTraceID)
		}

		// [5. 存储终态与调用副作用核对]
		// 5.1 核对 Qwen SDK 调用模型与提示词拼装。
		qwenCalls := qwenRule.RequireCallCount(t, 1)
		var qwenInput struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(qwenCalls[0].ParamsJSON, &qwenInput); err != nil {
			t.Fatalf("unmarshal Qwen SDK params: %v", err)
		}
		if qwenInput.Model != "qwen3.8-flash" || !strings.Contains(qwenInput.Prompt, rawUserPrompt) {
			t.Fatalf("unexpected Qwen SDK params: model=%q prompt=%q", qwenInput.Model, qwenInput.Prompt)
		}

		// 5.2 核对公司图片服务收到了 Qwen 优化后的提示词，且透传了相同的链路追踪标识 TraceId 与 W3C traceparent。
		imageCalls := imageGenRoute.RequireCallCount(t, 1)
		var outboundBody struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(imageCalls[0].Body, &outboundBody); err != nil || outboundBody.Prompt != optimizedPrompt {
			t.Fatalf("company imageGen body = %s, want prompt %q", imageCalls[0].Body, optimizedPrompt)
		}
		if gotTrace := imageCalls[0].Header.Get("TraceId"); gotTrace != inboundTraceID {
			t.Fatalf("outbound TraceId = %q, want %q", gotTrace, inboundTraceID)
		}
		if gotTraceparent := imageCalls[0].Header.Get("Traceparent"); !strings.Contains(gotTraceparent, inboundTraceID) {
			t.Fatalf("outbound traceparent %q does not carry trace ID %q", gotTraceparent, inboundTraceID)
		}

		// 5.3 核对数据库用户资料未发生任何变更。
		stored := queryStoredUserProfileByID(t, env, operatorID)
		if stored.AvatarURL.Valid || stored.Version != 0 {
			t.Fatalf("generateUserAvatar must not mutate user_profile, got %+v", stored)
		}
	})

	t.Run("02_reject_blank_or_invalid_prompt", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			rawBody         string
			wantField       string
		}{
			{
				name:            "whitespace_only_prompt",
				businessExplain: "仅含空格与换行的 prompt 在 Validator 层被拒绝返回 400 并定位字段 prompt，绝不消耗下游 AI 与图片配额",
				rawBody:         `{"prompt":"  \n\t  "}`,
				wantField:       "prompt",
			},
			{
				name:            "missing_prompt_field",
				businessExplain: "缺失必填字段 prompt 时返回 400 并定位字段 prompt",
				rawBody:         `{}`,
				wantField:       "prompt",
			},
			{
				name:            "malformed_json",
				businessExplain: "JSON 语法错误在绑定阶段返回 400 INVALID_ARGUMENT",
				rawBody:         `{"prompt":123}`,
				wantField:       "",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				memberToken := env.Auth.IssueMemberToken(t, 40010)

				// [2. 外部依赖模拟] 不注册任何下游规则（预期 0 次调用）。

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, generateUserAvatarPath,
					apicall.WithBearerToken(memberToken),
					apicall.WithRawJSON(tc.rawBody),
				)

				// [4. 预期响应核对]
				errInfo := apicall.RequireError(t, resp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
				if tc.wantField != "" {
					apicall.RequireFieldError(t, errInfo, tc.wantField)
				}

				// [5. 存储终态与调用副作用核对]
				env.SDKMock.RequireNoCalls(t)
				env.CompanyStub("python-service-template").RequireNoCalls(t)
			})
		}
	})

	t.Run("03_reject_unauthenticated_and_service", func(t *testing.T) {
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
				businessExplain: "匿名请求不允许触发 AI 头像生成，Controller 返回 401 UNAUTHENTICATED",
				authHeader:      func(*testing.T, *harness.Environment) string { return "" },
				wantStatus:      http.StatusUnauthorized,
				wantCode:        "UNAUTHENTICATED",
			},
			{
				name:            "expired_member_token",
				businessExplain: "已过期的成员 JWT 在认证中间件即被拒绝返回 401 UNAUTHENTICATED",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueExpiredMemberToken(t, 40020)
				},
				wantStatus: http.StatusUnauthorized,
				wantCode:   "UNAUTHENTICATED",
			},
			{
				name:            "service_admin_caller_forbidden",
				businessExplain: "service/admin 服务凭证不具备普通成员身份，Controller 返回 403 USER_PROFILE_PERMISSION_DENIED",
				authHeader: func(t *testing.T, env *harness.Environment) string {
					return "Bearer " + env.Auth.IssueServiceAdminToken(t, "ops-batch")
				},
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

				// [2. 外部依赖模拟] 不注册任何下游规则（预期 0 次调用）。

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, generateUserAvatarPath,
					apicall.WithHeaderIfNonEmpty("Authorization", header),
					apicall.WithRawJSON(`{"prompt":"watercolor Shiba Inu"}`),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, tc.wantStatus, tc.wantCode, tc.wantBusinessCode)

				// [5. 存储终态与调用副作用核对]
				env.SDKMock.RequireNoCalls(t)
				env.CompanyStub("python-service-template").RequireNoCalls(t)
			})
		}
	})

	t.Run("04_propagate_qwen_sdk_failure", func(t *testing.T) {
		// [业务解释]
		// 当上游第三方 Qwen SDK 发生限流或服务故障时，Service 必须立即中止流程并由 Controller 映射为 500 INTERNAL_ERROR，
		// 严禁继续用未优化的原始提示词请求公司图片服务。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		memberToken := env.Auth.IssueMemberToken(t, 40030)

		// [2. 外部依赖模拟]
		qwenRule := env.SDKMock.Expect("ai.llm.qwen", "GenerateText",
			sdkmock.RespondError("UPSTREAM_RATE_LIMIT", http.StatusTooManyRequests, 0, "provider rate limit"))
		// python-service-template 不注册任何路由。

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, generateUserAvatarPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{"prompt":"赛博朋克风格宇航员"}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusInternalServerError, "INTERNAL_ERROR", "")

		// [5. 存储终态与调用副作用核对]
		qwenRule.RequireCallCount(t, 1)
		env.CompanyStub("python-service-template").RequireNoCalls(t)
	})

	t.Run("05_abort_when_qwen_returns_empty_text", func(t *testing.T) {
		// [业务解释]
		// 即使 Qwen SDK 调用本身返回成功，若返回的优化文本经裁剪后为空白，Service 仍须视为无效生成结果并返回 500 INTERNAL_ERROR，
		// 阻断向公司图片生成服务发送空提示词请求。
		env := suite.Shared(t)

		// [1. 前置数据与身份]
		memberToken := env.Auth.IssueMemberToken(t, 40040)

		// [2. 外部依赖模拟]
		qwenRule := env.SDKMock.Expect("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{
			"content": "   \t\n  ",
		}))

		// [3. 请求输入]
		resp := env.HTTP.PostJSON(t, generateUserAvatarPath,
			apicall.WithBearerToken(memberToken),
			apicall.WithRawJSON(`{"prompt":"水墨山水头像"}`),
		)

		// [4. 预期响应核对]
		apicall.RequireError(t, resp, http.StatusInternalServerError, "INTERNAL_ERROR", "")

		// [5. 存储终态与调用副作用核对]
		qwenRule.RequireCallCount(t, 1)
		env.CompanyStub("python-service-template").RequireNoCalls(t)
	})

	t.Run("06_propagate_company_image_gen_failure", func(t *testing.T) {
		cases := []struct {
			name            string
			businessExplain string
			stubResponder   httpstub.Responder
		}{
			{
				name:            "company_service_returns_502",
				businessExplain: "Qwen 优化成功但公司图片服务返回 HTTP 502 时，Company Client 将非 200 状态判为调用失败，接口统一返回 500 INTERNAL_ERROR",
				stubResponder:   httpstub.RawResponse(http.StatusBadGateway, "application/json", []byte(`{"error":"upstream failed"}`)),
			},
			{
				name:            "company_service_returns_malformed_json",
				businessExplain: "公司图片服务虽返回 HTTP 200 但响应正文为损坏 JSON 时，Company Client 解码失败，接口统一返回 500 INTERNAL_ERROR",
				stubResponder:   httpstub.RawResponse(http.StatusOK, "application/json", []byte(`{"image_url":`)),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// [业务解释] 见 tc.businessExplain
				env := suite.Shared(t)

				// [1. 前置数据与身份]
				memberToken := env.Auth.IssueMemberToken(t, 40050)

				// [2. 外部依赖模拟]
				qwenRule := env.SDKMock.Expect("ai.llm.qwen", "GenerateText", sdkmock.RespondJSON(map[string]any{
					"content": "minimalist geometric fox avatar",
				}))
				imageRoute := env.CompanyStub("python-service-template").Expect(
					http.MethodPost, "/api/v1/image/imageGen/generateImage", tc.stubResponder,
				)

				// [3. 请求输入]
				resp := env.HTTP.PostJSON(t, generateUserAvatarPath,
					apicall.WithBearerToken(memberToken),
					apicall.WithRawJSON(`{"prompt":"极简几何狐狸"}`),
				)

				// [4. 预期响应核对]
				apicall.RequireError(t, resp, http.StatusInternalServerError, "INTERNAL_ERROR", "")

				// [5. 存储终态与调用副作用核对]
				qwenRule.RequireCallCount(t, 1)
				imageRoute.RequireCallCount(t, 1)
			})
		}
	})
}
