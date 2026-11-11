package text_test

import (
	"regexp"
	"strings"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/text"
)

type patternCompileCase struct {
	name                 string
	pattern              string
	wantErr              error
	requiresWrapperLimit bool
}

func TestCompileFullPattern(t *testing.T) {
	testPatternCompile(t, text.CompileFullPattern, []patternCompileCase{
		// 汉字与量词构成合法的 Go 正则，应创建完整匹配规则。
		{name: "valid Unicode expression", pattern: "汉+"},
		// 空模式是合法定义，匹配范围由完整规则控制。
		{name: "empty pattern", pattern: ""},
		// 非法字节属于规则定义错误，返回的规则对象应为空。
		{name: "invalid UTF-8", pattern: "\xff", wantErr: check.ErrInvalidRule},
		// 该表达式可与外层括号组成合法正则，原始模式仍应被拒绝。
		{name: "unbalanced original parentheses", pattern: "private-closed)|(?:open", wantErr: check.ErrInvalidRule},
		// 用接近 Go 正则深度上限的模式，验证添加完整匹配边界后编译失败时返回规则错误。
		{name: "full match wrapper exceeds regexp nesting limit", pattern: strings.Repeat("(", 999) + "a" + strings.Repeat(")", 999), wantErr: check.ErrInvalidRule, requiresWrapperLimit: true},
	})
}

func TestCompileFragmentPattern(t *testing.T) {
	testPatternCompile(t, text.CompileFragmentPattern, []patternCompileCase{
		// 用字符类与量词验证合法片段规则可以编译。
		{name: "valid character class", pattern: "[0-9]+"},
		// 空表达式也可以编译成片段规则。
		{name: "empty pattern", pattern: ""},
		// 含非法编码的模式应返回规则定义错误。
		{name: "invalid UTF-8", pattern: "\xff", wantErr: check.ErrInvalidRule},
		// 未关闭的字符类应被判为无效的正则定义。
		{name: "unclosed character class", pattern: "[private-pattern", wantErr: check.ErrInvalidRule},
	})
}

type patternCheckCase struct {
	name    string
	check   func(string) error
	value   string
	wantErr error
}

func TestFullPatternCheck(t *testing.T) {
	alternation := mustCompileFullPattern(t, "甲|乙")
	multiline := mustCompileFullPattern(t, "(?m)^word$")
	empty := mustCompileFullPattern(t, "")
	var nilPattern *text.FullPattern

	testPatternCheck(t, []patternCheckCase{
		// 选中第二个分支时，整个输入仍应通过。
		{name: "complete alternative", check: alternation.Check, value: "乙"},
		// 在第一个分支后加入字符，验证整个交替表达式都受边界限制。
		{name: "partial alternative", check: alternation.Check, value: "甲后", wantErr: check.ErrInvalidValue},
		// 一行内容完整匹配，应允许带多行标志的规则。
		{name: "one complete line", check: multiline.Check, value: "word"},
		// 输入中间一行匹配时，绝对文本边界仍要求整个输入匹配。
		{name: "matching interior line", check: multiline.Check, value: "prefix\nword\nsuffix", wantErr: check.ErrInvalidValue},
		// 空模式可以完整匹配空文本。
		{name: "empty pattern with empty input", check: empty.Check, value: ""},
		// 空模式只消费零字符，完整匹配应拒绝非空输入。
		{name: "empty pattern with nonempty input", check: empty.Check, value: "a", wantErr: check.ErrInvalidValue},
		// 在完整匹配前应检查编码。
		{name: "invalid UTF-8", check: alternation.Check, value: "\xff", wantErr: check.ErrInvalidValue},
		// 零值未编译，应返回规则定义错误。
		{name: "zero value", check: (&text.FullPattern{}).Check, value: "", wantErr: check.ErrInvalidRule},
		// nil 接收者也应返回规则错误，优先于输入检查。
		{name: "nil pattern and invalid input", check: nilPattern.Check, value: "\xff", wantErr: check.ErrInvalidRule},
	})
}

func TestFragmentPatternCheck(t *testing.T) {
	digits := mustCompileFragmentPattern(t, "[0-9]+")
	empty := mustCompileFragmentPattern(t, "")
	var nilPattern *text.FragmentPattern

	testPatternCheck(t, []patternCheckCase{
		// 只在中间放置数字，验证存在匹配片段就能通过。
		{name: "match inside input", check: digits.Check, value: "编号12号"},
		// 使用全角数字验证 ASCII 字符类的具体匹配范围。
		{name: "no matching ASCII digit", check: digits.Check, value: "编号１号", wantErr: check.ErrInvalidValue},
		// 空模式在非空文本中也能找到零长度片段。
		{name: "empty pattern with nonempty input", check: empty.Check, value: "a"},
		// 输入含有匹配数字和非法字节时，应优先拒绝非法编码。
		{name: "match followed by invalid UTF-8", check: digits.Check, value: "1\xff", wantErr: check.ErrInvalidValue},
		// 零值未编译，应返回规则定义错误。
		{name: "zero value", check: (&text.FragmentPattern{}).Check, value: "", wantErr: check.ErrInvalidRule},
		// nil 接收者没有已编译规则，应返回规则错误。
		{name: "nil pattern", check: nilPattern.Check, value: "12", wantErr: check.ErrInvalidRule},
	})
}

func testPatternCompile[P any](t *testing.T, compile func(string) (*P, error), tests []patternCompileCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.requiresWrapperLimit {
				// 深度限制取决于 Go 版本，先确认该输入只在包上完整匹配边界后触发限制。
				if _, err := regexp.Compile(test.pattern); err != nil {
					t.Skip("current Go regexp rejects the unwrapped pattern")
				}
				if _, err := regexp.Compile("\\A(?:" + test.pattern + ")\\z"); err == nil {
					t.Skip("current Go regexp also accepts the wrapped pattern")
				}
			}
			pattern, err := compile(test.pattern)
			assertTextError(t, err, test.wantErr)
			if (pattern == nil) != (test.wantErr != nil) {
				t.Errorf("compiled pattern is nil = %t, want %t", pattern == nil, test.wantErr != nil)
			}
			if err != nil && strings.Contains(err.Error(), test.pattern) {
				t.Error("compilation error contains the original pattern")
			}
		})
	}
}

func testPatternCheck(t *testing.T, tests []patternCheckCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, test.check(test.value), test.wantErr)
		})
	}
}

func mustCompileFullPattern(t *testing.T, pattern string) *text.FullPattern {
	t.Helper()
	compiled, err := text.CompileFullPattern(pattern)
	assertTextError(t, err, nil)
	return compiled
}

func mustCompileFragmentPattern(t *testing.T, pattern string) *text.FragmentPattern {
	t.Helper()
	compiled, err := text.CompileFragmentPattern(pattern)
	assertTextError(t, err, nil)
	return compiled
}
