// Package scalar 检查普通标量值，不读取外部状态，也不改写输入。
package scalar

import (
	"fmt"
	"strconv"

	"go-service-template/internal/check"
)

// ParseInt 按十进制解析平台 int；允许前导正负号和零，不接受空白、小数、指数或下划线。
func ParseInt(value string) (int, error) {
	number, err := strconv.ParseInt(value, 10, strconv.IntSize)
	if err != nil {
		return 0, check.NewViolation("integer.parse", "must be a decimal integer within the platform int range")
	}
	return int(number), nil
}

// ParseInt64 按十进制解析 int64；允许前导正负号和零，不接受空白、小数、指数或下划线。
func ParseInt64(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, check.NewViolation("integer.parse_int64", "must be a decimal 64-bit signed integer")
	}
	return number, nil
}

// ParseUint64 按十进制解析 uint64；允许前导零，不接受正负号、空白、小数、指数或下划线。
func ParseUint64(value string) (uint64, error) {
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, check.NewViolation("integer.parse_uint64", "must be a decimal 64-bit unsigned integer without a sign")
	}
	return number, nil
}

// ParsePositiveInt64 解析正 int64，保留 ParseInt64 的文本语法约定。
func ParsePositiveInt64(value string) (int64, error) {
	number, err := ParseInt64(value)
	if err != nil {
		return 0, err
	}
	if err := PositiveInt64(number); err != nil {
		return 0, err
	}
	return number, nil
}

// ParseNonNegativeInt64 解析非负 int64；正负零均表示合法的 0。
func ParseNonNegativeInt64(value string) (int64, error) {
	number, err := ParseInt64(value)
	if err != nil {
		return 0, err
	}
	if err := NonNegativeInt64(number); err != nil {
		return 0, err
	}
	return number, nil
}

// PositiveInt 检查值严格大于 0。
func PositiveInt(value int) error { return IntAtLeast(value, 1) }

// NonNegativeInt 检查值大于或等于 0。
func NonNegativeInt(value int) error { return IntAtLeast(value, 0) }

// PositiveInt64 检查值严格大于 0。
func PositiveInt64(value int64) error { return Int64AtLeast(value, 1) }

// NonNegativeInt64 检查值大于或等于 0。
func NonNegativeInt64(value int64) error { return Int64AtLeast(value, 0) }

// PositiveUint64 检查值严格大于 0。
func PositiveUint64(value uint64) error { return Uint64AtLeast(value, 1) }

// IntAtLeast 检查 int 下限，允许等于 minimum。
func IntAtLeast(value, minimum int) error {
	if value < minimum {
		return check.NewViolation("integer.minimum", fmt.Sprintf("must be at least %d", minimum))
	}
	return nil
}

// IntAtMost 检查 int 上限，允许等于 maximum。
func IntAtMost(value, maximum int) error {
	if value > maximum {
		return check.NewViolation("integer.maximum", fmt.Sprintf("must be at most %d", maximum))
	}
	return nil
}

// IntInRange 检查 int 闭区间；minimum 大于 maximum 属于规则定义错误。
func IntInRange(value, minimum, maximum int) error {
	if minimum > maximum {
		return check.NewRuleError("integer.range", "minimum must not exceed maximum")
	}
	if value < minimum || value > maximum {
		return check.NewViolation("integer.range", fmt.Sprintf("must be between %d and %d inclusive", minimum, maximum))
	}
	return nil
}

// Int64AtLeast 检查 int64 下限，允许等于 minimum。
func Int64AtLeast(value, minimum int64) error {
	if value < minimum {
		return check.NewViolation("integer.minimum", fmt.Sprintf("must be at least %d", minimum))
	}
	return nil
}

// Int64AtMost 检查 int64 上限，允许等于 maximum。
func Int64AtMost(value, maximum int64) error {
	if value > maximum {
		return check.NewViolation("integer.maximum", fmt.Sprintf("must be at most %d", maximum))
	}
	return nil
}

// Int64InRange 检查 int64 闭区间，不转换为浮点数比较。
func Int64InRange(value, minimum, maximum int64) error {
	if minimum > maximum {
		return check.NewRuleError("integer.range", "minimum must not exceed maximum")
	}
	if value < minimum || value > maximum {
		return check.NewViolation("integer.range", fmt.Sprintf("must be between %d and %d inclusive", minimum, maximum))
	}
	return nil
}

// Uint64AtLeast 检查 uint64 下限，不转换为有符号数。
func Uint64AtLeast(value, minimum uint64) error {
	if value < minimum {
		return check.NewViolation("integer.minimum", fmt.Sprintf("must be at least %d", minimum))
	}
	return nil
}

// Uint64AtMost 检查 uint64 上限，不转换为有符号数。
func Uint64AtMost(value, maximum uint64) error {
	if value > maximum {
		return check.NewViolation("integer.maximum", fmt.Sprintf("must be at most %d", maximum))
	}
	return nil
}

// Uint64InRange 检查 uint64 闭区间。
func Uint64InRange(value, minimum, maximum uint64) error {
	if minimum > maximum {
		return check.NewRuleError("integer.range", "minimum must not exceed maximum")
	}
	if value < minimum || value > maximum {
		return check.NewViolation("integer.range", fmt.Sprintf("must be between %d and %d inclusive", minimum, maximum))
	}
	return nil
}

// Int64MultipleOf 检查整数能否整除；divisor 可以为负，不能为 0。
func Int64MultipleOf(value, divisor int64) error {
	if divisor == 0 {
		return check.NewRuleError("integer.multiple", "divisor must not be zero")
	}
	if value%divisor != 0 {
		return check.NewViolation("integer.multiple", "must be an exact multiple of the divisor")
	}
	return nil
}

// Uint64MultipleOf 检查无符号整数能否整除；divisor 不能为 0。
func Uint64MultipleOf(value, divisor uint64) error {
	if divisor == 0 {
		return check.NewRuleError("integer.multiple", "divisor must not be zero")
	}
	if value%divisor != 0 {
		return check.NewViolation("integer.multiple", "must be an exact multiple of the divisor")
	}
	return nil
}
