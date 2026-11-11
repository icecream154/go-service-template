package userprofile

import (
	"net/http"
	"strings"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
)

type generateUserAvatarHTTPCase struct {
	name       string
	body       string
	wantFields []string
}

func TestGenerateUserAvatar(t *testing.T) {
	tests := []generateUserAvatarHTTPCase{
		// 提示词用 JSON 数字传入，验证类型不匹配作为绑定错误返回 400。
		{name: "numeric prompt", body: `{"prompt":17}`},
		// 省略必填的提示词，验证绑定后返回定位到 prompt 的字段错误。
		{name: "omitted prompt", body: `{}`, wantFields: []string{"prompt"}},
		// 显式传入 null，验证零值提示词也按字段错误返回 400。
		{name: "null prompt", body: `{"prompt":null}`, wantFields: []string{"prompt"}},
		// JSON 字符串可正常解码但内容全是空白，验证字段校验拒绝。
		{name: "blank prompt", body: `{"prompt":"\t\u3000"}`, wantFields: []string{"prompt"}},
	}

	validator := NewGenerateUserAvatarValidator(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newProfileValidatorRequest(http.MethodPost, "/", test.body)
			validator.GenerateUserAvatar(ctx)
			assertProfileValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

type generateUserAvatarPromptCase struct {
	name       string
	prompt     string
	wantFields []string
}

func TestValidateGenerateUserAvatarRequest(t *testing.T) {
	tests := []generateUserAvatarPromptCase{
		// 单个汉字已构成有效描述，验证最短的非空白提示词可用。
		{name: "single code point", prompt: "猫"},
		// 多行描述带有首尾空白，验证允许非空白原文并保留格式。
		{name: "multiline prompt", prompt: "  水彩头像\n浅蓝背景  "},
		// 提示词超过头像 URL 的码点上限，验证提示词使用自己的文本规则。
		{name: "long nonblank prompt", prompt: strings.Repeat("图", 513)},
		// 空字符串没有描述，验证返回 prompt 字段错误。
		{name: "empty prompt", wantFields: []string{"prompt"}},
		// 混用制表符、换行和全角空格，验证 Unicode 空白都不构成有效提示词。
		{name: "Unicode whitespace only", prompt: "\t\n\u3000", wantFields: []string{"prompt"}},
		// 在合法文字后拼接非法字节，验证绑定后的字段仍须整体通过 UTF-8 检查。
		{name: "invalid UTF-8", prompt: "猫\xff", wantFields: []string{"prompt"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := userprofiledto.GenerateUserAvatarRequest{Prompt: test.prompt}
			details, err := validateGenerateUserAvatarRequest(&request)
			if err != nil {
				t.Fatalf("validateGenerateUserAvatarRequest() error = %v, want nil", err)
			}
			assertProfileValidatorFields(t, details, test.wantFields...)
			if request.Prompt != test.prompt {
				t.Errorf("validated prompt = %q, want original %q", request.Prompt, test.prompt)
			}
		})
	}
}
