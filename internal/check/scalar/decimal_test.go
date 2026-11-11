package scalar_test

import (
	"errors"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
)

// 测试函数按 decimal.go 中被测函数和方法的声明顺序排列。

func TestParseDecimal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		// 整数可以省略小数点，并保留整数前导零。
		{name: "integer with leading zeros", input: "00042"},
		// 正号和小数尾随零都是合法原文的一部分，解析结果应保留它们。
		{name: "explicit plus and trailing zeros", input: "+001.2300"},
		// 负号、小数点两侧的数字和尾随零可同时出现，解析应成功。
		{name: "negative fraction", input: "-0.0040"},
		// 整数部分超过 uint64 上限，验证精确十进制可以表示超出机器整数范围的值。
		{name: "beyond uint64 range", input: "18446744073709551616.00001"},
		// 空串缺少整数数字，应返回输入值错误。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 单独的符号用来验证符号之后必须至少有一个数字。
		{name: "sign without digits", input: "-", wantErr: check.ErrInvalidValue},
		// 小数点前省略整数部分时，应返回输入值错误。
		{name: "missing integer digits", input: "+.5", wantErr: check.ErrInvalidValue},
		// 小数点后缺少数字时，应返回输入值错误。
		{name: "missing fractional digits", input: "1.", wantErr: check.ErrInvalidValue},
		// 第二个小数点违反单小数点约定，应返回输入值错误。
		{name: "multiple decimal points", input: "1.2.3", wantErr: check.ErrInvalidValue},
		// 在有效数值前放一个空格，验证解析器对空白执行严格校验。
		{name: "leading whitespace", input: " 1", wantErr: check.ErrInvalidValue},
		// 用表示有限数值的指数文本验证十进制解析要求普通数字写法。
		{name: "exponent notation", input: "1e2", wantErr: check.ErrInvalidValue},
		// 全角数字验证语法只接受 ASCII 数字。
		{name: "fullwidth digit", input: "１.2", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := scalar.ParseDecimal(test.input)
			assertDecimalError(t, err, test.wantErr)
			if test.wantErr == nil && value.String() != test.input {
				t.Errorf("ParseDecimal(%q).String() = %q, want %q", test.input, value.String(), test.input)
			}
		})
	}
}

type decimalValueCase[T comparable] struct {
	name  string
	value scalar.Decimal
	want  T
}

func TestDecimalString(t *testing.T) {
	testDecimalValue(t, scalar.Decimal.String, []decimalValueCase[string]{
		// 未经解析的零值应有可读的整数文本。
		{name: "zero value", value: scalar.Decimal{}, want: "0"},
		// 正号、整数前导零及小数尾随零应保持原样。
		{name: "original signed text", value: mustParseDecimal(t, "+0001.2300"), want: "+0001.2300"},
		// 有符号的零应保留其原文，便于区分输入形式。
		{name: "original negative zero", value: mustParseDecimal(t, "-00.000"), want: "-00.000"},
	})
}

func TestDecimalScale(t *testing.T) {
	testDecimalValue(t, scalar.Decimal.Scale, []decimalValueCase[int]{
		// 零值表示整数零，小数位数应为 0。
		{name: "zero value", value: scalar.Decimal{}, want: 0},
		// 没有小数点的有符号整数验证普通整数的小数位数也为 0。
		{name: "integer", value: mustParseDecimal(t, "-0012"), want: 0},
		// 两个有效小数位后补两个零，验证原始的四位小数都被保留。
		{name: "trailing fractional zeros", value: mustParseDecimal(t, "1.2300"), want: 4},
		// 数值为零且小数部分全部为零时，仍按输入计算三位小数。
		{name: "fractional zero", value: mustParseDecimal(t, "0.000"), want: 3},
	})
}

func TestDecimalPrecision(t *testing.T) {
	testDecimalValue(t, scalar.Decimal.Precision, []decimalValueCase[int]{
		// 未经解析的零值应报告最小精度 1。
		{name: "zero value", value: scalar.Decimal{}, want: 1},
		// 解析出的整数零验证全为前导零时精度仍至少为 1。
		{name: "integer zero", value: mustParseDecimal(t, "000"), want: 1},
		// 整数前导零和负号应排除，120 的整数末尾零应计入总位数。
		{name: "sign and leading zeros", value: mustParseDecimal(t, "-00120"), want: 3},
		// 小数点两侧的有效位数相加，小数尾随零仍占用精度。
		{name: "integer and fractional digits", value: mustParseDecimal(t, "+001.2300"), want: 5},
		// 整数部分全为零，小数前导零和尾随零都应计入精度。
		{name: "fractional digits only", value: mustParseDecimal(t, "000.0040"), want: 4},
	})
}

func TestDecimalSign(t *testing.T) {
	testDecimalValue(t, scalar.Decimal.Sign, []decimalValueCase[int]{
		// 零值没有显式系数，仍应报告数值符号 0。
		{name: "zero value", value: scalar.Decimal{}, want: 0},
		// 显式正号的非零小数应报告数值符号 1。
		{name: "positive fraction", value: mustParseDecimal(t, "+0.0001"), want: 1},
		// 整数部分为零的负小数验证符号取决于完整数值。
		{name: "negative fraction", value: mustParseDecimal(t, "-0.0001"), want: -1},
		// 显式正号的零应报告数值符号 0。
		{name: "positive zero", value: mustParseDecimal(t, "+0.00"), want: 0},
		// 显式负号的零也应报告数值符号 0。
		{name: "negative zero", value: mustParseDecimal(t, "-000.00"), want: 0},
	})
}

func TestDecimalCmp(t *testing.T) {
	tests := []struct {
		name  string
		left  scalar.Decimal
		right scalar.Decimal
		want  int
	}{
		// 两个值的小数位数相同，比较应直接区分系数大小。
		{name: "same scale", left: mustParseDecimal(t, "1.02"), right: mustParseDecimal(t, "1.03"), want: -1},
		// 左值的小数位数较少，比较时应对齐两者并判断数值相等。
		{name: "shorter scale on left", left: mustParseDecimal(t, "1.2"), right: mustParseDecimal(t, "1.2000"), want: 0},
		// 交换长短小数位数，验证右值也可对齐且比较结果仍相等。
		{name: "shorter scale on right", left: mustParseDecimal(t, "1.2000"), right: mustParseDecimal(t, "1.2"), want: 0},
		// 负零和 Decimal 的零值在数值上应相等，同时覆盖零值的系数处理。
		{name: "zero value and negative zero", left: scalar.Decimal{}, right: mustParseDecimal(t, "-0.000"), want: 0},
		// 负数在小数位对齐后仍按负数顺序比较，-1.2 大于 -1.2001。
		{name: "negative fractions", left: mustParseDecimal(t, "-1.20"), right: mustParseDecimal(t, "-1.2001"), want: 1},
		// 在 2^53 处比较相邻整数，验证比较保留 float64 无法区分的个位差异。
		{name: "adjacent integers above float precision", left: mustParseDecimal(t, "9007199254740992"), right: mustParseDecimal(t, "9007199254740993"), want: -1},
		// 大整数部分相同，额外一位小数应使右值严格大于左值。
		{name: "tiny difference at a large magnitude", left: mustParseDecimal(t, "9007199254740992.0001"), right: mustParseDecimal(t, "9007199254740992.00010000000000000001"), want: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalLeft := mustParseDecimal(t, test.left.String())
			originalRight := mustParseDecimal(t, test.right.String())
			if got := test.left.Cmp(test.right); got != test.want {
				t.Fatalf("%q.Cmp(%q) = %d, want %d", test.left, test.right, got, test.want)
			}
			// 与各自独立解析的原值比较，检查对齐小数位时是否误改原系数。
			if test.left.Cmp(originalLeft) != 0 || test.right.Cmp(originalRight) != 0 {
				t.Errorf("Cmp(%q, %q) changed an operand", originalLeft, originalRight)
			}
		})
	}
}

type decimalBoundCase struct {
	name    string
	value   string
	bound   string
	wantErr error
}

func TestDecimalAtLeast(t *testing.T) {
	testDecimalBounds(t, scalar.DecimalAtLeast, []decimalBoundCase{
		// 只比大数下限少最后一位小数，仍应被识别为低于下限。
		{name: "below bound", value: "9007199254740992.0001", bound: "9007199254740992.0002", wantErr: check.ErrInvalidValue},
		// 多写一个小数尾随零可验证下限按数值相等接受边界。
		{name: "equal to bound", value: "9007199254740992.00020", bound: "9007199254740992.0002"},
		// 只比大数下限多最后一位小数，仍应通过校验。
		{name: "above bound", value: "9007199254740992.0003", bound: "9007199254740992.0002"},
	})
}

func TestDecimalAtMost(t *testing.T) {
	testDecimalBounds(t, scalar.DecimalAtMost, []decimalBoundCase{
		// 只比大数上限少最后一位小数，仍应通过校验。
		{name: "below bound", value: "9007199254740992.0001", bound: "9007199254740992.0002"},
		// 小数位数不同而数值相等，验证上限允许等于边界。
		{name: "equal to bound", value: "9007199254740992.00020", bound: "9007199254740992.0002"},
		// 只比大数上限多最后一位小数，仍应被识别为高于上限。
		{name: "above bound", value: "9007199254740992.0003", bound: "9007199254740992.0002", wantErr: check.ErrInvalidValue},
	})
}

func TestDecimalInRange(t *testing.T) {
	const minimum = "9007199254740992.0002"
	const maximum = "9007199254740992.0004"
	tests := []struct {
		name    string
		value   string
		minimum string
		maximum string
		wantErr error
	}{
		// 小数末位等于下限，验证大数闭区间包含下端点。
		{name: "equal to lower bound", value: minimum, minimum: minimum, maximum: maximum},
		// 大数的末位在两端之间，验证精确小数仍可通过范围校验。
		{name: "inside bounds", value: "9007199254740992.0003", minimum: minimum, maximum: maximum},
		// 给上限补一个小数尾随零，验证大数闭区间包含数值相等的上端点。
		{name: "equal to upper bound", value: "9007199254740992.00040", minimum: minimum, maximum: maximum},
		// 与下限仅相差最后一位小数，仍应返回输入值错误。
		{name: "below lower bound", value: "9007199254740992.0001", minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 与上限仅相差最后一位小数，仍应返回输入值错误。
		{name: "above upper bound", value: "9007199254740992.0005", minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 上下限数值相等时构成单点区间，值等于该点时应通过。
		{name: "equal bounds with different scales", value: "1.0", minimum: "1.00", maximum: "1"},
		// 对调大数上下限，验证最后一位小数决定规则本身无效。
		{name: "reversed bounds", value: "9007199254740992.0003", minimum: maximum, maximum: minimum, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := mustParseDecimal(t, test.value)
			minimum := mustParseDecimal(t, test.minimum)
			maximum := mustParseDecimal(t, test.maximum)
			assertDecimalError(t, scalar.DecimalInRange(value, minimum, maximum), test.wantErr)
		})
	}
}

func TestDecimalScaleInRange(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		minimum int
		maximum int
		wantErr error
	}{
		// 整数的小数位为 0，验证允许以 0 作为下限。
		{name: "equal to lower bound", value: "42", minimum: 0, maximum: 2},
		// 一个小数位严格处于零到两位之间，应通过校验。
		{name: "inside bounds", value: "1.2", minimum: 0, maximum: 2},
		// 1.20 的原始小数位为 2，验证末尾的零计入上限。
		{name: "equal to upper bound", value: "1.20", minimum: 1, maximum: 2},
		// 上下限相等时，小数位数等于该点应通过校验。
		{name: "equal bounds", value: "1.20", minimum: 2, maximum: 2},
		// 整数的小数位小于正下限，应返回输入值错误。
		{name: "below lower bound", value: "1", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 用尾随零增加原始小数位，验证三位小数超过两位上限。
		{name: "above upper bound", value: "1.200", minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 下限为负且仍小于上限，单独验证负下限属于规则错误。
		{name: "negative lower bound", value: "1.20", minimum: -1, maximum: 2, wantErr: check.ErrInvalidRule},
		// 交换两个非负边界，验证区间顺序错误的分类。
		{name: "reversed bounds", value: "1.20", minimum: 3, maximum: 2, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := mustParseDecimal(t, test.value)
			assertDecimalError(t, scalar.DecimalScaleInRange(value, test.minimum, test.maximum), test.wantErr)
		})
	}
}

func TestDecimalPrecisionAtMost(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		maximum int
		wantErr error
	}{
		// 解析出的零精度为 1，验证最小合法精度上限包含该值。
		{name: "smallest maximum", value: "0", maximum: 1},
		// 输入精度为 2，验证低于精度上限时可以通过校验。
		{name: "below maximum", value: "1.2", maximum: 3},
		// 整数前导零排除在计数外，小数尾随零使精度正好为 3。
		{name: "equal to maximum", value: "001.20", maximum: 3},
		// 相同文本在精度上限为 2 时应失败，验证小数尾随零仍占精度。
		{name: "above maximum", value: "001.20", maximum: 2, wantErr: check.ErrInvalidValue},
		// 精度上限为零时规则无效，即使输入数值也是零。
		{name: "zero maximum", value: "0", maximum: 0, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := mustParseDecimal(t, test.value)
			assertDecimalError(t, scalar.DecimalPrecisionAtMost(value, test.maximum), test.wantErr)
		})
	}
}

func TestDecimalFits(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		precision int
		scale     int
		wantErr   error
	}{
		// 输入只需一位整数和两位小数，前导零及小数尾随零可在精确表示时省略。
		{name: "trim insignificant zeros", value: "0001.2300", precision: 3, scale: 2},
		// 小数全为零时，负整数可无损放入零小数位的表示。
		{name: "negative integer with a zero fraction", value: "-00123.000", precision: 3, scale: 0},
		// 精度全部留给小数，纯小数仍可精确表示。
		{name: "no integer capacity", value: "0.0123", precision: 4, scale: 4},
		// 带符号的零可放入仅一位小数的表示，原始文本和小数位数应保留。
		{name: "zero without integer capacity", value: "-000.0000", precision: 1, scale: 1},
		// 小数可全部省略，剩余两位整数超过一位整数容量。
		{name: "integer capacity exceeded", value: "10.0", precision: 2, scale: 1, wantErr: check.ErrInvalidValue},
		// 小数尾随零可省略，仍需保留第三个非零小数位。
		{name: "required fractional digits exceed scale", value: "1.2340", precision: 3, scale: 2, wantErr: check.ErrInvalidValue},
		// 精度为零违反类型规则，零数值也应返回规则错误。
		{name: "zero precision", value: "0", precision: 0, scale: 0, wantErr: check.ErrInvalidRule},
		// 精度有效时单独传入负小数位数，应返回规则错误。
		{name: "negative scale", value: "0", precision: 2, scale: -1, wantErr: check.ErrInvalidRule},
		// 小数位数比正精度大一位，应返回规则错误。
		{name: "scale greater than precision", value: "0", precision: 2, scale: 3, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := mustParseDecimal(t, test.value)
			original := mustParseDecimal(t, test.value)
			assertDecimalError(t, scalar.DecimalFits(value, test.precision, test.scale), test.wantErr)
			if value.String() != test.value || value.Scale() != original.Scale() || value.Cmp(original) != 0 {
				t.Errorf("DecimalFits changed the input: got %q with scale %d, want %q with scale %d and the original value", value, value.Scale(), test.value, original.Scale())
			}
		})
	}
}

func mustParseDecimal(t *testing.T, input string) scalar.Decimal {
	t.Helper()
	value, err := scalar.ParseDecimal(input)
	if err != nil {
		t.Fatalf("ParseDecimal(%q) = %v, want a valid test operand", input, err)
	}
	return value
}

func assertDecimalError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func testDecimalValue[T comparable](t *testing.T, read func(scalar.Decimal) T, tests []decimalValueCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := read(test.value); got != test.want {
				t.Errorf("value for %q = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func testDecimalBounds(t *testing.T, validate func(scalar.Decimal, scalar.Decimal) error, tests []decimalBoundCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := mustParseDecimal(t, test.value)
			bound := mustParseDecimal(t, test.bound)
			assertDecimalError(t, validate(value, bound), test.wantErr)
		})
	}
}
