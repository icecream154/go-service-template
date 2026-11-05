package text

import (
	"regexp"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// FullPattern 是完整输入匹配规则，编译后只读，可并发复用。
// 零值不可用，应通过 CompileFullPattern 构造。
type FullPattern struct {
	compiled *regexp.Regexp
}

// FragmentPattern 是查找任意匹配片段的规则，编译后只读，可并发复用。
// 零值不可用，应通过 CompileFragmentPattern 构造。
type FragmentPattern struct {
	compiled *regexp.Regexp
}

// CompileFullPattern 编译完整匹配规则，使用绝对文本边界而不受多行标志影响。
// 无效 UTF-8 或正则定义返回规则错误；错误不包含原始模式。
func CompileFullPattern(pattern string) (*FullPattern, error) {
	compiled, err := compileTextPattern("full_pattern", pattern, true)
	if err != nil {
		return nil, err
	}
	return &FullPattern{compiled: compiled}, nil
}

// CompileFragmentPattern 编译片段匹配规则，不为调用方隐式增加文本边界。
func CompileFragmentPattern(pattern string) (*FragmentPattern, error) {
	compiled, err := compileTextPattern("fragment_pattern", pattern, false)
	if err != nil {
		return nil, err
	}
	return &FragmentPattern{compiled: compiled}, nil
}

// Check 检查整个合法 UTF-8 输入符合规则，是否接受空串由模式本身决定。
func (pattern *FullPattern) Check(value string) error {
	if pattern == nil || pattern.compiled == nil {
		return check.NewRuleError("full_pattern", "pattern must be compiled before use")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if !pattern.compiled.MatchString(value) {
		return check.NewViolation("full_pattern", "entire text must match the required pattern")
	}
	return nil
}

// Check 检查合法 UTF-8 输入包含一个匹配片段，不代表整个字段都符合该格式。
func (pattern *FragmentPattern) Check(value string) error {
	if pattern == nil || pattern.compiled == nil {
		return check.NewRuleError("fragment_pattern", "pattern must be compiled before use")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if !pattern.compiled.MatchString(value) {
		return check.NewViolation("fragment_pattern", "text must contain a match for the required pattern")
	}
	return nil
}

func compileTextPattern(rule, pattern string, whole bool) (*regexp.Regexp, error) {
	if !utf8.ValidString(pattern) {
		return nil, check.NewRuleError(rule, "pattern must be valid UTF-8 text")
	}
	// 先验证原始模式，防止未配对括号利用外层包装意外组成合法但未完整锚定的表达式。
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, check.NewRuleError(rule, "pattern must be a valid Go regular expression")
	}
	if whole {
		pattern = `\A(?:` + pattern + `)\z`
		compiled, err = regexp.Compile(pattern)
	}
	if err != nil {
		return nil, check.NewRuleError(rule, "pattern must be a valid Go regular expression")
	}
	return compiled, nil
}
