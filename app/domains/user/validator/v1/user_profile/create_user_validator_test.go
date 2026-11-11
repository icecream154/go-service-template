package userprofile

import (
	"net/http"
	"reflect"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	commonvalidator "go-service-template/internal/validator"
)

type createUserHTTPCase struct {
	name       string
	body       string
	wantFields []string
}

func TestCreateUser(t *testing.T) {
	tests := []createUserHTTPCase{
		// 截断 JSON 对象，验证绑定错误返回 400，且响应只包含通用错误信息。
		{name: "malformed JSON", body: `{"account_source":`},
		// 可选头像仍须按字符串绑定，验证 JSON 数字属于绑定错误。
		{
			name: "numeric avatar",
			body: `{"account_source":"OIDC","external_account_id":"0007","user_name":"小明","avatar_url":42}`,
		},
		// 空对象省略全部字段，验证三个必填字段按顺序报告，头像保持可选。
		{
			name: "missing required fields", body: `{}`,
			wantFields: []string{"account_source", "external_account_id", "user_name"},
		},
		// 给三个必填字段传空白，并提供无效头像，验证四条错误依请求字段顺序汇总。
		{
			name:       "all fields invalid",
			body:       `{"account_source":" ","external_account_id":"\t","user_name":"\u3000","avatar_url":"avatar.png"}`,
			wantFields: []string{"account_source", "external_account_id", "user_name", "avatar_url"},
		},
	}

	validator := NewCreateUserValidator(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newProfileValidatorRequest(http.MethodPost, "/", test.body)
			validator.CreateUser(ctx)
			assertProfileValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

type createUserAvatarCase struct {
	name       string
	avatarJSON string
	wantAvatar *string
	wantFields []string
}

func TestValidateCreateUserRequest(t *testing.T) {
	tests := []createUserAvatarCase{
		// 省略头像字段，验证绑定保留 nil 且注册校验通过。
		{name: "omitted avatar"},
		// 显式提供 null，验证它也表示尚未提供头像。
		{name: "null avatar", avatarJSON: `,"avatar_url":null`},
		// 显式提供空字符串，验证保留非 nil 的空字符串且注册校验通过。
		{name: "empty avatar", avatarJSON: `,"avatar_url":""`, wantAvatar: new("")},
		// 提供完整 HTTPS 地址，验证已提供的头像可以按领域规则通过。
		{
			name:       "valid avatar",
			avatarJSON: `,"avatar_url":"https://images.example.test/profile.png"`,
			wantAvatar: new("https://images.example.test/profile.png"),
		},
		// 只含空白的头像有值，验证它会进入领域校验并定位到头像字段。
		{
			name:       "blank avatar",
			avatarJSON: `,"avatar_url":"  "`,
			wantAvatar: new("  "),
			wantFields: []string{"avatar_url"},
		},
		// 提供有路径但无协议和主机的头像，验证错误明细定位到 avatar_url。
		{
			name:       "relative avatar URL",
			avatarJSON: `,"avatar_url":"/profile.png"`,
			wantAvatar: new("/profile.png"),
			wantFields: []string{"avatar_url"},
		},
	}

	const base = `{"account_source":"OIDC","external_account_id":"000A/账户-42","user_name":" 小明 "`
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := newProfileValidatorRequest(http.MethodPost, "/", base+test.avatarJSON+"}")
			var request userprofiledto.CreateUserRequest
			if err := commonvalidator.Bind(ctx, &request); err != nil {
				t.Fatalf("Bind() error = %v, want nil", err)
			}
			details, err := validateCreateUserRequest(&request)
			if err != nil {
				t.Fatalf("validateCreateUserRequest() error = %v, want nil", err)
			}
			assertProfileValidatorFields(t, details, test.wantFields...)

			want := userprofiledto.CreateUserRequest{
				AccountSource:     "OIDC",
				ExternalAccountID: "000A/账户-42",
				UserName:          " 小明 ",
				AvatarURL:         test.wantAvatar,
			}
			if !reflect.DeepEqual(request, want) {
				t.Errorf("validated request = %#v, want %#v", request, want)
			}
		})
	}
}
