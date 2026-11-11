package text_test

import (
	"math"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/text"
)

func TestByteCount(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		// 空串的原始字节数应为零。
		{name: "empty text", value: "", want: 0},
		// 用一、三、四字节码点验证字节数与码点数的差异。
		{name: "mixed UTF-8 widths", value: "a汉😀", want: 8},
		// 非法 UTF-8 字节仍计入原始长度。
		{name: "invalid UTF-8 byte", value: "\xffa", want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := text.ByteCount(test.value); got != test.want {
				t.Errorf("ByteCount(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

type textCountCase struct {
	name    string
	value   string
	want    int
	wantErr error
}

func TestRuneCount(t *testing.T) {
	testTextCount(t, text.RuneCount, []textCountCase{
		// 空串没有码点，返回零。
		{name: "empty text", value: "", want: 0},
		// 用不同 UTF-8 字节宽度的字母、汉字和表情验证各计一个码点。
		{name: "mixed UTF-8 widths", value: "a汉😀", want: 3},
		// e 和组合重音各计一个码点，计数应为二。
		{name: "combining mark", value: "e\u0301", want: 2},
		// 非法字节应返回输入错误。
		{name: "invalid UTF-8", value: "\xff", wantErr: check.ErrInvalidValue},
	})
}

func TestGraphemeCount(t *testing.T) {
	testTextCount(t, text.GraphemeCount, []textCountCase{
		// 空串没有扩展字素簇，返回零。
		{name: "empty text", value: "", want: 0},
		// 基本字符和组合重音应合成一个字素簇。
		{name: "combining mark", value: "e\u0301", want: 1},
		// 零宽连接符组成的表情与后面的汉字应各算一个字素簇。
		{name: "joined emoji followed by Han character", value: "👩‍💻汉", want: 2},
		// 两个区域指示符组成的旗帜应算一个字素簇。
		{name: "regional indicator pair", value: "🇨🇳", want: 1},
		// 非法字节应在字素分段前被拒绝。
		{name: "invalid UTF-8", value: "\xff", wantErr: check.ErrInvalidValue},
	})
}

func TestWeightedLength(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		weight  func(rune) int
		want    int
		wantErr error
	}{
		// 空输入使用合法权重函数，累加结果应为零。
		{name: "empty text", value: "", weight: sampleRuneWeight, want: 0},
		// 字母权重一、汉字权重三、表情权重零，验证按码点求和。
		{name: "mixed rune weights", value: "a汉😀", weight: sampleRuneWeight, want: 4},
		// 单个码点的权重恰好为最大 int 时，仍可返回该值。
		{name: "maximum representable weight", value: "a", weight: maxRuneWeight, want: math.MaxInt},
		// 第二个最大权重使总和越界，应返回输入错误。
		{name: "sum overflows int", value: "ab", weight: maxRuneWeight, wantErr: check.ErrInvalidValue},
		// 零长度也要求权重规则已定义。
		{name: "nil weight with empty text", value: "", wantErr: check.ErrInvalidRule},
		// 非法编码应在按码点取权重前被拒绝。
		{name: "invalid UTF-8", value: "\xff", weight: sampleRuneWeight, wantErr: check.ErrInvalidValue},
		// 返回负值的权重函数违反规则约定。
		{name: "negative rune weight", value: "a", weight: negativeRuneWeight, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := text.WeightedLength(test.value, test.weight)
			assertTextError(t, err, test.wantErr)
			if test.wantErr == nil && got != test.want {
				t.Errorf("WeightedLength(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

type textCountRangeCase struct {
	name    string
	value   string
	minimum int
	maximum int
	wantErr error
}

func TestByteCountInRange(t *testing.T) {
	testTextCountRange(t, text.ByteCountInRange, []textCountRangeCase{
		// 零字节应落在零宽闭区间内。
		{name: "empty text on both endpoints", value: "", minimum: 0, maximum: 0},
		// 汉字占三个字节，恰好命中下限。
		{name: "lower endpoint", value: "汉", minimum: 3, maximum: 4},
		// 汉字加字母共四字节，恰好命中上限。
		{name: "upper endpoint", value: "汉a", minimum: 3, maximum: 4},
		// 两个 ASCII 字节刚低于下限，应返回输入错误。
		{name: "below minimum", value: "ab", minimum: 3, maximum: 4, wantErr: check.ErrInvalidValue},
		// 一个三字节汉字刚超出上限，应返回输入错误。
		{name: "above maximum", value: "汉", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 一个非法 UTF-8 字节仍满足一字节约束。
		{name: "invalid UTF-8 still counts as bytes", value: "\xff", minimum: 1, maximum: 1},
		// 负下限违反长度规则，应报告规则错误。
		{name: "negative minimum", value: "a", minimum: -1, maximum: 1, wantErr: check.ErrInvalidRule},
		// 上限小于下限时，规则自身无效。
		{name: "reversed bounds", value: "a", minimum: 2, maximum: 1, wantErr: check.ErrInvalidRule},
	})
}

type textCountLimitCase struct {
	name    string
	value   string
	bound   int
	wantErr error
}

func TestByteCountAtLeast(t *testing.T) {
	testTextCountLimit(t, text.ByteCountAtLeast, []textCountLimitCase{
		// 汉字的三个字节恰好达到下限。
		{name: "at minimum", value: "汉", bound: 3},
		// 下限比汉字长度大一字节，应返回输入错误。
		{name: "below minimum", value: "汉", bound: 4, wantErr: check.ErrInvalidValue},
		// 负下限即使配空文本也应报告规则错误。
		{name: "negative minimum", value: "", bound: -1, wantErr: check.ErrInvalidRule},
	})
}

func TestByteCountAtMost(t *testing.T) {
	testTextCountLimit(t, text.ByteCountAtMost, []textCountLimitCase{
		// 汉字长度恰好是上限，应通过。
		{name: "at maximum", value: "汉", bound: 3},
		// 上限比汉字长度少一字节，应返回输入错误。
		{name: "above maximum", value: "汉", bound: 2, wantErr: check.ErrInvalidValue},
		// 负上限违反非负长度规则。
		{name: "negative maximum", value: "", bound: -1, wantErr: check.ErrInvalidRule},
	})
}

func TestRuneCountInRange(t *testing.T) {
	testTextCountRange(t, text.RuneCountInRange, []textCountRangeCase{
		// 三字节汉字只占一个码点，应命中下限。
		{name: "lower endpoint", value: "汉", minimum: 1, maximum: 2},
		// e 和组合重音各算一个码点，应命中上限。
		{name: "upper endpoint", value: "e\u0301", minimum: 1, maximum: 2},
		// 空串的零个码点低于正下限。
		{name: "below minimum", value: "", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 三个码点超过两个码点的上限。
		{name: "above maximum", value: "a汉😀", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 合法规则配非法 UTF-8，应报告输入错误。
		{name: "invalid UTF-8", value: "\xff", minimum: 0, maximum: 2, wantErr: check.ErrInvalidValue},
		// 反向区间和非法编码同时出现时，应优先报告规则错误。
		{name: "reversed bounds and invalid UTF-8", value: "\xff", minimum: 2, maximum: 1, wantErr: check.ErrInvalidRule},
	})
}

func TestRuneCountAtLeast(t *testing.T) {
	testTextCountLimit(t, text.RuneCountAtLeast, []textCountLimitCase{
		// 汉字和表情各算一个码点，两个码点满足边界。
		{name: "at minimum", value: "汉😀", bound: 2},
		// 三字节汉字只占一个码点，低于两个码点的下限。
		{name: "below minimum", value: "汉", bound: 2, wantErr: check.ErrInvalidValue},
		// 负下限使规则无效。
		{name: "negative minimum", value: "", bound: -1, wantErr: check.ErrInvalidRule},
	})
}

func TestRuneCountAtMost(t *testing.T) {
	testTextCountLimit(t, text.RuneCountAtMost, []textCountLimitCase{
		// 汉字只算一个码点，可用尽上限。
		{name: "at maximum", value: "汉", bound: 1},
		// e 和组合重音是两个码点，超过一位上限。
		{name: "above maximum", value: "e\u0301", bound: 1, wantErr: check.ErrInvalidValue},
		// 非法字节无法作为有效码点计数，应报告输入错误。
		{name: "invalid UTF-8", value: "\xff", bound: 1, wantErr: check.ErrInvalidValue},
	})
}

func TestGraphemeCountInRange(t *testing.T) {
	testTextCountRange(t, text.GraphemeCountInRange, []textCountRangeCase{
		// 带组合重音的字符形成一个字素簇，应命中下限。
		{name: "lower endpoint", value: "e\u0301", minimum: 1, maximum: 2},
		// 旗帜和汉字各算一个字素簇，应命中上限。
		{name: "upper endpoint", value: "🇨🇳汉", minimum: 1, maximum: 2},
		// 空串没有字素簇，低于正下限。
		{name: "below minimum", value: "", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 三个分开的汉字形成三个字素簇，超过两位上限。
		{name: "above maximum", value: "甲乙丙", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 合法规则配非法 UTF-8，应报告输入错误。
		{name: "invalid UTF-8", value: "\xff", minimum: 0, maximum: 2, wantErr: check.ErrInvalidValue},
		// 负下限和非法编码同时出现时，应优先报告规则错误。
		{name: "negative minimum and invalid UTF-8", value: "\xff", minimum: -1, maximum: 1, wantErr: check.ErrInvalidRule},
	})
}

func TestGraphemeCountAtLeast(t *testing.T) {
	testTextCountLimit(t, text.GraphemeCountAtLeast, []textCountLimitCase{
		// 两个组合字符应计为两个字素簇并命中下限。
		{name: "at minimum", value: "e\u0301a\u0308", bound: 2},
		// 一个连接表情仍只形成一个字素簇，低于下限。
		{name: "below minimum", value: "👩‍💻", bound: 2, wantErr: check.ErrInvalidValue},
		// 负下限应报告规则错误。
		{name: "negative minimum", value: "", bound: -1, wantErr: check.ErrInvalidRule},
	})
}

func TestGraphemeCountAtMost(t *testing.T) {
	testTextCountLimit(t, text.GraphemeCountAtMost, []textCountLimitCase{
		// 由多个码点组成的旗帜仍满足一位字素簇上限。
		{name: "at maximum", value: "🇨🇳", bound: 1},
		// 追加一个汉字形成第二个字素簇，超过上限。
		{name: "above maximum", value: "🇨🇳汉", bound: 1, wantErr: check.ErrInvalidValue},
		// 非法 UTF-8 应报告输入错误。
		{name: "invalid UTF-8", value: "\xff", bound: 1, wantErr: check.ErrInvalidValue},
	})
}

func TestWeightedLengthInRange(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		minimum int
		maximum int
		weight  func(rune) int
		wantErr error
	}{
		// 汉字的权重三恰好命中下限。
		{name: "lower endpoint", value: "汉", minimum: 3, maximum: 4, weight: sampleRuneWeight},
		// 汉字加字母的权重和为四，恰好命中上限。
		{name: "upper endpoint", value: "汉a", minimum: 3, maximum: 4, weight: sampleRuneWeight},
		// 表情权重为零，非空输入也可能低于正下限。
		{name: "below minimum", value: "😀", minimum: 1, maximum: 2, weight: sampleRuneWeight, wantErr: check.ErrInvalidValue},
		// 一个汉字的权重超过两位上限，应报告输入错误。
		{name: "above maximum", value: "汉", minimum: 1, maximum: 2, weight: sampleRuneWeight, wantErr: check.ErrInvalidValue},
		// 合法区间内的非法 UTF-8 应传播编码错误。
		{name: "invalid UTF-8", value: "\xff", minimum: 0, maximum: 2, weight: sampleRuneWeight, wantErr: check.ErrInvalidValue},
		// 即使区间和输入都有效，也需要显式提供权重函数。
		{name: "nil weight", value: "", minimum: 0, maximum: 2, wantErr: check.ErrInvalidRule},
		// 反向区间和非法编码同时出现时，应优先报告区间规则错误。
		{name: "reversed bounds and invalid UTF-8", value: "\xff", minimum: 2, maximum: 1, weight: sampleRuneWeight, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, text.WeightedLengthInRange(test.value, test.minimum, test.maximum, test.weight), test.wantErr)
		})
	}
}

func TestWeightedLengthAtLeast(t *testing.T) {
	testTextCountLimit(t, weightedLengthAtLeast, []textCountLimitCase{
		// 汉字权重三，恰好命中下限。
		{name: "at minimum", value: "汉", bound: 3},
		// 只有表情的非空输入权重为零，低于正下限。
		{name: "below minimum", value: "😀", bound: 1, wantErr: check.ErrInvalidValue},
		// 负下限应报告规则错误。
		{name: "negative minimum", value: "", bound: -1, wantErr: check.ErrInvalidRule},
	})
}

func TestWeightedLengthAtMost(t *testing.T) {
	testTextCountLimit(t, weightedLengthAtMost, []textCountLimitCase{
		// 表情和汉字的总权重为三，命中上限。
		{name: "at maximum", value: "😀汉", bound: 3},
		// 一个汉字权重三，超过两位上限。
		{name: "above maximum", value: "汉", bound: 2, wantErr: check.ErrInvalidValue},
		// 非法 UTF-8 应作为输入错误传播。
		{name: "invalid UTF-8", value: "\xff", bound: 2, wantErr: check.ErrInvalidValue},
	})
}

func testTextCount(t *testing.T, count func(string) (int, error), tests []textCountCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := count(test.value)
			assertTextError(t, err, test.wantErr)
			if test.wantErr == nil && got != test.want {
				t.Errorf("count(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}

func testTextCountRange(t *testing.T, validate func(string, int, int) error, tests []textCountRangeCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, validate(test.value, test.minimum, test.maximum), test.wantErr)
		})
	}
}

func testTextCountLimit(t *testing.T, validate func(string, int) error, tests []textCountLimitCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTextError(t, validate(test.value, test.bound), test.wantErr)
		})
	}
}

func weightedLengthAtLeast(value string, bound int) error {
	return text.WeightedLengthAtLeast(value, bound, sampleRuneWeight)
}

func weightedLengthAtMost(value string, bound int) error {
	return text.WeightedLengthAtMost(value, bound, sampleRuneWeight)
}

func sampleRuneWeight(value rune) int {
	switch value {
	case '汉':
		return 3
	case '😀':
		return 0
	default:
		return 1
	}
}

func maxRuneWeight(rune) int { return math.MaxInt }

func negativeRuneWeight(rune) int { return -1 }
