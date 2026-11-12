package userprofile

import (
	"net/http"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"
)

type queryUserProfileByUserIDHTTPCase struct {
	name        string
	method      string
	target      string
	wantDetails []response.FieldError
}

func TestQueryUserProfileByUserIDBadRequest(t *testing.T) {
	tests := []queryUserProfileByUserIDHTTPCase{
		// 直接向入口传入通用绑定不支持的方法，验证绑定错误返回 400 且省略字段明细。
		{name: "unsupported binding method", method: http.MethodPut, target: "/queryUserProfileByUserId?user_id=1"},
		// GET 缺少必填 Query 参数，验证字段校验返回 400 并定位到 user_id。
		{name: "missing query ID", method: http.MethodGet, target: "/queryUserProfileByUserId", wantDetails: []response.FieldError{{Field: "user_id", Reason: "must be a positive 64-bit integer"}}},
		// 零可以绑定为文本但不是正整数，验证这一失败同样由字段明细表达。
		{name: "zero query ID", method: http.MethodGet, target: "/queryUserProfileByUserId?user_id=0", wantDetails: []response.FieldError{{Field: "user_id", Reason: "must be a positive 64-bit integer"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newProfileValidatorRequest(test.method, test.target, "")
			validator := NewQueryUserProfileByUserIDValidator(nil)

			validator.QueryUserProfileByUserID(ctx)

			assertProfileValidatorBadRequest(t, ctx, recorder, test.wantDetails...)
		})
	}
}

func TestQueryUserProfileByUserIDBindsQueryText(t *testing.T) {
	// Query 用百分号编码显式加号并保留前导零，验证绑定和校验后仍保存有效 ID 的原始十进制文本。
	ctx, _ := newProfileValidatorRequest(http.MethodGet, "/queryUserProfileByUserId?user_id=%2B00042", "")
	var request userprofiledto.QueryUserProfileByUserIDRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		t.Fatalf("Bind() error = %v, want nil", err)
	}

	details, err := validateQueryUserProfileByUserIDRequest(&request)

	if err != nil {
		t.Fatalf("validateQueryUserProfileByUserIDRequest() error = %v, want nil", err)
	}
	assertProfileValidatorDetails(t, details)
	if request.UserID != "+00042" {
		t.Errorf("UserID = %q, want %q", request.UserID, "+00042")
	}
}

type queryUserProfileByUserIDRequestCase struct {
	name        string
	userID      string
	wantDetails []response.FieldError
}

func TestValidateQueryUserProfileByUserIDRequest(t *testing.T) {
	tests := []queryUserProfileByUserIDRequestCase{
		// 1 是最小有效用户 ID，验证正数下限可以通过。
		{name: "minimum positive ID", userID: "1"},
		// int64 最大值仍是有效的正数 ID，验证允许上界。
		{name: "maximum int64 ID", userID: "9223372036854775807"},
		// 零紧邻正数下限，验证数字格式正确时仍需满足正数要求。
		{name: "below positive minimum", userID: "0", wantDetails: []response.FieldError{{Field: "user_id", Reason: "must be a positive 64-bit integer"}}},
		// 比 int64 上界多一的文本无法转为有效 ID，验证溢出仍定位到 user_id。
		{name: "above int64 maximum", userID: "9223372036854775808", wantDetails: []response.FieldError{{Field: "user_id", Reason: "must be a positive 64-bit integer"}}},
		// 带前导空格的文本无法按约定解析，验证校验不会先裁剪原值。
		{name: "whitespace before ID", userID: " 1", wantDetails: []response.FieldError{{Field: "user_id", Reason: "must be a positive 64-bit integer"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := userprofiledto.QueryUserProfileByUserIDRequest{UserID: test.userID}

			details, err := validateQueryUserProfileByUserIDRequest(&request)

			if err != nil {
				t.Fatalf("validateQueryUserProfileByUserIDRequest() error = %v, want nil", err)
			}
			assertProfileValidatorDetails(t, details, test.wantDetails...)
			if request.UserID != test.userID {
				t.Errorf("UserID = %q, want original %q", request.UserID, test.userID)
			}
		})
	}
}
