package text

import (
	"fmt"
	"math"
	"unicode/utf8"

	"go-service-template/internal/check"

	"github.com/rivo/uniseg"
)

// GraphemeUnicodeVersion 是字素分段采用的 Unicode 数据版本（uniseg v0.4.7）。
const GraphemeUnicodeVersion = "15.0.0"

// ByteCount 返回原始字符串的字节数，不对其编码作判断。
func ByteCount(value string) int { return len(value) }

// RuneCount 返回合法 UTF-8 文本的 Unicode 码点数；非法文本不以替换符计数。
func RuneCount(value string) (int, error) {
	if err := ValidUTF8(value); err != nil {
		return 0, err
	}
	return utf8.RuneCountInString(value), nil
}

// GraphemeCount 按 Unicode 15.0.0 扩展字素簇规则计数，不等同于字节数、码点数或显示宽度。
func GraphemeCount(value string) (int, error) {
	if err := ValidUTF8(value); err != nil {
		return 0, err
	}
	return uniseg.GraphemeClusterCount(value), nil
}

// WeightedLength 按每个码点的显式权重求和，不预设任何语言的权重。
// weight 必须非 nil 且对每个码点返回非负权重；返回负值属于规则错误。
// 累加超过 int 范围时拒绝该输入，不发生整数溢出。
func WeightedLength(value string, weight func(rune) int) (int, error) {
	if weight == nil {
		return 0, check.NewRuleError("weighted_length", "weight function must not be nil")
	}
	if err := ValidUTF8(value); err != nil {
		return 0, err
	}
	total := 0
	for _, r := range value {
		amount := weight(r)
		if amount < 0 {
			return 0, check.NewRuleError("weighted_length", "character weights must be non-negative")
		}
		if amount > math.MaxInt-total {
			return 0, check.NewViolation("weighted_length", "weighted length exceeds the supported integer range")
		}
		total += amount
	}
	return total, nil
}

// ByteCountInRange 检查原始字节数位于闭区间，不附加 UTF-8 要求。
func ByteCountInRange(value string, minimum, maximum int) error {
	return checkCountRange("byte_count", len(value), minimum, maximum)
}

// ByteCountAtLeast 检查字节数不小于 minimum，minimum 必须非负。
func ByteCountAtLeast(value string, minimum int) error {
	return ByteCountInRange(value, minimum, math.MaxInt)
}

// ByteCountAtMost 检查字节数不大于 maximum，maximum 必须非负。
func ByteCountAtMost(value string, maximum int) error {
	return ByteCountInRange(value, 0, maximum)
}

// RuneCountInRange 检查合法 UTF-8 文本的码点数位于闭区间。
func RuneCountInRange(value string, minimum, maximum int) error {
	if err := validCountBounds("rune_count", minimum, maximum); err != nil {
		return err
	}
	count, err := RuneCount(value)
	if err != nil {
		return err
	}
	return checkCountRange("rune_count", count, minimum, maximum)
}

// RuneCountAtLeast 检查合法 UTF-8 文本至少包含 minimum 个码点。
func RuneCountAtLeast(value string, minimum int) error {
	return RuneCountInRange(value, minimum, math.MaxInt)
}

// RuneCountAtMost 检查合法 UTF-8 文本至多包含 maximum 个码点。
func RuneCountAtMost(value string, maximum int) error {
	return RuneCountInRange(value, 0, maximum)
}

// GraphemeCountInRange 检查合法文本的扩展字素簇数量位于闭区间。
func GraphemeCountInRange(value string, minimum, maximum int) error {
	if err := validCountBounds("grapheme_count", minimum, maximum); err != nil {
		return err
	}
	count, err := GraphemeCount(value)
	if err != nil {
		return err
	}
	return checkCountRange("grapheme_count", count, minimum, maximum)
}

// GraphemeCountAtLeast 检查扩展字素簇数量不小于 minimum。
func GraphemeCountAtLeast(value string, minimum int) error {
	return GraphemeCountInRange(value, minimum, math.MaxInt)
}

// GraphemeCountAtMost 检查扩展字素簇数量不大于 maximum。
func GraphemeCountAtMost(value string, maximum int) error {
	return GraphemeCountInRange(value, 0, maximum)
}

// WeightedLengthInRange 检查按显式码点权重计算的长度位于闭区间。
func WeightedLengthInRange(value string, minimum, maximum int, weight func(rune) int) error {
	if err := validCountBounds("weighted_length", minimum, maximum); err != nil {
		return err
	}
	count, err := WeightedLength(value, weight)
	if err != nil {
		return err
	}
	return checkCountRange("weighted_length", count, minimum, maximum)
}

// WeightedLengthAtLeast 检查加权长度不小于 minimum。
func WeightedLengthAtLeast(value string, minimum int, weight func(rune) int) error {
	return WeightedLengthInRange(value, minimum, math.MaxInt, weight)
}

// WeightedLengthAtMost 检查加权长度不大于 maximum。
func WeightedLengthAtMost(value string, maximum int, weight func(rune) int) error {
	return WeightedLengthInRange(value, 0, maximum, weight)
}

func validCountBounds(rule string, minimum, maximum int) error {
	if minimum < 0 || maximum < minimum {
		return check.NewRuleError(rule, "bounds must satisfy 0 <= minimum <= maximum")
	}
	return nil
}

func checkCountRange(rule string, count, minimum, maximum int) error {
	if err := validCountBounds(rule, minimum, maximum); err != nil {
		return err
	}
	if count < minimum || count > maximum {
		return check.NewViolation(rule, fmt.Sprintf("count must be between %d and %d inclusive", minimum, maximum))
	}
	return nil
}
