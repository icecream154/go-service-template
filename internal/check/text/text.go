package text

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// ValidUTF8 检查字节序列是否为合法 UTF-8，空字符串合法。
func ValidUTF8(value string) error {
	if !utf8.ValidString(value) {
		return check.NewViolation("valid_utf8", "must be valid UTF-8 text")
	}
	return nil
}

// Empty 只检查字符串是否为空，不推断空白或缺失语义。
func Empty(value string) error {
	if value != "" {
		return check.NewViolation("empty", "must be empty")
	}
	return nil
}

// NotEmpty 只检查原始字节长度大于零；需要合法编码时另行组合 ValidUTF8。
func NotEmpty(value string) error {
	if value == "" {
		return check.NewViolation("not_empty", "must not be empty")
	}
	return nil
}

// NotBlank 拒绝空串和仅含 Unicode 空白的文本，不 trim 或改写原值。
func NotBlank(value string) error {
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return check.NewViolation("not_blank", "must not be blank")
	}
	return nil
}

// NoSurroundingWhitespace 拒绝首尾 Unicode 空白；空串合法，内部空白不受影响。
func NoSurroundingWhitespace(value string) error {
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if strings.TrimSpace(value) != value {
		return check.NewViolation("no_surrounding_whitespace", "must not have surrounding whitespace")
	}
	return nil
}

// ASCII 检查所有字节都属于 ASCII（0..127），不额外限制控制字符；空串合法。
func ASCII(value string) error {
	for i := 0; i < len(value); i++ {
		if value[i] >= utf8.RuneSelf {
			return check.NewViolation("ascii", "must contain only ASCII characters")
		}
	}
	return nil
}

// NoControlCharacters 拒绝 Unicode Cc 控制字符（包括制表和换行），空串合法。
// 它不将 Cf 格式字符、所有不可见字符或表情一概视为控制字符。
func NoControlCharacters(value string) error {
	if err := ValidUTF8(value); err != nil {
		return err
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return check.NewViolation("no_control_characters", "must not contain Unicode control characters")
		}
	}
	return nil
}

// StartsWith 检查原值具有指定前缀；空前缀允许，双方必须是合法 UTF-8。
func StartsWith(value, prefix string) error {
	if !utf8.ValidString(prefix) {
		return check.NewRuleError("starts_with", "prefix must be valid UTF-8 text")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if !strings.HasPrefix(value, prefix) {
		return check.NewViolation("starts_with", "must have the required prefix")
	}
	return nil
}

// EndsWith 检查原值具有指定后缀；空后缀允许，双方必须是合法 UTF-8。
func EndsWith(value, suffix string) error {
	if !utf8.ValidString(suffix) {
		return check.NewRuleError("ends_with", "suffix must be valid UTF-8 text")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if !strings.HasSuffix(value, suffix) {
		return check.NewViolation("ends_with", "must have the required suffix")
	}
	return nil
}

// ContainsSubstring 检查原值包含指定字面文本，不解释正则；空片段允许。
func ContainsSubstring(value, fragment string) error {
	if !utf8.ValidString(fragment) {
		return check.NewRuleError("contains_substring", "fragment must be valid UTF-8 text")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if !strings.Contains(value, fragment) {
		return check.NewViolation("contains_substring", "must contain the required text")
	}
	return nil
}
