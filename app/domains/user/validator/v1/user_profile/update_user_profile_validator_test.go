package userprofile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

type updateProfileBadRequestCase struct {
	name       string
	body       string
	wantFields []string
}

func TestUpdateUserProfileReturnsBadRequest(t *testing.T) {
	tests := []updateProfileBadRequestCase{
		// 截断 JSON 对象，验证绑定失败返回 400，且没有字段校验明细。
		{name: "malformed JSON", body: `{"user_id":`},
		// 用户 ID 用未加引号的数字表示，验证此接口的字符串 ID 契约在绑定阶段生效。
		{name: "unquoted user ID", body: `{"user_id":42,"expected_version":0,"user_name":"小明","avatar_url":"https://img.test/a"}`},
		// 版本以引号包裹，验证类型错误按绑定失败返回 400。
		{name: "quoted expected version", body: `{"user_id":"42","expected_version":"0","user_name":"小明","avatar_url":"https://img.test/a"}`},
		// JSON 可绑定且其他字段合法，验证省略版本时 400 携带 expected_version 的字段明细。
		{
			name: "missing expected version", body: `{"user_id":"42","user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantFields: []string{"expected_version"},
		},
		// 四个字段都能绑定但分别违反规则，验证 400 保留明细顺序，并以第一条明细作为消息。
		{
			name: "multiple field failures", body: `{"user_id":"0","expected_version":-1,"user_name":" ","avatar_url":"ftp://img.test/a"}`,
			wantFields: []string{"user_id", "expected_version", "user_name", "avatar_url"},
		},
	}

	validator := NewUpdateUserProfileValidator(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newProfileValidatorRequest(http.MethodPost, "/profiles/update", test.body)
			validator.UpdateUserProfile(ctx)
			assertProfileValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

type validateUpdateProfileCase struct {
	name        string
	body        string
	wantVersion *int64
	wantFields  []string
}

func TestValidateUpdateUserProfileRequest(t *testing.T) {
	tests := []validateUpdateProfileCase{
		// 省略版本字段，验证绑定后缺少版本时只报告 expected_version。
		{
			name: "omitted version", body: `{"user_id":"42","user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantFields: []string{"expected_version"},
		},
		// 显式 null 表示没有有效版本，验证与省略字段一样被拒绝。
		{
			name: "null version", body: `{"user_id":"42","expected_version":null,"user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantFields: []string{"expected_version"},
		},
		// 显式传入 0，验证绑定保留非空指针及初始版本值，并通过校验。
		{
			name: "explicit zero version", body: `{"user_id":"42","expected_version":0,"user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantVersion: new(int64(0)),
		},
		// 版本比合法下限小一，验证负数被定位到 expected_version。
		{
			name: "version below minimum", body: `{"user_id":"42","expected_version":-1,"user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantFields: []string{"expected_version"},
		},
		// 版本为 int64 最大值减一，验证接受上限并为后续递增留出空间。
		{
			name:        "version at maximum",
			body:        `{"user_id":"42","expected_version":9223372036854775806,"user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantVersion: new(int64(9223372036854775806)),
		},
		// 版本可绑定为 int64 但已超出业务上限，验证失败归到字段校验。
		{
			name:       "version above maximum",
			body:       `{"user_id":"42","expected_version":9223372036854775807,"user_name":"小明","avatar_url":"https://img.test/a"}`,
			wantFields: []string{"expected_version"},
		},
		// 省略头像，验证更新接口要求提供 avatar_url。
		{
			name: "omitted avatar", body: `{"user_id":"42","expected_version":0,"user_name":"小明"}`,
			wantFields: []string{"avatar_url"},
		},
		// 显式传入 null，验证更新时不能用 null 清除头像。
		{
			name: "null avatar", body: `{"user_id":"42","expected_version":0,"user_name":"小明","avatar_url":null}`,
			wantFields: []string{"avatar_url"},
		},
		// 头像字段存在但为空，验证显式空串仍不满足更新的必填约定。
		{
			name: "empty avatar", body: `{"user_id":"42","expected_version":0,"user_name":"小明","avatar_url":""}`,
			wantFields: []string{"avatar_url"},
		},
		// 使用相对图片路径，验证头像领域规则接入后将错误关联到 avatar_url。
		{
			name: "relative avatar URL", body: `{"user_id":"42","expected_version":0,"user_name":"小明","avatar_url":"/a.png"}`,
			wantFields: []string{"avatar_url"},
		},
		// 空 JSON 对象绑定为各字段零值，验证四条错误按请求字段顺序汇总，每个字段仅一条。
		{
			name: "all fields invalid in request order", body: `{}`,
			wantFields: []string{"user_id", "expected_version", "user_name", "avatar_url"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := newProfileValidatorRequest(http.MethodPost, "/profiles/update", test.body)
			var request userprofiledto.UpdateUserProfileRequest
			if err := commonvalidator.Bind(ctx, &request); err != nil {
				t.Fatalf("bind fixture request: %v", err)
			}

			details, err := validateUpdateUserProfileRequest(&request)
			if err != nil {
				t.Fatalf("validateUpdateUserProfileRequest() error = %v, want nil", err)
			}
			assertProfileValidatorFields(t, details, test.wantFields...)
			if test.wantVersion != nil {
				if request.ExpectedVersion == nil {
					t.Fatalf("ExpectedVersion = nil, want %d", *test.wantVersion)
				}
				if got := *request.ExpectedVersion; got != *test.wantVersion {
					t.Errorf("ExpectedVersion = %d, want %d", got, *test.wantVersion)
				}
			}
		})
	}
}

func newProfileValidatorRequest(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	if method == http.MethodPost {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	return ctx, recorder
}

func assertProfileValidatorFields(t *testing.T, details []response.FieldError, wantFields ...string) {
	t.Helper()

	gotFields := make([]string, len(details))
	for i, detail := range details {
		gotFields[i] = detail.Field
		if strings.TrimSpace(detail.Reason) == "" {
			t.Errorf("field %q has no error reason", detail.Field)
		}
	}
	if !slices.Equal(gotFields, wantFields) {
		t.Errorf("error fields = %v, want %v", gotFields, wantFields)
	}
}

func assertProfileValidatorBadRequest(t *testing.T, ctx *gin.Context, recorder *httptest.ResponseRecorder, wantFields ...string) {
	t.Helper()

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("HTTP status = %d, want %d, body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !ctx.IsAborted() {
		t.Error("validation failure did not abort the handler chain")
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    response.ErrorCode `json:"code"`
			Message string             `json:"message"`
			Details json.RawMessage    `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode HTTP 400 response: %v", err)
	}
	if string(envelope.Data) != "null" || envelope.Error == nil {
		t.Fatalf("invalid failure envelope: %s", recorder.Body.String())
	}
	failure := envelope.Error
	if failure.Code != response.CodeInvalidArgument || strings.TrimSpace(failure.Message) == "" {
		t.Errorf("error code and message = %q, %q, want %q and a nonblank message", failure.Code, failure.Message, response.CodeInvalidArgument)
	}
	if len(wantFields) == 0 {
		if len(failure.Details) != 0 {
			t.Errorf("binding failure details = %s, want field omitted", failure.Details)
		}
		return
	}

	var details []response.FieldError
	if err := json.Unmarshal(failure.Details, &details); err != nil {
		t.Fatalf("decode field errors: %v", err)
	}
	assertProfileValidatorFields(t, details, wantFields...)
	if len(details) > 0 && failure.Message != details[0].Error() {
		t.Errorf("error message = %q, want first field error %q", failure.Message, details[0].Error())
	}
}
