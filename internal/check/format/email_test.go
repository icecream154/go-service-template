package format_test

import (
	"errors"
	"net/mail"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

type emailCase struct {
	name    string
	input   string
	want    mail.Address
	wantErr error
}

func TestParseEmailAddress(t *testing.T) {
	testEmailParse(t, format.ParseEmailAddress, []emailCase{
		// 带显示名的地址用于验证名称与邮箱分别解析。
		{name: "display name", input: "Example Person <Person@Example.COM>", want: mail.Address{Name: "Example Person", Address: "Person@Example.COM"}},
		// 单个裸地址验证无需显示名，域名大小写保持原样。
		{name: "bare address", input: "Person@Example.COM", want: mail.Address{Address: "Person@Example.COM"}},
		// 在地址前追加非 UTF-8 字节，验证文本编码错误。
		{name: "invalid UTF-8", input: "\xffuser@example.com", wantErr: check.ErrInvalidValue},
		// 用折行形式验证只接受单行地址文本。
		{name: "line break", input: "Example\r\n <user@example.com>", wantErr: check.ErrInvalidValue},
		// 两个逗号分隔的有效地址验证只接受单个地址。
		{name: "address list", input: "a@example.com, b@example.com", wantErr: check.ErrInvalidValue},
	})
}

func TestParseMailbox(t *testing.T) {
	testEmailParse(t, format.ParseMailbox, []emailCase{
		// 大写本地部分验证裸邮箱不会被统一转成小写。
		{name: "bare mailbox", input: "User@Example.COM", want: mail.Address{Address: "User@Example.COM"}},
		// 引号内带空格的本地部分验证裸地址允许合法的引用语法。
		{name: "quoted local part", input: `"a b"@example.com`, want: mail.Address{Address: "a b@example.com"}},
		// 转义引号验证引用状态延续到本地部分末尾。
		{name: "escaped quote", input: `"a\"b c"@example.com`, want: mail.Address{Address: `a"b c@example.com`}},
		// 带显示名的合法邮件地址验证裸邮箱只接受地址部分。
		{name: "display name", input: "Example Person <user@example.com>", wantErr: check.ErrInvalidValue},
		// 仅用尖括号包裹地址验证即使没有显示名也会拒绝。
		{name: "angle brackets", input: "<user@example.com>", wantErr: check.ErrInvalidValue},
		// 地址末尾附加注释，验证裸邮箱拒绝额外内容。
		{name: "comment", input: "user@example.com (comment)", wantErr: check.ErrInvalidValue},
		// 前导空白验证裸邮箱要求原始文本精确匹配地址语法。
		{name: "surrounding whitespace", input: " user@example.com", wantErr: check.ErrInvalidValue},
		// 缺少域的文本验证原始解析错误沿裸邮箱入口返回。
		{name: "missing domain", input: "user@", wantErr: check.ErrInvalidValue},
	})
}

func TestMailboxDomainOneOf(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		allowed []string
		wantErr error
	}{
		// 匹配项在允许列表第二位，域大小写不同仍应通过。
		{name: "case insensitive match", value: "User@EXAMPLE.COM", allowed: []string{"other.example", "example.com"}},
		// 引用本地部分内有 @，验证比较使用最终分隔符后的域。
		{name: "quoted at sign", value: `"a@b"@example.com`, allowed: []string{"example.com"}},
		// 允许父域时子域仍需独立列出，验证精确域匹配。
		{name: "subdomain not listed", value: "user@sub.example.com", allowed: []string{"example.com"}, wantErr: check.ErrInvalidValue},
		// 有效地址配空列表验证缺少允许域属于规则错误。
		{name: "no allowed domains", value: "user@example.com", wantErr: check.ErrInvalidRule},
		// 空域元素验证列表中的每一项都需要有效内容。
		{name: "empty allowed domain", value: "user@example.com", allowed: []string{""}, wantErr: check.ErrInvalidRule},
		// 不完整的 UTF-8 字节验证域规则拒绝无效文本。
		{name: "non UTF-8 domain", value: "user@example.com", allowed: []string{"\xff"}, wantErr: check.ErrInvalidRule},
		// 把整个地址配置为域验证规则拒绝 @ 分隔符。
		{name: "address used as domain", value: "user@example.com", allowed: []string{"user@example.com"}, wantErr: check.ErrInvalidRule},
		// 域内空格验证规则拒绝空白字符。
		{name: "space in domain", value: "user@example.com", allowed: []string{"example .com"}, wantErr: check.ErrInvalidRule},
		// 无空白的连续点验证规则还需要满足邮箱域语法。
		{name: "malformed domain syntax", value: "user@example.com", allowed: []string{"example..com"}, wantErr: check.ErrInvalidRule},
		// 规则有效时，缺失地址域应报告输入值错误。
		{name: "invalid mailbox", value: "user@", allowed: []string{"example.com"}, wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertEmailError(t, format.MailboxDomainOneOf(test.value, test.allowed...), test.wantErr)
		})
	}
}

func testEmailParse(t *testing.T, parse func(string) (*mail.Address, error), tests []emailCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input)
			assertEmailError(t, err, test.wantErr)
			if test.wantErr == nil && (got == nil || *got != test.want) {
				t.Errorf("parse(%q) = %+v, want %+v", test.input, got, test.want)
			}
		})
	}
}

func assertEmailError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
