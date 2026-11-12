package usermembership

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

func TestQueryUserMembershipByUserIDRejectsBadRequest(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantFields []string
	}{
		// 不支持的绑定方法在校验前失败，响应不补造字段明细。
		{name: "binding failure", method: http.MethodPut, target: "/?user_id=1"},
		// 必填 ID 缺失时必须返回 user_id 明细，不能查询匿名目标。
		{name: "missing ID", method: http.MethodGet, target: "/", wantFields: []string{"user_id"}},
		// 显式零值可绑定但不是有效目标，不能进入 Controller。
		{name: "zero ID", method: http.MethodGet, target: "/?user_id=0", wantFields: []string{"user_id"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newMembershipValidatorRequest(test.method, test.target)
			NewQueryUserMembershipByUserIDValidator(nil).QueryUserMembershipByUserID(ctx)
			assertMembershipValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

func TestValidateQueryUserMembershipByUserIDRequest(t *testing.T) {
	tests := []struct {
		name       string
		queryID    string
		wantID     string
		wantFields []string
	}{
		// 百分号编码的正号与前导零按现有 ID 文本规则保留。
		{name: "original decimal text", queryID: "%2B00042", wantID: "+00042"},
		// 最大 int64 不经浮点数转换，允许完整标识通过。
		{name: "maximum ID", queryID: "9223372036854775807", wantID: "9223372036854775807"},
		// 超过最大 int64 的数字必须由 ID 规则拒绝。
		{name: "overflow ID", queryID: "9223372036854775808", wantID: "9223372036854775808", wantFields: []string{"user_id"}},
		// 校验不能通过裁剪空白把无效输入修复为有效 ID。
		{name: "whitespace ID", queryID: "%201", wantID: " 1", wantFields: []string{"user_id"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := newMembershipValidatorRequest(http.MethodGet, "/?user_id="+test.queryID)
			var request usermembershipdto.QueryUserMembershipByUserIDRequest
			if err := commonvalidator.Bind(ctx, &request); err != nil {
				t.Fatalf("Bind() error = %v", err)
			}
			details, err := validateQueryUserMembershipByUserIDRequest(&request)
			if err != nil {
				t.Fatalf("validateQueryUserMembershipByUserIDRequest() error = %v", err)
			}
			assertMembershipValidatorFields(t, details, test.wantFields...)
			if request.UserID != test.wantID {
				t.Errorf("UserID = %q, want %q", request.UserID, test.wantID)
			}
		})
	}
}

func newMembershipValidatorRequest(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	return ctx, recorder
}

func assertMembershipValidatorFields(t *testing.T, details []response.FieldError, wantFields ...string) {
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

func assertMembershipValidatorBadRequest(t *testing.T, ctx *gin.Context, recorder *httptest.ResponseRecorder, wantFields ...string) {
	t.Helper()
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("HTTP status = %d, want 400, body = %s", recorder.Code, recorder.Body.String())
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
		t.Fatalf("decode failure response: %v", err)
	}
	if string(envelope.Data) != "null" || envelope.Error == nil {
		t.Fatalf("invalid failure envelope: %s", recorder.Body.String())
	}
	failure := envelope.Error
	if failure.Code != response.CodeInvalidArgument || strings.TrimSpace(failure.Message) == "" {
		t.Errorf("error = %q, %q, want INVALID_ARGUMENT and a nonblank message", failure.Code, failure.Message)
	}
	if len(wantFields) == 0 {
		if len(failure.Details) != 0 {
			t.Errorf("binding failure details = %s, want omitted", failure.Details)
		}
		return
	}
	var details []response.FieldError
	if err := json.Unmarshal(failure.Details, &details); err != nil {
		t.Fatalf("decode field errors: %v", err)
	}
	assertMembershipValidatorFields(t, details, wantFields...)
	if len(details) > 0 && failure.Message != details[0].Error() {
		t.Errorf("message = %q, want first field error %q", failure.Message, details[0].Error())
	}
}
