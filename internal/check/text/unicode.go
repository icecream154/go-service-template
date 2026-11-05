package text

import (
	"unicode"
	"unicode/utf8"

	"go-service-template/internal/check"

	"golang.org/x/text/unicode/norm"
)

const (
	// CharacterUnicodeVersion 是标准库 Unicode 类别、脚本和属性表的版本。
	CharacterUnicodeVersion = unicode.Version
	// NormalizationUnicodeVersion 是规范化检查采用的 Unicode 数据版本。
	NormalizationUnicodeVersion = norm.Version
)

// NormalizationForm 指定需要检查的 Unicode 规范化形式，不执行字符转换。
type NormalizationForm uint8

const (
	NFC NormalizationForm = iota
	NFD
	NFKC
	NFKD
)

// Normalized 检查合法 UTF-8 文本已经满足指定规范化形式，空串合法。
func Normalized(value string, form NormalizationForm) error {
	var normalization norm.Form
	switch form {
	case NFC:
		normalization = norm.NFC
	case NFD:
		normalization = norm.NFD
	case NFKC:
		normalization = norm.NFKC
	case NFKD:
		normalization = norm.NFKD
	default:
		return check.NewRuleError("unicode_normalized", "normalization form must be NFC, NFD, NFKC, or NFKD")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	if !normalization.IsNormalString(value) {
		return check.NewViolation("unicode_normalized", "must already use the required Unicode normalization form")
	}
	return nil
}

// AllowedRunes 检查每个码点都满足显式纯谓词，空串合法，nil 谓词属于规则错误。
func AllowedRunes(value string, allowed func(rune) bool) error {
	if allowed == nil {
		return check.NewRuleError("allowed_runes", "character predicate must not be nil")
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	for _, r := range value {
		if !allowed(r) {
			return check.NewViolation("allowed_runes", "must contain only permitted Unicode characters")
		}
	}
	return nil
}

// RuneSet 是从明确字符集合构建的只读规则，可并发复用；零值表示空集合。
type RuneSet struct {
	runes map[rune]struct{}
}

// NewRuneSet 构建允许字符集合，重复字符合并；非法 UTF-8 定义属于规则错误。
// 空集合允许定义，其 Check 只接受空字符串。
func NewRuneSet(characters string) (*RuneSet, error) {
	if !utf8.ValidString(characters) {
		return nil, check.NewRuleError("rune_set", "character set must be valid UTF-8 text")
	}
	set := &RuneSet{runes: make(map[rune]struct{})}
	for _, r := range characters {
		set.runes[r] = struct{}{}
	}
	return set, nil
}

// Check 检查文本中的每个码点都属于该允许集合，不改写或去重输入。
func (set *RuneSet) Check(value string) error {
	if set == nil {
		return check.NewRuleError("rune_set", "character set must not be nil")
	}
	return AllowedRunes(value, func(r rune) bool {
		_, found := set.runes[r]
		return found
	})
}

// AllowedCategories 检查每个码点至少属于一个指定 Unicode 类别。
// 名称严格采用 unicode.Categories 的键（如 L、Nd）；未知类别属于规则错误。
func AllowedCategories(value string, categories ...string) error {
	return allowedUnicodeTables(value, "unicode_categories", categories, unicode.Categories)
}

// AllowedScripts 检查每个码点至少属于一个指定 Unicode 脚本。
// 名称严格采用 unicode.Scripts 的键（如 Han、Latin）；不隐式允许 Common 或 Inherited。
func AllowedScripts(value string, scripts ...string) error {
	return allowedUnicodeTables(value, "unicode_scripts", scripts, unicode.Scripts)
}

// AllowedProperties 检查每个码点至少满足一个指定的标准库 Unicode 属性。
// 它不声称覆盖标准库表中没有提供的所有 Unicode 或表情属性。
func AllowedProperties(value string, properties ...string) error {
	return allowedUnicodeTables(value, "unicode_properties", properties, unicode.Properties)
}

func allowedUnicodeTables(value, rule string, names []string, registry map[string]*unicode.RangeTable) error {
	tables := make([]*unicode.RangeTable, 0, len(names))
	for _, name := range names {
		table, found := registry[name]
		if !found || table == nil {
			return check.NewRuleError(rule, "all Unicode table names must be known")
		}
		tables = append(tables, table)
	}
	if err := ValidUTF8(value); err != nil {
		return err
	}
	for _, r := range value {
		if !unicode.IsOneOf(tables, r) {
			return check.NewViolation(rule, "must contain only characters in the permitted Unicode tables")
		}
	}
	return nil
}
