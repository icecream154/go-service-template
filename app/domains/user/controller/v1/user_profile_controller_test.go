package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	httpauthz "go-service-template/app/http/authz"
	"go-service-template/internal/check"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

func TestUserProfileDTO(t *testing.T) {
	emptyAvatar := ""
	avatar := "https://example.com/avatar.png"
	tests := []struct {
		name       string
		avatar     *string
		wantAvatar string
	}{
		// 未提供头像必须保留 null，不能在输出时补为空字符串。
		{name: "missing avatar", wantAvatar: "null"},
		// 显式空头像必须保留空字符串，与从未提供头像区分。
		{name: "empty avatar", avatar: &emptyAvatar, wantAvatar: `""`},
		// 已提供地址应原样保留，转换不清洗或替换有效头像。
		{name: "provided avatar", avatar: &avatar, wantAvatar: `"https://example.com/avatar.png"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := &models.UserProfile{
				ID: 9007199254740993, AccountSource: "user/member", ExternalAccountID: "external-account",
				UserName: "用户", AvatarURL: test.avatar, IsEnabled: false, Version: 7,
				CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			}
			before := *profile
			got := userProfileDTO(profile)
			want := userprofiledto.UserProfile{UserID: 9007199254740993, UserName: "用户", AvatarURL: test.avatar, IsEnabled: false, Version: 7}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DTO = %#v, want %#v", got, want)
			}
			body, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 5 || string(fields["user_id"]) != `"9007199254740993"` || string(fields["avatar_url"]) != test.wantAvatar {
				t.Errorf("JSON fields = %s; want five public fields, string ID and avatar %s", body, test.wantAvatar)
			}
			if _, exists := fields["account_source"]; exists {
				t.Error("DTO exposes account_source")
			}
			if _, exists := fields["external_account_id"]; exists {
				t.Error("DTO exposes external_account_id")
			}
			if !reflect.DeepEqual(*profile, before) {
				t.Error("DTO conversion changed the source model")
			}
		})
	}
}

func TestUserProfileDTOList(t *testing.T) {
	tests := []struct {
		name     string
		profiles []*models.UserProfile
		want     []userprofiledto.UserProfile
	}{
		// nil 查询结果应转成非 nil 空列表，使响应编码为 []。
		{name: "nil list", want: []userprofiledto.UserProfile{}},
		// 已分配的空结果与 nil 输入具有相同的空页协议。
		{name: "empty list", profiles: []*models.UserProfile{}, want: []userprofiledto.UserProfile{}},
		// 输入顺序刻意不按 ID 排列，验证转换保留业务层给定的顺序和状态。
		{name: "preserve order", profiles: []*models.UserProfile{
			{ID: 9, UserName: "first", IsEnabled: false, Version: 2},
			{ID: 3, UserName: "second", IsEnabled: true, Version: 1},
		}, want: []userprofiledto.UserProfile{
			{UserID: 9, UserName: "first", IsEnabled: false, Version: 2},
			{UserID: 3, UserName: "second", IsEnabled: true, Version: 1},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := userProfileDTOList(test.profiles)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DTO list = %#v, want %#v", got, test.want)
			}
			if len(test.want) == 0 {
				body, err := json.Marshal(got)
				if err != nil || string(body) != "[]" {
					t.Errorf("empty list JSON = %s, error = %v; want []", body, err)
				}
			}
		})
	}
}

func TestMapUserProfileErrorCodes(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantCode     response.ErrorCode
		wantBusiness response.BusinessErrorCode
	}{
		// 缺少凭证归入认证失败，不附加资料业务码。
		{name: "authentication required", err: httpauthz.ErrAuthenticationRequired, wantCode: response.CodeUnauthenticated},
		// 无效会员身份同样归入认证失败，而不是已认证后的权限拒绝。
		{name: "invalid member identity", err: httpauthz.ErrInvalidMemberIdentity, wantCode: response.CodeUnauthenticated},
		// 规则定义故障属于内部错误，不能误报为调用者输入问题。
		{name: "invalid rule", err: check.NewRuleError("range", "invalid bounds"), wantCode: response.CodeInternalError},
		// 被包装的未找到错误仍需识别，并输出既定资料业务码。
		{name: "wrapped not found", err: fmt.Errorf("lookup: %w", userservice.ErrUserProfileNotFound), wantCode: response.CodeNotFound, wantBusiness: "USER_PROFILE_NOT_FOUND"},
		// 重复账户必须与其他冲突原因区分，供调用者作出对应处理。
		{name: "duplicate account", err: userservice.ErrAccountAlreadyExists, wantCode: response.CodeConflict, wantBusiness: "USER_ACCOUNT_ALREADY_EXISTS"},
		// 停用用户是明确状态冲突，保留专属业务码。
		{name: "disabled user", err: userservice.ErrUserDisabled, wantCode: response.CodeConflict, wantBusiness: "USER_DISABLED"},
		// 旧版本更新失败必须映射为版本冲突，不能归入通用内部故障。
		{name: "version conflict", err: userservice.ErrUserProfileVersionConflict, wantCode: response.CodeConflict, wantBusiness: "USER_PROFILE_VERSION_CONFLICT"},
		// 已认证但身份范围不符时返回权限拒绝及资料权限业务码。
		{name: "principal not allowed", err: httpauthz.ErrPrincipalNotAllowed, wantCode: response.CodePermissionDenied, wantBusiness: "USER_PROFILE_PERMISSION_DENIED"},
		// Controller 本人访问检查失败与 Service 的权限结果保持同一协议。
		{name: "controller access denied", err: errUserProfileAccessDenied, wantCode: response.CodePermissionDenied, wantBusiness: "USER_PROFILE_PERMISSION_DENIED"},
		// Service 权限错误经过包装后也必须保留原业务分类。
		{name: "wrapped service permission", err: fmt.Errorf("update: %w", userservice.ErrUserProfilePermissionDenied), wantCode: response.CodePermissionDenied, wantBusiness: "USER_PROFILE_PERMISSION_DENIED"},
		// 结构化输入校验错误应归为参数错误，且不补造业务码。
		{name: "invalid input", err: check.NewViolation("positive", "must be positive"), wantCode: response.CodeInvalidArgument},
		// 同时存在规则故障和输入错误时优先报告规则故障，避免将程序问题推给调用方。
		{name: "rule error takes precedence", err: errors.Join(check.ErrInvalidValue, check.ErrInvalidRule), wantCode: response.CodeInternalError},
		// 不认识的技术故障使用内部错误兜底，不伪装为已知业务冲突。
		{name: "unknown failure", err: errors.New("storage unavailable"), wantCode: response.CodeInternalError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, business := mapUserProfileErrorCodes(test.err)
			if code != test.wantCode || business != test.wantBusiness {
				t.Errorf("codes = (%s, %s), want (%s, %s)", code, business, test.wantCode, test.wantBusiness)
			}
		})
	}
}

func TestFailUserProfileOperation(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantCode      response.ErrorCode
		wantBusiness  response.BusinessErrorCode
		wantChallenge string
	}{
		// 缺少身份时提供基础 Bearer 挑战头，不宣称调用者已经提交了无效令牌。
		{name: "missing identity", err: httpauthz.ErrAuthenticationRequired, wantStatus: 401, wantCode: response.CodeUnauthenticated, wantChallenge: `Bearer realm="api"`},
		// 包装后的无效身份仍应加 invalid_token，同时保留完整错误消息。
		{name: "invalid identity", err: fmt.Errorf("identity: %w", httpauthz.ErrInvalidMemberIdentity), wantStatus: 401, wantCode: response.CodeUnauthenticated, wantChallenge: `Bearer realm="api", error="invalid_token"`},
		// 本人访问拒绝输出业务码，但不能携带认证挑战头。
		{name: "permission denied", err: errUserProfileAccessDenied, wantStatus: 403, wantCode: response.CodePermissionDenied, wantBusiness: "USER_PROFILE_PERMISSION_DENIED"},
		// 已知未找到结果输出 404 与资料业务码，保持 data 为 null。
		{name: "not found", err: userservice.ErrUserProfileNotFound, wantStatus: 404, wantCode: response.CodeNotFound, wantBusiness: "USER_PROFILE_NOT_FOUND"},
		// 并发更新失败输出 409，供调用方辨别需要重新读取版本。
		{name: "version conflict", err: userservice.ErrUserProfileVersionConflict, wantStatus: 409, wantCode: response.CodeConflict, wantBusiness: "USER_PROFILE_VERSION_CONFLICT"},
		// 纯校验失败只输出原错误消息，不能由 Controller 补造字段明细。
		{name: "invalid argument", err: check.NewViolation("positive", "must be positive"), wantStatus: 400, wantCode: response.CodeInvalidArgument},
		// 技术错误返回 500 时也保留收到的消息，而非替换为固定兜底文案。
		{name: "technical failure", err: errors.New("storage unavailable"), wantStatus: 500, wantCode: response.CodeInternalError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			failUserProfileOperation(ctx, test.err)
			assertUserControllerFailure(t, ctx, recorder, test.err, test.wantStatus, test.wantCode, test.wantBusiness, test.wantChallenge)
		})
	}
}

func assertUserControllerFailure(t *testing.T, ctx *gin.Context, recorder *httptest.ResponseRecorder, source error, wantStatus int, wantCode response.ErrorCode, wantBusiness response.BusinessErrorCode, wantChallenge string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Errorf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if !ctx.IsAborted() {
		t.Error("failure did not abort the handler chain")
	}
	if got := recorder.Header().Get("WWW-Authenticate"); got != wantChallenge {
		t.Errorf("challenge = %q, want %q", got, wantChallenge)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload) != 2 || string(payload["data"]) != "null" {
		t.Errorf("response = %s; want data:null and error", recorder.Body.Bytes())
	}
	var failure response.ErrorInfo
	if err := json.Unmarshal(payload["error"], &failure); err != nil {
		t.Fatalf("decode failure: %v", err)
	}
	if failure.Code != wantCode || failure.BusinessCode != wantBusiness || failure.Message != source.Error() {
		t.Errorf("failure = %#v, want code %s, business %s, message %q", failure, wantCode, wantBusiness, source.Error())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload["error"], &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["details"]; exists {
		t.Error("Controller supplied unrequested field details")
	}
	if _, exists := fields["business_code"]; exists != (wantBusiness != "") {
		t.Errorf("business_code presence = %v, want %v", exists, wantBusiness != "")
	}
}
