package scalar_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
)

// TestParseFiniteFloat64 验证文本语法、浮点边界和解析错误的公开行为。
func TestParseFiniteFloat64(t *testing.T) {
	tests := []struct {
		name              string
		input             string
		want              float64
		wantErr           error
		checkInputOmitted bool
	}{
		// 用负小数验证十进制文本的符号和小数部分都得到保留。
		{name: "negative decimal", input: "-12.5", want: -12.5},
		// 用带指数的十进制文本验证指数语法会参与数值计算。
		{name: "decimal exponent", input: "1.25e2", want: 125},
		// 用十六进制小数和二进制指数验证十六进制语法得到支持。
		{name: "hexadecimal exponent", input: "0x1.8p+1", want: 3},
		// 用显式负零验证解析保留零的符号。
		{name: "negative zero", input: "-0", want: math.Copysign(0, -1)},
		// 用最小正次正规数验证有限非零值的下边界。
		{name: "smallest nonzero float64", input: "0x1p-1074", want: math.SmallestNonzeroFloat64},
		// 用最大有限数的精确十六进制表示验证上边界可以解析。
		{name: "largest finite float64", input: "0x1.fffffffffffffp+1023", want: math.MaxFloat64},
		// 用空串验证缺少数值文本时返回输入错误。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 用带前导空格的合法数值验证解析要求原始文本符合语法。
		{name: "leading whitespace", input: " 1", wantErr: check.ErrInvalidValue},
		// 用易辨识的非法文本验证格式错误分类，并核对错误文案是否隐藏原文。
		{name: "malformed input", input: "private-float-input", wantErr: check.ErrInvalidValue, checkInputOmitted: true},
		// 用超出最大浮点数的指数验证溢出错误分类，并检查错误文案省略原文。
		{name: "overflow", input: "1e309", wantErr: check.ErrInvalidValue, checkInputOmitted: true},
		// 用标准 NaN 文本验证语法解析成功后仍执行有限值检查。
		{name: "not a number", input: "NaN", wantErr: check.ErrInvalidValue},
		// 用显式正无穷文本验证拒绝正向非有限值。
		{name: "positive infinity", input: "+Inf", wantErr: check.ErrInvalidValue},
		// 用显式负无穷文本验证拒绝负向非有限值。
		{name: "negative infinity", input: "-Inf", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := scalar.ParseFiniteFloat64(test.input)
			assertFloat64Error(t, err, test.wantErr)
			if test.wantErr != nil {
				if test.checkInputOmitted && strings.Contains(err.Error(), test.input) {
					t.Errorf("error text = %q, want the original input to be omitted", err.Error())
				}
				return
			}
			// 按位比较保留正负零的区别。
			if math.Float64bits(got) != math.Float64bits(test.want) {
				t.Errorf("ParseFiniteFloat64(%q) = %g, want %g", test.input, got, test.want)
			}
		})
	}
}

// TestFiniteFloat64 验证正负零、有限边界和三种非有限输入的分类。
func TestFiniteFloat64(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr error
	}{
		// 用普通零验证有限值检查允许零值。
		{name: "positive zero", value: 0},
		// 用带符号的零验证零的符号不会影响有限值判定。
		{name: "negative zero", value: math.Copysign(0, -1)},
		// 用最大有限值验证有限数的上边界。
		{name: "largest finite value", value: math.MaxFloat64},
		// 用绝对值最大的负有限数验证有限数的下边界。
		{name: "smallest finite value", value: -math.MaxFloat64},
		// 用 NaN 验证无序的浮点值被归为输入错误。
		{name: "not a number", value: math.NaN(), wantErr: check.ErrInvalidValue},
		// 用正无穷验证拒绝正向溢出值。
		{name: "positive infinity", value: math.Inf(1), wantErr: check.ErrInvalidValue},
		// 用负无穷验证拒绝负向溢出值。
		{name: "negative infinity", value: math.Inf(-1), wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertFloat64Error(t, scalar.FiniteFloat64(test.value), test.wantErr)
		})
	}
}

type float64BoundCase struct {
	name    string
	value   float64
	bound   float64
	wantErr error
}

// TestFloat64AtLeast 验证下限包含等号，并区分输入值与下限规则错误。
func TestFloat64AtLeast(t *testing.T) {
	testFloat64Bound(t, scalar.Float64AtLeast, []float64BoundCase{
		// 用等于负下限的值验证闭合下边界。
		{name: "equal to minimum", value: -1.5, bound: -1.5},
		// 用紧邻下限上侧的可表示值验证合法方向。
		{name: "just above minimum", value: math.Nextafter(-1.5, math.Inf(1)), bound: -1.5},
		// 用紧邻下限下侧的可表示值验证任何低于下限的输入都被拒绝。
		{name: "just below minimum", value: math.Nextafter(-1.5, math.Inf(-1)), bound: -1.5, wantErr: check.ErrInvalidValue},
		// 用能通过大小比较的正无穷验证输入还要通过有限值检查。
		{name: "infinite value", value: math.Inf(1), bound: -1.5, wantErr: check.ErrInvalidValue},
		// 用 NaN 下限验证无序边界被归为规则错误。
		{name: "NaN minimum", value: 0, bound: math.NaN(), wantErr: check.ErrInvalidRule},
		// 同时传入无效值与无穷下限，验证优先报告规则错误。
		{name: "infinite minimum and invalid value", value: math.NaN(), bound: math.Inf(-1), wantErr: check.ErrInvalidRule},
	})
}

// TestFloat64AtMost 验证上限包含等号，并区分输入值与上限规则错误。
func TestFloat64AtMost(t *testing.T) {
	testFloat64Bound(t, scalar.Float64AtMost, []float64BoundCase{
		// 用等于上限的值验证闭合上边界。
		{name: "equal to maximum", value: 1.5, bound: 1.5},
		// 用紧邻上限下侧的可表示值验证合法方向。
		{name: "just below maximum", value: math.Nextafter(1.5, math.Inf(-1)), bound: 1.5},
		// 用紧邻上限上侧的可表示值验证超过上限的输入被拒绝。
		{name: "just above maximum", value: math.Nextafter(1.5, math.Inf(1)), bound: 1.5, wantErr: check.ErrInvalidValue},
		// 用能通过大小比较的负无穷验证输入还要通过有限值检查。
		{name: "infinite value", value: math.Inf(-1), bound: 1.5, wantErr: check.ErrInvalidValue},
		// 用 NaN 上限验证无序边界被归为规则错误。
		{name: "NaN maximum", value: 0, bound: math.NaN(), wantErr: check.ErrInvalidRule},
		// 同时传入无效值与无穷上限，验证优先报告规则错误。
		{name: "infinite maximum and invalid value", value: math.NaN(), bound: math.Inf(1), wantErr: check.ErrInvalidRule},
	})
}

// TestFloat64InRange 验证闭区间、非有限值和非法边界的行为。
func TestFloat64InRange(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		minimum float64
		maximum float64
		wantErr error
	}{
		// 用负下端点验证区间包含下界。
		{name: "lower endpoint", value: -1.5, minimum: -1.5, maximum: 2.5},
		// 用区间内部的零验证典型的合法输入。
		{name: "interior value", value: 0, minimum: -1.5, maximum: 2.5},
		// 用正上端点验证区间包含上界。
		{name: "upper endpoint", value: 2.5, minimum: -1.5, maximum: 2.5},
		// 让两个边界相等，验证闭区间可以只包含一个值。
		{name: "singleton range", value: 1, minimum: 1, maximum: 1},
		// 用紧邻下端点的区间外值验证下侧越界。
		{name: "just below range", value: math.Nextafter(-1.5, math.Inf(-1)), minimum: -1.5, maximum: 2.5, wantErr: check.ErrInvalidValue},
		// 用紧邻上端点的区间外值验证上侧越界。
		{name: "just above range", value: math.Nextafter(2.5, math.Inf(1)), minimum: -1.5, maximum: 2.5, wantErr: check.ErrInvalidValue},
		// 用 NaN 输入验证区间检查拒绝无序的输入值。
		{name: "NaN value", value: math.NaN(), minimum: -1.5, maximum: 2.5, wantErr: check.ErrInvalidValue},
		// 单独让下限为 NaN，验证下限的规则错误分类。
		{name: "NaN minimum", value: 0, minimum: math.NaN(), maximum: 2.5, wantErr: check.ErrInvalidRule},
		// 单独让上限为 NaN，验证上限的规则错误分类。
		{name: "NaN maximum", value: 0, minimum: -1.5, maximum: math.NaN(), wantErr: check.ErrInvalidRule},
		// 用能包含有限输入的负无穷下限验证边界必须有限。
		{name: "infinite minimum", value: 0, minimum: math.Inf(-1), maximum: 2.5, wantErr: check.ErrInvalidRule},
		// 用能包含有限输入的正无穷上限验证边界必须有限。
		{name: "infinite maximum", value: 0, minimum: -1.5, maximum: math.Inf(1), wantErr: check.ErrInvalidRule},
		// 使用两个有限且反向的边界，验证边界顺序属于规则约束。
		{name: "reversed bounds", value: 0, minimum: 2.5, maximum: -1.5, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertFloat64Error(t, scalar.Float64InRange(test.value, test.minimum, test.maximum), test.wantErr)
		})
	}
}

// TestApproximatelyEqualFloat64 验证两种容差、有限极值和错误分类。
func TestApproximatelyEqualFloat64(t *testing.T) {
	tests := []struct {
		name              string
		value             float64
		expected          float64
		absoluteTolerance float64
		relativeTolerance float64
		wantErr           error
	}{
		// 用正负零和零容差验证零差值直接通过。
		{name: "signed zeros", value: math.Copysign(0, -1), expected: 0},
		// 用两个相邻浮点数和零容差验证比较采用调用方给出的容差。
		{name: "zero tolerances", value: math.Nextafter(1, 2), expected: 1, wantErr: check.ErrInvalidValue},
		// 用精确的二进制小数让绝对误差恰等于容差，验证等号成立。
		{name: "absolute tolerance boundary", value: 1.25, expected: 1, absoluteTolerance: 0.25},
		// 将容差调到绝对误差下方的相邻值，验证绝对误差越界。
		{name: "absolute tolerance exceeded", value: 1.25, expected: 1, absoluteTolerance: math.Nextafter(0.25, 0), wantErr: check.ErrInvalidValue},
		// 绝对误差超过容差且相对误差恰为 2/8，验证相对容差单独成立即可通过。
		{name: "relative tolerance boundary", value: 8, expected: 6, absoluteTolerance: 1, relativeTolerance: 0.25},
		// 让期望值拥有较大的绝对值，验证相对误差以两值的最大绝对值为基准。
		{name: "larger expected magnitude", value: -6, expected: -8, relativeTolerance: 0.25},
		// 相对容差刚低于 2/8 且绝对容差为零，验证两种容差均失败时拒绝输入。
		{name: "relative tolerance exceeded", value: 8, expected: 6, relativeTolerance: math.Nextafter(0.25, 0), wantErr: check.ErrInvalidValue},
		// 相减会溢出，归一化后的相对误差恰为 2，验证有限极值仍能通过比较。
		{name: "overflowing difference at relative tolerance", value: math.MaxFloat64, expected: -math.MaxFloat64, absoluteTolerance: math.MaxFloat64, relativeTolerance: 2},
		// 用相减同样溢出的不对称极值验证归一化得到实际相对误差 1.5。
		{name: "overflowing difference with unequal magnitudes", value: math.MaxFloat64, expected: -math.MaxFloat64 / 2, relativeTolerance: 1.5},
		// 将相对容差调到 2 下方，验证相减溢出时仍能拒绝越界输入。
		{name: "overflowing difference outside relative tolerance", value: math.MaxFloat64, expected: -math.MaxFloat64, relativeTolerance: math.Nextafter(2, 0), wantErr: check.ErrInvalidValue},
		// 单独让实际值为无穷，验证其属于输入错误。
		{name: "nonfinite value", value: math.Inf(1), expected: 1, wantErr: check.ErrInvalidValue},
		// 同时让实际值和期望值为 NaN，验证优先报告期望值的规则错误。
		{name: "NaN expected and invalid value", value: math.NaN(), expected: math.NaN(), wantErr: check.ErrInvalidRule},
		// 单独让期望值为无穷，验证期望值必须有限。
		{name: "infinite expected", value: 1, expected: math.Inf(-1), wantErr: check.ErrInvalidRule},
		// 保持两值相等并传入 NaN 绝对容差，验证规则在数值比较前检查。
		{name: "NaN absolute tolerance", value: 1, expected: 1, absoluteTolerance: math.NaN(), wantErr: check.ErrInvalidRule},
		// 用会放行任何差值的无穷绝对容差验证容差必须有限。
		{name: "infinite absolute tolerance", value: 2, expected: 1, absoluteTolerance: math.Inf(1), wantErr: check.ErrInvalidRule},
		// 用有限的负绝对容差验证绝对容差还必须非负。
		{name: "negative absolute tolerance", value: 1, expected: 1, absoluteTolerance: -1, wantErr: check.ErrInvalidRule},
		// 保持两值相等并传入 NaN 相对容差，验证另一种容差也要通过规则检查。
		{name: "NaN relative tolerance", value: 1, expected: 1, relativeTolerance: math.NaN(), wantErr: check.ErrInvalidRule},
		// 用会放行任何相对误差的无穷相对容差验证有限值约束。
		{name: "infinite relative tolerance", value: 2, expected: 1, relativeTolerance: math.Inf(1), wantErr: check.ErrInvalidRule},
		// 用有限的负相对容差验证相对容差还必须非负。
		{name: "negative relative tolerance", value: 1, expected: 1, relativeTolerance: -1, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := scalar.ApproximatelyEqualFloat64(test.value, test.expected, test.absoluteTolerance, test.relativeTolerance)
			assertFloat64Error(t, err, test.wantErr)
		})
	}
}

// testFloat64Bound 为上下限检查复用用例执行和错误断言。
func testFloat64Bound(t *testing.T, validate func(float64, float64) error, tests []float64BoundCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertFloat64Error(t, validate(test.value, test.bound), test.wantErr)
		})
	}
}

// assertFloat64Error 按公开错误类别核对输入和规则错误。
func assertFloat64Error(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
