package text_test

import (
	"testing"
	"unicode"

	"go-service-template/internal/check"
	"go-service-template/internal/check/text"
)

func TestNormalized(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		form    text.NormalizationForm
		wantErr error
	}{
		// 空串已满足 NFC，应通过。
		{name: "empty NFC", value: "", form: text.NFC},
		// 预组字符 é 满足 NFC，验证接受合成形式。
		{name: "composed NFC", value: "\u00e9", form: text.NFC},
		// e 加组合重音已分解，应在 NFC 检查中被拒绝。
		{name: "decomposed text in NFC", value: "e\u0301", form: text.NFC, wantErr: check.ErrInvalidValue},
		// 分解形式 e 加重音已满足 NFD。
		{name: "decomposed NFD", value: "e\u0301", form: text.NFD},
		// 预组字符 é 仍可分解，应被 NFD 检查拒绝。
		{name: "composed text in NFD", value: "\u00e9", form: text.NFD, wantErr: check.ErrInvalidValue},
		// fi 是连字 ﬁ 的兼容规范形式，应通过 NFKC 检查。
		{name: "expanded NFKC", value: "fi", form: text.NFKC},
		// 连字 ﬁ 应作兼容展开，原文本应被 NFKC 拒绝。
		{name: "compatibility ligature in NFKC", value: "\ufb01", form: text.NFKC, wantErr: check.ErrInvalidValue},
		// 全角 A 加重音的兼容分解结果为 ASCII A 加重音，应通过 NFKD。
		{name: "expanded and decomposed NFKD", value: "A\u0301", form: text.NFKD},
		// 预组字符 Á 仍可分解，应被 NFKD 拒绝。
		{name: "composed text in NFKD", value: "\u00c1", form: text.NFKD, wantErr: check.ErrInvalidValue},
		// 已知规范形式搭配非法字节，应报告输入错误。
		{name: "invalid UTF-8", value: "\xff", form: text.NFC, wantErr: check.ErrInvalidValue},
		// 未知形式与非法编码同时出现时，应优先报告规则定义错误。
		{name: "invalid form and input", value: "\xff", form: text.NormalizationForm(255), wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, text.Normalized(test.value, test.form), test.wantErr)
		})
	}
}

func TestAllowedRunes(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		allowed func(rune) bool
		wantErr error
	}{
		// 让谓词拒绝每个码点，空文本仍满足逐码点约束。
		{name: "empty text", value: "", allowed: rejectEveryRune},
		// 用多字节汉字和 ASCII 字母验证逐码点应用 Unicode 字母谓词。
		{name: "allowed Unicode letters", value: "汉A", allowed: unicode.IsLetter},
		// 将数字放在合法汉字后面，验证任一不满足谓词的码点都导致失败。
		{name: "disallowed rune after allowed rune", value: "汉1", allowed: unicode.IsLetter, wantErr: check.ErrInvalidValue},
		// 允许每个码点的谓词也应收到合法编码的输入。
		{name: "invalid UTF-8", value: "\xff", allowed: allowEveryRune, wantErr: check.ErrInvalidValue},
		// 谓词缺失时空输入也应报告规则错误。
		{name: "nil predicate", value: "", wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, text.AllowedRunes(test.value, test.allowed), test.wantErr)
		})
	}
}

func TestNewRuneSet(t *testing.T) {
	tests := []struct {
		name       string
		characters string
		wantErr    error
	}{
		// 空字符集是合法定义，应返回可用的规则对象。
		{name: "empty definition", characters: ""},
		// 重复汉字与 ASCII 字符可以一起构造规则。
		{name: "duplicate Unicode characters", characters: "汉a汉"},
		// 规则定义含有非法编码时应返回规则错误。
		{name: "invalid UTF-8 definition", characters: "\xff", wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, err := text.NewRuneSet(test.characters)
			assertTextError(t, err, test.wantErr)
			if (set == nil) != (test.wantErr != nil) {
				t.Errorf("RuneSet is nil = %t, want %t", set == nil, test.wantErr != nil)
			}
		})
	}
}

func TestRuneSetCheck(t *testing.T) {
	set := mustNewRuneSet(t, "汉a汉")
	emptySet := mustNewRuneSet(t, "")
	tests := []struct {
		name    string
		set     *text.RuneSet
		value   string
		wantErr error
	}{
		// 输入顺序不同且重复使用汉字，验证集合只检查成员资格。
		{name: "allowed characters in any order", set: set, value: "a汉汉"},
		// 将非法成员放在合法成员之后，验证检查覆盖全部码点。
		{name: "character outside set", set: set, value: "汉b", wantErr: check.ErrInvalidValue},
		// 空集合接受空输入。
		{name: "empty set with empty input", set: emptySet, value: ""},
		// 空集合没有可匹配的字符，应拒绝非空输入。
		{name: "empty set with nonempty input", set: emptySet, value: "a", wantErr: check.ErrInvalidValue},
		// 零值表示空集合，应接受空输入。
		{name: "zero value with empty input", set: &text.RuneSet{}, value: ""},
		// 零值集合也应拒绝非空输入。
		{name: "zero value with nonempty input", set: &text.RuneSet{}, value: "a", wantErr: check.ErrInvalidValue},
		// 成员检查仍要求输入编码有效。
		{name: "invalid UTF-8 input", set: set, value: "\xff", wantErr: check.ErrInvalidValue},
		// nil 接收者缺少规则定义，应返回规则错误。
		{name: "nil set", value: "", wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, test.set.Check(test.value), test.wantErr)
		})
	}
}

type unicodeTableCase struct {
	name    string
	value   string
	tables  []string
	wantErr error
}

func TestAllowedCategories(t *testing.T) {
	testUnicodeTables(t, text.AllowedCategories, []unicodeTableCase{
		// 同时用汉字、ASCII 字母和阿拉伯数字，验证 L 与 Nd 形成允许并集。
		{name: "union of categories", value: "汉A١", tables: []string{"L", "Nd"}},
		// 标点不在字母类别中，应被拒绝。
		{name: "punctuation outside letters", value: "A!", tables: []string{"L"}, wantErr: check.ErrInvalidValue},
		// 空允许集合对空输入没有待检查的码点，应通过。
		{name: "empty input and category list"},
		// 空允许集合无法匹配非空输入，应报告输入错误。
		{name: "no categories for a letter", value: "A", wantErr: check.ErrInvalidValue},
		// 已知类别后追加小写别名，验证规则会检查每个类别名称。
		{name: "unknown category after known category", value: "A", tables: []string{"L", "nd"}, wantErr: check.ErrInvalidRule},
		// 类别有效时，非法输入编码应作为输入错误返回。
		{name: "invalid UTF-8 input", value: "\xff", tables: []string{"L"}, wantErr: check.ErrInvalidValue},
	})
}

func TestAllowedScripts(t *testing.T) {
	testUnicodeTables(t, text.AllowedScripts, []unicodeTableCase{
		// 将 Han 和 Latin 一起提供，验证两种脚本的字符均可通过。
		{name: "union of scripts", value: "汉A", tables: []string{"Han", "Latin"}},
		// 标点属于 Common，只提供 Latin 时应拒绝。
		{name: "common script omitted", value: "A.", tables: []string{"Latin"}, wantErr: check.ErrInvalidValue},
		// 组合重音属于 Inherited，只提供 Latin 时应拒绝。
		{name: "inherited script omitted", value: "A\u0301", tables: []string{"Latin"}, wantErr: check.ErrInvalidValue},
		// 明确允许 Common 和 Inherited 后，标点和组合重音都可通过。
		{name: "explicit common and inherited scripts", value: "A.\u0301", tables: []string{"Latin", "Common", "Inherited"}},
		// 规则名和输入都非法时，脚本定义错误应优先报告。
		{name: "unknown script and invalid input", value: "\xff", tables: []string{"latin"}, wantErr: check.ErrInvalidRule},
	})
}

func TestAllowedProperties(t *testing.T) {
	testUnicodeTables(t, text.AllowedProperties, []unicodeTableCase{
		// ASCII 十六进制字符和制表符来自不同属性，允许并集应接受两者。
		{name: "union of properties", value: "0aF\t", tables: []string{"ASCII_Hex_Digit", "White_Space"}},
		// 全角 A 验证 ASCII 十六进制属性要求原始码点为 ASCII。
		{name: "fullwidth letter outside ASCII hex digits", value: "Ａ", tables: []string{"ASCII_Hex_Digit"}, wantErr: check.ErrInvalidValue},
		// 对空文本也应检查属性名是否存在。
		{name: "unknown property with empty input", tables: []string{"No_Such_Property"}, wantErr: check.ErrInvalidRule},
	})
}

func rejectEveryRune(rune) bool { return false }

func allowEveryRune(rune) bool { return true }

func mustNewRuneSet(t *testing.T, characters string) *text.RuneSet {
	t.Helper()
	set, err := text.NewRuneSet(characters)
	assertTextError(t, err, nil)
	return set
}

func testUnicodeTables(t *testing.T, validate func(string, ...string) error, tests []unicodeTableCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, validate(test.value, test.tables...), test.wantErr)
		})
	}
}
