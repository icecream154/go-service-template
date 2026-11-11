package text_test

import (
	"errors"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/text"
)

type textValueCase struct {
	name    string
	value   string
	wantErr error
}

func TestValidUTF8(t *testing.T) {
	testTextValue(t, text.ValidUTF8, []textValueCase{
		// 空串没有非法字节，应通过编码检查。
		{name: "empty text", value: ""},
		// 用汉字和表情验证合法的多字节序列可以通过。
		{name: "multibyte text", value: "汉😀"},
		// 显式编码的替换字符也是合法码点，应通过。
		{name: "encoded replacement rune", value: "\ufffd"},
		// 缺少续字节的开头字节应被识别为输入错误。
		{name: "truncated sequence", value: "\xe4", wantErr: check.ErrInvalidValue},
		// 单独的 0xff 字节应被识别为输入错误。
		{name: "invalid byte", value: "\xff", wantErr: check.ErrInvalidValue},
	})
}

func TestEmpty(t *testing.T) {
	testTextValue(t, text.Empty, []textValueCase{
		// 零字节输入满足空串约束。
		{name: "empty text", value: ""},
		// 单个空格已有一个字节，应被判为非空。
		{name: "space has bytes", value: " ", wantErr: check.ErrInvalidValue},
		// 一个非法编码字节同样使原始字符串非空。
		{name: "invalid byte has length", value: "\xff", wantErr: check.ErrInvalidValue},
	})
}

func TestNotEmpty(t *testing.T) {
	testTextValue(t, text.NotEmpty, []textValueCase{
		// 空串长度为零，应被非空约束拒绝。
		{name: "empty text", value: "", wantErr: check.ErrInvalidValue},
		// 一个制表符也有字节长度，应通过非空检查。
		{name: "whitespace has length", value: "\t"},
		// 一个原始非法编码字节已有长度，应通过字节层面的非空检查。
		{name: "invalid byte has length", value: "\xff"},
	})
}

func TestNotBlank(t *testing.T) {
	testTextValue(t, text.NotBlank, []textValueCase{
		// 空串没有任何非空白码点，应被拒绝。
		{name: "empty text", value: "", wantErr: check.ErrInvalidValue},
		// 混合 ASCII 空白和全角空格，验证 Unicode 空白也参与判断。
		{name: "only Unicode whitespace", value: "\t\u3000\n", wantErr: check.ErrInvalidValue},
		// 首尾空白中的汉字仍使整个输入符合非空白约束。
		{name: "surrounded content", value: "\u3000汉\t"},
		// 原始字节非法时，应返回编码检查的输入错误。
		{name: "invalid UTF-8", value: "a\xff", wantErr: check.ErrInvalidValue},
	})
}

func TestNoSurroundingWhitespace(t *testing.T) {
	testTextValue(t, text.NoSurroundingWhitespace, []textValueCase{
		// 空串没有首尾空白，应通过。
		{name: "empty text", value: ""},
		// 只在内容内部放置 Unicode 空白，验证内部空白可以保留。
		{name: "interior Unicode whitespace", value: "甲\u3000乙"},
		// 用全角空格放在开头，验证 Unicode 前导空白会被拒绝。
		{name: "leading Unicode whitespace", value: "\u3000甲", wantErr: check.ErrInvalidValue},
		// 末尾的 Unicode 不换行空格也属于首尾空白。
		{name: "trailing Unicode whitespace", value: "甲\u00a0", wantErr: check.ErrInvalidValue},
		// 非法编码应在检查文本边界前被拒绝。
		{name: "invalid UTF-8", value: "\xff", wantErr: check.ErrInvalidValue},
	})
}

func TestASCII(t *testing.T) {
	testTextValue(t, text.ASCII, []textValueCase{
		// 空输入的所有字节都满足 ASCII 约束。
		{name: "empty text", value: ""},
		// 同时使用 0x00、0x7f 和普通字母，验证 ASCII 两端及控制字节都允许。
		{name: "inclusive ASCII byte bounds", value: "\x00A\x7f"},
		// 紧邻 ASCII 上限的 0x80 应被拒绝。
		{name: "first byte outside ASCII", value: "\x80", wantErr: check.ErrInvalidValue},
		// 合法 UTF-8 汉字仍包含超出 ASCII 范围的字节。
		{name: "multibyte text", value: "A汉", wantErr: check.ErrInvalidValue},
	})
}

func TestNoControlCharacters(t *testing.T) {
	testTextValue(t, text.NoControlCharacters, []textValueCase{
		// 空串没有控制字符，应通过。
		{name: "empty text", value: ""},
		// 用带零宽连接符的表情验证 Unicode 格式字符 Cf 可以通过。
		{name: "format character in emoji", value: "👩‍💻"},
		// 制表符属于 Cc 控制字符，应被拒绝。
		{name: "ASCII control character", value: "a\tb", wantErr: check.ErrInvalidValue},
		// U+0085 验证非 ASCII 的 Cc 控制字符也会被拒绝。
		{name: "Unicode control character", value: "a\u0085b", wantErr: check.ErrInvalidValue},
		// 用非法编码验证检查先确认输入是合法 UTF-8。
		{name: "invalid UTF-8", value: "\xff", wantErr: check.ErrInvalidValue},
	})
}

type textPartCase struct {
	name    string
	value   string
	part    string
	wantErr error
}

func TestStartsWith(t *testing.T) {
	testTextPart(t, text.StartsWith, []textPartCase{
		// 用开头的汉字验证按原始 Unicode 文本匹配前缀。
		{name: "matching prefix", value: "甲乙", part: "甲"},
		// 空前缀可以匹配空文本。
		{name: "empty prefix and input", value: "", part: ""},
		// 相同片段出现在末尾时，应被前缀检查拒绝。
		{name: "match at wrong position", value: "乙甲", part: "甲", wantErr: check.ErrInvalidValue},
		// 只改变字母大小写，验证前缀匹配保留大小写。
		{name: "different case", value: "Ab", part: "a", wantErr: check.ErrInvalidValue},
		// 即使前缀为空，非法 UTF-8 输入也应被拒绝。
		{name: "invalid input with empty prefix", value: "\xff", part: "", wantErr: check.ErrInvalidValue},
		// 输入和前缀都非法时，验证前缀定义错误优先报告。
		{name: "invalid prefix and input", value: "\xff", part: "\xff", wantErr: check.ErrInvalidRule},
	})
}

func TestEndsWith(t *testing.T) {
	testTextPart(t, text.EndsWith, []textPartCase{
		// 用末尾的汉字验证按原始 Unicode 文本匹配后缀。
		{name: "matching suffix", value: "甲乙", part: "乙"},
		// 空后缀允许用于普通文本。
		{name: "empty suffix", value: "甲乙", part: ""},
		// 相同片段出现在开头时，应被后缀检查拒绝。
		{name: "match at wrong position", value: "乙甲", part: "乙", wantErr: check.ErrInvalidValue},
		// 只改变后缀的字母大小写，验证匹配保留大小写。
		{name: "different case", value: "aB", part: "b", wantErr: check.ErrInvalidValue},
		// 即使后缀为空，非法 UTF-8 输入也应被拒绝。
		{name: "invalid input with empty suffix", value: "\xff", part: "", wantErr: check.ErrInvalidValue},
		// 输入和后缀都非法时，验证后缀定义错误优先报告。
		{name: "invalid suffix and input", value: "\xff", part: "\xff", wantErr: check.ErrInvalidRule},
	})
}

func TestContainsSubstring(t *testing.T) {
	testTextPart(t, text.ContainsSubstring, []textPartCase{
		// 用包含点和星号的片段验证按字面匹配。
		{name: "literal metacharacters", value: "甲a.*乙", part: "a.*"},
		// 正则会匹配这个输入，字面片段检查应拒绝。
		{name: "pattern text is literal", value: "abc", part: "a.*", wantErr: check.ErrInvalidValue},
		// 空片段可以匹配空文本。
		{name: "empty fragment and input", value: "", part: ""},
		// 片段为空也要求原始输入是合法 UTF-8。
		{name: "invalid input with empty fragment", value: "\xff", part: "", wantErr: check.ErrInvalidValue},
		// 输入和片段都非法时，验证片段定义错误优先报告。
		{name: "invalid fragment and input", value: "\xff", part: "\xff", wantErr: check.ErrInvalidRule},
	})
}

func testTextValue(t *testing.T, validate func(string) error, tests []textValueCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, validate(test.value), test.wantErr)
		})
	}
}

func testTextPart(t *testing.T, validate func(string, string) error, tests []textPartCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, validate(test.value, test.part), test.wantErr)
		})
	}
}

func assertTextError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
