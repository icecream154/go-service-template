package scalar

import (
	"fmt"
	"math"
	"strconv"

	"go-service-template/internal/check"
)

// ParseFiniteFloat64 按 strconv.ParseFloat 的十进制/十六进制语法解析，但拒绝 NaN 和正负无穷。
// 不去空格、不钳制溢出结果；解析失败不在错误中包含原始文本。
func ParseFiniteFloat64(value string) (float64, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, check.NewViolation("float.parse", "must be a valid finite 64-bit floating-point number")
	}
	if err := FiniteFloat64(number); err != nil {
		return 0, err
	}
	return number, nil
}

// FiniteFloat64 拒绝 NaN 及正负无穷，正负零均合法。
func FiniteFloat64(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return check.NewViolation("float.finite", "must be finite")
	}
	return nil
}

// Float64AtLeast 检查有限浮点数下限，边界必须有限且允许等于。
func Float64AtLeast(value, minimum float64) error {
	if math.IsNaN(minimum) || math.IsInf(minimum, 0) {
		return check.NewRuleError("float.minimum", "minimum must be finite")
	}
	if err := FiniteFloat64(value); err != nil {
		return err
	}
	if value < minimum {
		return check.NewViolation("float.minimum", fmt.Sprintf("must be at least %g", minimum))
	}
	return nil
}

// Float64AtMost 检查有限浮点数上限，边界必须有限且允许等于。
func Float64AtMost(value, maximum float64) error {
	if math.IsNaN(maximum) || math.IsInf(maximum, 0) {
		return check.NewRuleError("float.maximum", "maximum must be finite")
	}
	if err := FiniteFloat64(value); err != nil {
		return err
	}
	if value > maximum {
		return check.NewViolation("float.maximum", fmt.Sprintf("must be at most %g", maximum))
	}
	return nil
}

// Float64InRange 检查有限浮点数闭区间；边界非法属于规则错误。
func Float64InRange(value, minimum, maximum float64) error {
	if math.IsNaN(minimum) || math.IsNaN(maximum) || math.IsInf(minimum, 0) || math.IsInf(maximum, 0) || minimum > maximum {
		return check.NewRuleError("float.range", "bounds must be finite and ordered")
	}
	if err := FiniteFloat64(value); err != nil {
		return err
	}
	if value < minimum || value > maximum {
		return check.NewViolation("float.range", fmt.Sprintf("must be between %g and %g inclusive", minimum, maximum))
	}
	return nil
}

// ApproximatelyEqualFloat64 使用调用方显式提供的绝对和相对容差检查近似相等。
// 满足绝对误差 <= absoluteTolerance 或相对误差 <= relativeTolerance 即通过；两者均不预设默认值。
// 相对误差以两值绝对值的较大者为基准，计算避免有限极值相减时溢出影响判断。
func ApproximatelyEqualFloat64(value, expected, absoluteTolerance, relativeTolerance float64) error {
	if math.IsNaN(expected) || math.IsInf(expected, 0) || math.IsNaN(absoluteTolerance) || math.IsInf(absoluteTolerance, 0) ||
		math.IsNaN(relativeTolerance) || math.IsInf(relativeTolerance, 0) || absoluteTolerance < 0 || relativeTolerance < 0 {
		return check.NewRuleError("float.approximately_equal", "expected value and nonnegative tolerances must be finite")
	}
	if err := FiniteFloat64(value); err != nil {
		return err
	}
	difference := math.Abs(value - expected)
	if difference <= absoluteTolerance {
		return nil
	}
	scale := math.Max(math.Abs(value), math.Abs(expected))
	if scale == 0 {
		return nil
	}
	relativeDifference := difference / scale
	if math.IsInf(difference, 0) {
		relativeDifference = math.Abs(value/scale - expected/scale)
	}
	if relativeDifference <= relativeTolerance {
		return nil
	}
	return check.NewViolation("float.approximately_equal", "must be within the configured absolute or relative tolerance")
}
