package models_test

import (
	"errors"
	"strings"
	"testing"

	"go-service-template/app/domains/user/models"
	"go-service-template/internal/check"
)

type profileValidationCase struct {
	name     string
	value    string
	wantRule string
}

func TestValidateAccountSource(t *testing.T) {
	testProfileValidation(t, models.ValidateAccountSource, []profileValidationCase{
		// 一个非空白码点足以作为账户来源，验证最短有效值。
		{name: "single code point", value: "A"},
		// 账户来源可以含大写字母和两侧空格，验证规则允许非空白原文。
		{name: "mixed case with surrounding spaces", value: " Provider-A "},
		// 31 个多字节汉字紧邻上限，验证该长度可以通过。
		{name: "one below maximum", value: strings.Repeat("源", 31)},
		// 32 个汉字的字节数超过 32，验证上限按码点计算且包含等号。
		{name: "exact maximum", value: strings.Repeat("源", 32)},
		// 比上限多一个汉字，验证首个超长值被长度规则拒绝。
		{name: "one above maximum", value: strings.Repeat("源", 33), wantRule: "rune_count"},
		// 空串没有任何账户来源，验证非空白规则拒绝缺省值。
		{name: "empty", wantRule: "not_blank"},
		// 全角空格和制表符均为空白，验证非空字符串仍可能被拒绝。
		{name: "Unicode whitespace only", value: "\u3000\t", wantRule: "not_blank"},
		// 在正常字符后附加非法字节，验证编码错误先于长度判断返回。
		{name: "invalid UTF-8", value: "A\xff", wantRule: "valid_utf8"},
	})
}

func TestValidateExternalAccountID(t *testing.T) {
	testProfileValidation(t, models.ValidateExternalAccountID, []profileValidationCase{
		// 单个数字作为文本标识，验证外部账户 ID 可短至一个码点。
		{name: "single code point", value: "0"},
		// 以零开头的标识混有字母和分隔符，验证规则按外部系统的原始文本处理。
		{name: "opaque account identifier", value: "000A/用户-42"},
		// 127 个多字节码点紧邻上限，验证低于上限的标识可以通过。
		{name: "one below maximum", value: strings.Repeat("账", 127)},
		// 128 个汉字恰好达到上限，验证字节数较大也不影响通过。
		{name: "exact maximum", value: strings.Repeat("账", 128)},
		// 增加一个汉字达到 129 个码点，验证超长标识被拒绝。
		{name: "one above maximum", value: strings.Repeat("账", 129), wantRule: "rune_count"},
		// 空串无法表示外部账户，验证缺省值被非空白规则拒绝。
		{name: "empty", wantRule: "not_blank"},
		// 使用普通空格和全角空格构成非空输入，验证全空白标识仍无效。
		{name: "Unicode whitespace only", value: " \u3000", wantRule: "not_blank"},
		// 短输入包含不完整的 UTF-8 序列，验证长度符合要求也不能绕过编码检查。
		{name: "invalid UTF-8", value: "id\xe4\xb8", wantRule: "valid_utf8"},
	})
}

func TestValidateUserName(t *testing.T) {
	testProfileValidation(t, models.ValidateUserName, []profileValidationCase{
		// 一个汉字即可作为昵称，验证最短有效的多字节输入。
		{name: "single code point", value: "名"},
		// 两侧有空格但中间有展示内容，验证空格不会导致非空白检查失败。
		{name: "surrounding spaces", value: " 小明 "},
		// 63 个汉字比上限少一个码点，验证该长度仍可通过。
		{name: "one below maximum", value: strings.Repeat("名", 63)},
		// 每组字母和分解重音占两个码点，32 组恰好达到 64 个码点上限。
		{name: "exact maximum with combining marks", value: strings.Repeat("e\u0301", 32)},
		// 在分解重音昵称后追加一位，验证按 65 个原始码点拒绝。
		{name: "one above maximum with combining marks", value: strings.Repeat("e\u0301", 32) + "x", wantRule: "rune_count"},
		// 在 64 个汉字后保留一个空格，验证尾部空白也计入原文长度。
		{name: "trailing space counts toward maximum", value: strings.Repeat("名", 64) + " ", wantRule: "rune_count"},
		// 空昵称没有展示内容，验证空串被拒绝。
		{name: "empty", wantRule: "not_blank"},
		// 换行和全角空格组成纯空白昵称，验证非空文本也须含有可用内容。
		{name: "Unicode whitespace only", value: "\n\u3000", wantRule: "not_blank"},
		// 汉字后附加一个非法字节，验证含有合法部分的昵称也须整体通过编码检查。
		{name: "invalid UTF-8", value: "名\xff", wantRule: "valid_utf8"},
	})
}

func TestValidateAvatarURL(t *testing.T) {
	const prefix = "https://images.example.test/"
	testProfileValidation(t, models.ValidateAvatarURL, []profileValidationCase{
		// 绝对 HTTP 地址包含主机名和头像路径，验证 HTTP 是允许的协议。
		{name: "absolute HTTP URL", value: "http://images.example.test/avatar.png"},
		// 绝对 HTTPS 地址带查询和片段，验证大小写不同的协议和正常的资源参数均可通过。
		{name: "absolute HTTPS URL with resource parameters", value: "HTTPS://images.example.test/avatar.png?size=64#preview"},
		// 前缀全为 ASCII，使用汉字补齐到 511 个码点，验证低于上限时可通过。
		{name: "one below maximum", value: prefix + strings.Repeat("图", 511-len(prefix))},
		// Unicode 路径使字节数超过 512，验证恰好 512 个码点的完整 URL 仍有效。
		{name: "exact maximum", value: prefix + strings.Repeat("图", 512-len(prefix))},
		// URL 格式不变，只把路径增加到 513 个码点，验证失败原因是超长。
		{name: "one above maximum", value: prefix + strings.Repeat("图", 513-len(prefix)), wantRule: "rune_count"},
		// 对一个已提供的头像值传空串，验证本入口按非空白规则拒绝。
		{name: "empty", wantRule: "not_blank"},
		// 全角空格和制表符没有实际地址，验证在解析 URL 前返回空白错误。
		{name: "Unicode whitespace only", value: "\u3000\t", wantRule: "not_blank"},
		// 在合法的 HTTPS 前缀后附加非法字节，验证文本编码错误先于 URL 语法判断。
		{name: "invalid UTF-8", value: prefix + "\xff", wantRule: "valid_utf8"},
		// 路径看起来像图片资源，但没有协议和主机，验证相对路径被拒绝。
		{name: "relative path", value: "/avatar.png", wantRule: "absolute_url"},
		// URL 即使含主机名，只要省略协议仍是相对引用，验证不能作为头像地址。
		{name: "scheme-relative URL", value: "//images.example.test/avatar.png", wantRule: "absolute_url"},
		// FTP 地址本身合法且有主机，验证头像规则只接受 HTTP 或 HTTPS。
		{name: "unsupported scheme", value: "ftp://images.example.test/avatar.png", wantRule: "http_url"},
		// HTTPS 协议后只有路径，验证主机名也是必需条件。
		{name: "missing hostname", value: "https:///avatar.png", wantRule: "http_url"},
		// 百分号后使用非十六进制字符，验证错误的 URL 转义会被语法规则拒绝。
		{name: "malformed URL escape", value: prefix + "%zz", wantRule: "url"},
	})
}

func testProfileValidation(t *testing.T, validate func(string) error, tests []profileValidationCase) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validate(test.value)
			if test.wantRule == "" {
				if err != nil {
					t.Errorf("validation error = %v, want nil", err)
				}
				return
			}

			if !errors.Is(err, check.ErrInvalidValue) {
				t.Fatalf("validation error = %v, want %v", err, check.ErrInvalidValue)
			}
			var violation *check.Violation
			if !errors.As(err, &violation) || violation == nil {
				t.Fatalf("validation error = %T (%v), want *check.Violation", err, err)
			}
			if violation.Rule != test.wantRule {
				t.Errorf("violation rule = %q, want %q", violation.Rule, test.wantRule)
			}
		})
	}
}
