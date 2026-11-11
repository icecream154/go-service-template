package scalar_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
)

// 测试函数按 integer.go 中被测函数的声明顺序排列。

// integerParseCase 保存原始文本和成功时的预期数值。wantErr 为 nil 表示预期成功。
type integerParseCase[T int | int64 | uint64] struct {
	name    string
	input   string
	want    T
	wantErr error
}

// TestParseInt 验证平台 int 的十进制语法、上下限和越界错误。
func TestParseInt(t *testing.T) {
	// 用 uint64 构造 int 上下界相邻的越界值，兼容 32 位和 64 位平台。
	underflow := "-" + strconv.FormatUint(uint64(math.MaxInt)+2, 10)
	overflow := strconv.FormatUint(uint64(math.MaxInt)+1, 10)
	testIntegerParser(t, scalar.ParseInt, []integerParseCase[int]{
		// 零是合法的有符号整数。
		{name: "zero", input: "0", want: 0},
		// 前导正号和零可同时出现，数字 8 验证前导零仍按十进制解释。
		{name: "leading plus and zeros", input: "+0008", want: 8},
		// 当前平台的最小负数仍在 int 范围内。
		{name: "smallest platform int", input: strconv.Itoa(math.MinInt), want: math.MinInt},
		// 当前平台的最大正数仍在 int 范围内。
		{name: "largest platform int", input: strconv.Itoa(math.MaxInt), want: math.MaxInt},
		// 比最小值少 1 的十进制文本应报告输入错误。
		{name: "below platform int range", input: underflow, wantErr: check.ErrInvalidValue},
		// 比最大值多 1 的十进制文本应报告输入错误。
		{name: "above platform int range", input: overflow, wantErr: check.ErrInvalidValue},
		// 空串缺少必需的十进制数字。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 保留原始输入的前导空格，验证解析器拒绝带空格的文本。
		{name: "leading whitespace", input: " 42", wantErr: check.ErrInvalidValue},
		// 含小数点的文本超出整数语法。
		{name: "fractional number", input: "1.5", wantErr: check.ErrInvalidValue},
		// 即使指数文本表示整数，也应按整数文本语法拒绝。
		{name: "exponent notation", input: "1e3", wantErr: check.ErrInvalidValue},
		// 输入文本中的下划线不属于允许的数字语法。
		{name: "underscore separator", input: "1_000", wantErr: check.ErrInvalidValue},
	})
}

// TestParseInt64 验证固定 64 位的有符号十进制解析和越界处理。
func TestParseInt64(t *testing.T) {
	testIntegerParser(t, scalar.ParseInt64, []integerParseCase[int64]{
		// 合法的负号可以出现在零前面，解析结果仍是 0。
		{name: "signed zero", input: "-0", want: 0},
		// 前导正号和零不改变十进制数值。
		{name: "leading plus and zeros", input: "+0008", want: 8},
		// 负数侧可表示到 -2^63，且包括该端点。
		{name: "smallest int64", input: "-9223372036854775808", want: math.MinInt64},
		// 正数侧可表示到 2^63-1，且包括该端点。
		{name: "largest int64", input: "9223372036854775807", want: math.MaxInt64},
		// 比 int64 最小值少 1，应报告输入错误。
		{name: "below int64 range", input: "-9223372036854775809", wantErr: check.ErrInvalidValue},
		// 比 int64 最大值多 1，应报告输入错误。
		{name: "above int64 range", input: "9223372036854775808", wantErr: check.ErrInvalidValue},
		// 零个数字不能构成 int64 文本。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 前导空格应触发格式错误。
		{name: "leading whitespace", input: " 42", wantErr: check.ErrInvalidValue},
		// 小数点和小数位不属于 int64 文本语法。
		{name: "fractional number", input: "1.5", wantErr: check.ErrInvalidValue},
		// 指数形式超出十进制整数的文本语法。
		{name: "exponent notation", input: "1e3", wantErr: check.ErrInvalidValue},
		// 含数字分隔符的文本应报告输入错误。
		{name: "underscore separator", input: "1_000", wantErr: check.ErrInvalidValue},
	})
}

// TestParseUint64 验证无符号十进制语法、完整 uint64 范围和溢出处理。
func TestParseUint64(t *testing.T) {
	testIntegerParser(t, scalar.ParseUint64, []integerParseCase[uint64]{
		// 无符号范围的下端点为 0。
		{name: "zero", input: "0", want: 0},
		// 数字 8 验证前导零不会改变十进制解释。
		{name: "leading zeros", input: "0008", want: 8},
		// 接受 2^64-1，确保无符号解析能处理最高位为 1 的值。
		{name: "largest uint64", input: "18446744073709551615", want: math.MaxUint64},
		// 上端点再加 1 已超出 uint64 范围。
		{name: "above uint64 range", input: "18446744073709551616", wantErr: check.ErrInvalidValue},
		// 数值 1 本身有效，显式正号应触发语法拒绝。
		{name: "leading plus", input: "+1", wantErr: check.ErrInvalidValue},
		// 用负零单独验证负号非法，避开负值越界的影响。
		{name: "negative zero", input: "-0", wantErr: check.ErrInvalidValue},
		// 空串没有任何数字，应报告输入错误。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 前导空格不属于无符号整数语法。
		{name: "leading whitespace", input: " 42", wantErr: check.ErrInvalidValue},
		// 含小数点的文本应报告输入错误。
		{name: "fractional number", input: "1.5", wantErr: check.ErrInvalidValue},
		// 指数形式应被拒绝，即使其数值为非负整数。
		{name: "exponent notation", input: "1e3", wantErr: check.ErrInvalidValue},
		// 下划线分隔符应触发无符号整数格式错误。
		{name: "underscore separator", input: "1_000", wantErr: check.ErrInvalidValue},
	})
}

// TestParsePositiveInt64 验证正 int64 解析的数值边界和错误类别。
func TestParsePositiveInt64(t *testing.T) {
	testIntegerParser(t, scalar.ParsePositiveInt64, []integerParseCase[int64]{
		// 用普通正数验证解析返回原数值。
		{name: "positive integer", input: "42", want: 42},
		// 正数范围的下端点 1 必须通过校验。
		{name: "smallest positive value", input: "1", want: 1},
		// 合法的正号和前导零应保留十进制解释。
		{name: "leading plus and zeros", input: "+0008", want: 8},
		// int64 最大值仍是合法正数。
		{name: "largest int64", input: "9223372036854775807", want: math.MaxInt64},
		// 零能完成整数解析，随后应被正数校验拒绝。
		{name: "zero", input: "0", wantErr: check.ErrInvalidValue},
		// 负数能完成整数解析，随后应被正数校验拒绝。
		{name: "negative integer", input: "-1", wantErr: check.ErrInvalidValue},
		// 数值为正但超出 int64 范围，应在解析阶段报告输入错误。
		{name: "int64 overflow", input: "9223372036854775808", wantErr: check.ErrInvalidValue},
		// 空串应保留底层解析的输入错误类别。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 前导空格应触发解析错误，即使后面的数字为正。
		{name: "leading whitespace", input: " 42", wantErr: check.ErrInvalidValue},
		// 正小数仍违反整数文本语法。
		{name: "fractional number", input: "1.5", wantErr: check.ErrInvalidValue},
		// 指数形式即使表示正整数也应在解析阶段拒绝。
		{name: "exponent notation", input: "1e3", wantErr: check.ErrInvalidValue},
		// 含下划线的文本超出十进制数字语法。
		{name: "underscore separator", input: "1_000", wantErr: check.ErrInvalidValue},
	})
}

// TestParseNonNegativeInt64 验证非负 int64 解析接受零及其合法文本形式。
func TestParseNonNegativeInt64(t *testing.T) {
	testIntegerParser(t, scalar.ParseNonNegativeInt64, []integerParseCase[int64]{
		// 不带符号的零是非负范围的下端点。
		{name: "unsigned zero", input: "0", want: 0},
		// 前导正号仍将文本解析为零。
		{name: "positive zero", input: "+0", want: 0},
		// 前导负号同样将此文本解析为零，校验依据是数值。
		{name: "negative zero", input: "-0", want: 0},
		// 带正号和前导零的正整数应按十进制解析。
		{name: "positive integer", input: "+0008", want: 8},
		// 非负 int64 的上端点可正常解析。
		{name: "largest int64", input: "9223372036854775807", want: math.MaxInt64},
		// 比下端点少 1 的值应被非负校验拒绝。
		{name: "negative integer", input: "-1", wantErr: check.ErrInvalidValue},
		// 超出 int64 上端点的文本应报告解析错误。
		{name: "above int64 range", input: "9223372036854775808", wantErr: check.ErrInvalidValue},
		// 小数输入应保留整数解析的输入错误类别。
		{name: "fractional number", input: "1.5", wantErr: check.ErrInvalidValue},
	})
}

type integerValueCase[T int | int64 | uint64] struct {
	name    string
	value   T
	wantErr error
}

// TestPositiveInt 验证平台 int 的正数约束。
func TestPositiveInt(t *testing.T) {
	testIntegerValue(t, scalar.PositiveInt, []integerValueCase[int]{
		// -1 验证负数应被归类为无效值。
		{name: "negative", value: -1, wantErr: check.ErrInvalidValue},
		// 0 紧邻下界，用于区分正数和非负数约束。
		{name: "zero", value: 0, wantErr: check.ErrInvalidValue},
		// 1 是最小合法正数，应通过检查。
		{name: "positive", value: 1},
	})
}

// TestNonNegativeInt 验证平台 int 的非负约束包含零。
func TestNonNegativeInt(t *testing.T) {
	testIntegerValue(t, scalar.NonNegativeInt, []integerValueCase[int]{
		// -1 刚低于下界，应返回无效值错误。
		{name: "negative", value: -1, wantErr: check.ErrInvalidValue},
		// 0 位于下界，验证检查允许等于下界。
		{name: "zero", value: 0},
		// 1 高于下界，验证普通正数仍然有效。
		{name: "positive", value: 1},
	})
}

// TestPositiveInt64 验证 int64 的正数约束。
func TestPositiveInt64(t *testing.T) {
	testIntegerValue(t, scalar.PositiveInt64, []integerValueCase[int64]{
		// -1 代表负的 int64，应返回无效值错误。
		{name: "negative", value: -1, wantErr: check.ErrInvalidValue},
		// 0 在合法正数范围之外，应返回无效值错误。
		{name: "zero", value: 0, wantErr: check.ErrInvalidValue},
		// 1 验证 int64 正数下界本身有效。
		{name: "positive", value: 1},
	})
}

// TestNonNegativeInt64 验证 int64 的非负约束包含零。
func TestNonNegativeInt64(t *testing.T) {
	testIntegerValue(t, scalar.NonNegativeInt64, []integerValueCase[int64]{
		// -1 是离有效范围最近的负数，应被拒绝。
		{name: "negative", value: -1, wantErr: check.ErrInvalidValue},
		// 0 是非负 int64 的最小值，应被接受。
		{name: "zero", value: 0},
		// 1 验证大于零的 int64 同样满足约束。
		{name: "positive", value: 1},
	})
}

// TestPositiveUint64 验证无符号整数的正数下界和高位值。
func TestPositiveUint64(t *testing.T) {
	testIntegerValue(t, scalar.PositiveUint64, []integerValueCase[uint64]{
		// 0 是 uint64 中唯一不满足正数约束的值。
		{name: "zero", value: 0, wantErr: check.ErrInvalidValue},
		// 1 验证最小正数可以通过检查。
		{name: "smallest positive value", value: 1},
		// 最大 uint64 仍是正数，验证高位值不会被当成有符号负数。
		{name: "largest uint64", value: math.MaxUint64},
	})
}

type integerBoundCase[T int | int64 | uint64] struct {
	name    string
	value   T
	bound   T
	wantErr error
}

// TestIntAtLeast 验证 int 下限包含边界，并按有符号整数比较。
func TestIntAtLeast(t *testing.T) {
	testIntegerBound(t, scalar.IntAtLeast, []integerBoundCase[int]{
		// -6 恰好比下限小 1，应返回输入值错误。
		{name: "below bound", value: -6, bound: -5, wantErr: check.ErrInvalidValue},
		// -5 等于下限，应通过闭合边界检查。
		{name: "equal to bound", value: -5, bound: -5},
		// -4 大于下限，验证下限之上的值也通过。
		{name: "above bound", value: -4, bound: -5},
		// -1 小于 0，验证跨零比较遵循有符号整数的顺序。
		{name: "negative value below zero", value: -1, bound: 0, wantErr: check.ErrInvalidValue},
	})
}

// TestIntAtMost 验证 int 上限包含边界，并按有符号整数比较。
func TestIntAtMost(t *testing.T) {
	testIntegerBound(t, scalar.IntAtMost, []integerBoundCase[int]{
		// -6 小于上限 -5，应通过。
		{name: "below bound", value: -6, bound: -5},
		// -5 等于上限，应通过闭合边界检查。
		{name: "equal to bound", value: -5, bound: -5},
		// -4 恰好比上限大 1，应返回输入值错误。
		{name: "above bound", value: -4, bound: -5, wantErr: check.ErrInvalidValue},
		// -1 小于上限 0，应通过跨零比较。
		{name: "negative value below zero", value: -1, bound: 0},
	})
}

type integerRangeCase[T int | int64 | uint64] struct {
	name    string
	value   T
	minimum T
	maximum T
	wantErr error
}

// TestIntInRange 验证 int 闭区间包含端点，并区分越界输入与反向边界。
func TestIntInRange(t *testing.T) {
	testIntegerRange(t, scalar.IntInRange, []integerRangeCase[int]{
		// -2 是闭区间的下端点，应通过。
		{name: "lower endpoint", value: -2, minimum: -2, maximum: 2},
		// 0 严格位于两端之间，验证普通区间内取值。
		{name: "inside range", value: 0, minimum: -2, maximum: 2},
		// 2 是闭区间的上端点，应通过。
		{name: "upper endpoint", value: 2, minimum: -2, maximum: 2},
		// -3 紧邻下端点且位于区间外，应返回输入值错误。
		{name: "below range", value: -3, minimum: -2, maximum: 2, wantErr: check.ErrInvalidValue},
		// 3 紧邻上端点且位于区间外，应返回输入值错误。
		{name: "above range", value: 3, minimum: -2, maximum: 2, wantErr: check.ErrInvalidValue},
		// 相等的上下限构成单值区间，唯一的值 5 应通过。
		{name: "single value range", value: 5, minimum: 5, maximum: 5},
		// 下限超过上限时区间无效，应优先返回规则错误。
		{name: "reversed bounds", value: 0, minimum: 2, maximum: -2, wantErr: check.ErrInvalidRule},
	})
}

// TestInt64AtLeast 验证 int64 下限包含边界，并精确比较大整数。
func TestInt64AtLeast(t *testing.T) {
	// 边界及其相邻整数在转换为 float64 后会变成同一个值。
	const bound int64 = 1<<53 + 4
	testIntegerBound(t, scalar.Int64AtLeast, []integerBoundCase[int64]{
		// 比下限小 1 的整数仍应被识别为越界。
		{name: "below bound", value: bound - 1, bound: bound, wantErr: check.ErrInvalidValue},
		// 等于大整数下限的值应通过。
		{name: "equal to bound", value: bound, bound: bound},
		// 比下限大 1 的整数应通过。
		{name: "above bound", value: bound + 1, bound: bound},
		// 负数 -1 小于下限 0，应返回输入值错误。
		{name: "negative value below zero", value: -1, bound: 0, wantErr: check.ErrInvalidValue},
	})
}

// TestInt64AtMost 验证 int64 上限包含边界，并精确比较大整数。
func TestInt64AtMost(t *testing.T) {
	// 边界及其相邻整数在转换为 float64 后会变成同一个值。
	const bound int64 = 1<<53 + 4
	testIntegerBound(t, scalar.Int64AtMost, []integerBoundCase[int64]{
		// 比上限小 1 的整数应通过。
		{name: "below bound", value: bound - 1, bound: bound},
		// 等于大整数上限的值应通过。
		{name: "equal to bound", value: bound, bound: bound},
		// 比上限大 1 的整数仍应被识别为越界。
		{name: "above bound", value: bound + 1, bound: bound, wantErr: check.ErrInvalidValue},
		// -1 小于上限 0，应通过有符号比较。
		{name: "negative value below zero", value: -1, bound: 0},
	})
}

// TestInt64InRange 验证 int64 闭区间及大整数端点附近的精确比较。
func TestInt64InRange(t *testing.T) {
	// 两端均大于 2^53。各端点外侧相差 1 的整数转为 float64 时会舍入到该端点。
	const minimum int64 = 1<<53 + 4
	const maximum = minimum + 4
	testIntegerRange(t, scalar.Int64InRange, []integerRangeCase[int64]{
		// 取值恰好等于下端点，应通过。
		{name: "lower endpoint", value: minimum, minimum: minimum, maximum: maximum},
		// 下端点加 1 严格位于区间内，应通过。
		{name: "inside range", value: minimum + 1, minimum: minimum, maximum: maximum},
		// 取值恰好等于上端点，应通过。
		{name: "upper endpoint", value: maximum, minimum: minimum, maximum: maximum},
		// 下端点减 1 在区间外，应精确识别为输入值错误。
		{name: "below range", value: minimum - 1, minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 上端点加 1 在区间外，应精确识别为输入值错误。
		{name: "above range", value: maximum + 1, minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// -1 位于跨越零的区间内，验证负值能参与有符号比较。
		{name: "range crosses zero", value: -1, minimum: -2, maximum: 2},
		// 两端相等时，取值等于该端点应通过。
		{name: "single value range", value: minimum, minimum: minimum, maximum: minimum},
		// 交换上下限使区间无效，应返回规则错误。
		{name: "reversed bounds", value: minimum, minimum: maximum, maximum: minimum, wantErr: check.ErrInvalidRule},
	})
}

// TestUint64AtLeast 验证 uint64 下限包含边界，并保持无符号数顺序。
func TestUint64AtLeast(t *testing.T) {
	// 2^63 是超过 int64 最大值的第一个 uint64 值。
	const bound uint64 = 1 << 63
	testIntegerBound(t, scalar.Uint64AtLeast, []integerBoundCase[uint64]{
		// 2^63-1 比下限小 1，应返回输入值错误。
		{name: "below bound", value: bound - 1, bound: bound, wantErr: check.ErrInvalidValue},
		// 超出 int64 正数范围的值等于下限，应通过。
		{name: "equal to bound", value: bound, bound: bound},
		// 2^63+1 大于下限，应按 uint64 顺序通过。
		{name: "above bound", value: bound + 1, bound: bound},
	})
}

// TestUint64AtMost 验证 uint64 上限包含边界，并保持无符号数顺序。
func TestUint64AtMost(t *testing.T) {
	// 2^63 是超过 int64 最大值的第一个 uint64 值。
	const bound uint64 = 1 << 63
	testIntegerBound(t, scalar.Uint64AtMost, []integerBoundCase[uint64]{
		// 2^63-1 小于上限 2^63，应按 uint64 顺序通过。
		{name: "below bound", value: bound - 1, bound: bound},
		// 2^63 等于上限，应通过闭合边界检查。
		{name: "equal to bound", value: bound, bound: bound},
		// 2^63+1 比上限大 1，应返回输入值错误。
		{name: "above bound", value: bound + 1, bound: bound, wantErr: check.ErrInvalidValue},
	})
}

// TestUint64InRange 验证 uint64 闭区间跨越 int64 正数上限时的边界与错误类型。
func TestUint64InRange(t *testing.T) {
	// 区间两端都属于有效的 uint64 值，上端点超过 int64 最大值。
	const minimum uint64 = math.MaxInt64
	const maximum = minimum + 2
	testIntegerRange(t, scalar.Uint64InRange, []integerRangeCase[uint64]{
		// 等于下端点 int64 最大值的输入应通过。
		{name: "lower endpoint", value: minimum, minimum: minimum, maximum: maximum},
		// 2^63 位于这个 uint64 区间内，验证超过 int64 最大值的输入。
		{name: "inside range", value: minimum + 1, minimum: minimum, maximum: maximum},
		// 等于上端点的输入应按闭区间规则通过。
		{name: "upper endpoint", value: maximum, minimum: minimum, maximum: maximum},
		// 比下端点小 1 的输入应返回输入值错误。
		{name: "below range", value: minimum - 1, minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 比上端点大 1 的输入应返回输入值错误。
		{name: "above range", value: maximum + 1, minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 两端相等且超过 int64 最大值时，仍应接受唯一的区间内取值。
		{name: "single value range", value: maximum, minimum: maximum, maximum: maximum},
		// 按 uint64 比较后下限超过上限，应返回规则错误。
		{name: "reversed bounds", value: minimum, minimum: maximum, maximum: minimum, wantErr: check.ErrInvalidRule},
	})
}

type integerMultipleCase[T int64 | uint64] struct {
	name    string
	value   T
	divisor T
	wantErr error
}

// TestInt64MultipleOf 验证有符号整除规则和除数为零时的错误分类。
func TestInt64MultipleOf(t *testing.T) {
	testIntegerMultiple(t, scalar.Int64MultipleOf, []integerMultipleCase[int64]{
		// 12 除以 3 的余数为零，作为普通整除场景。
		{name: "exact multiple", value: 12, divisor: 3},
		// 负被除数仍可整除，验证负号本身不会导致拒绝。
		{name: "negative dividend", value: -12, divisor: 3},
		// 负除数也合法，12 除以 -3 应通过检查。
		{name: "negative divisor", value: 12, divisor: -3},
		// 0 除以非零数的余数为零，因此应通过。
		{name: "zero dividend", value: 0, divisor: 3},
		// 最小 int64 除以 -1 的余数仍是零，用于验证极值。
		{name: "smallest int64 divided by negative one", value: math.MinInt64, divisor: -1},
		// 10 除以 3 有余数，应归类为无效值。
		{name: "nonzero remainder", value: 10, divisor: 3, wantErr: check.ErrInvalidValue},
		// -10 除以 3 的余数为负，验证任意非零余数都会失败。
		{name: "negative remainder", value: -10, divisor: 3, wantErr: check.ErrInvalidValue},
		// 除数为零使规则本身无效，应在取余前返回规则错误。
		{name: "zero divisor", value: 12, divisor: 0, wantErr: check.ErrInvalidRule},
		// 被除数也是 0 时，仍优先报告除数无效。
		{name: "zero dividend and divisor", value: 0, divisor: 0, wantErr: check.ErrInvalidRule},
	})
}

// TestUint64MultipleOf 验证无符号整除规则和高位值。
func TestUint64MultipleOf(t *testing.T) {
	testIntegerMultiple(t, scalar.Uint64MultipleOf, []integerMultipleCase[uint64]{
		// 12 除以 3 的余数为零，应通过。
		{name: "exact multiple", value: 12, divisor: 3},
		// 被除数可以为 0，只要除数有效。
		{name: "zero dividend", value: 0, divisor: 3},
		// 最大 uint64 能被 3 整除，用于检验高位值仍按无符号整数取余。
		{name: "largest uint64", value: math.MaxUint64, divisor: 3},
		// 除数有效且余数非零时，应返回无效值错误。
		{name: "nonzero remainder", value: 10, divisor: 3, wantErr: check.ErrInvalidValue},
		// 除数为零时，应返回无效规则错误。
		{name: "zero divisor", value: 12, divisor: 0, wantErr: check.ErrInvalidRule},
		// 0 除以 0 同样适用除数检查，避免把零被除数直接判为成功。
		{name: "zero dividend and divisor", value: 0, divisor: 0, wantErr: check.ErrInvalidRule},
	})
}

// testIntegerParser 为各解析函数使用相同的数值和错误断言。
func testIntegerParser[T int | int64 | uint64](t *testing.T, parse func(string) (T, error), tests []integerParseCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input)
			// 预期错误为 nil 时要求解析成功。拒绝场景按公开的错误类别匹配。
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("parse(%q) error = %v, want %v", test.input, err, test.wantErr)
			}
			// 成功时核对解析值。拒绝场景的判定依据是上面的错误断言。
			if test.wantErr == nil && got != test.want {
				t.Errorf("parse(%q) = %d, want %d", test.input, got, test.want)
			}
		})
	}
}

// testIntegerValue 逐个检查正数和非负数用例的错误结果。
func testIntegerValue[T int | int64 | uint64](t *testing.T, validate func(T) error, tests []integerValueCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validate(test.value); !errors.Is(err, test.wantErr) {
				t.Errorf("validate(%d) error = %v, want %v", test.value, err, test.wantErr)
			}
		})
	}
}

// testIntegerBound 把值和边界传给单侧检查，验证边界内外的错误结果。
func testIntegerBound[T int | int64 | uint64](t *testing.T, validate func(T, T) error, tests []integerBoundCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validate(test.value, test.bound); !errors.Is(err, test.wantErr) {
				t.Errorf("validate(%d, %d) error = %v, want %v", test.value, test.bound, err, test.wantErr)
			}
		})
	}
}

// testIntegerRange 以完整区间运行用例，区分值越界和区间定义错误。
func testIntegerRange[T int | int64 | uint64](t *testing.T, inRange func(T, T, T) error, tests []integerRangeCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := inRange(test.value, test.minimum, test.maximum); !errors.Is(err, test.wantErr) {
				t.Errorf("inRange(%d, %d, %d) error = %v, want %v", test.value, test.minimum, test.maximum, err, test.wantErr)
			}
		})
	}
}

// testIntegerMultiple 逐个验证整除、非零余数和非法除数的结果。
func testIntegerMultiple[T int64 | uint64](t *testing.T, multipleOf func(T, T) error, tests []integerMultipleCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := multipleOf(test.value, test.divisor); !errors.Is(err, test.wantErr) {
				t.Errorf("multipleOf(%d, %d) error = %v, want %v", test.value, test.divisor, err, test.wantErr)
			}
		})
	}
}
