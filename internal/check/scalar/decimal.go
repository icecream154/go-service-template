package scalar

import (
	"fmt"
	"math/big"
	"strings"

	"go-service-template/internal/check"
)

// Decimal 是不可变的精确十进制值，系数不向外暴露；零值表示整数 0。
// Scale 保留输入中的尾随小数零，Precision 不计符号和整数前导零，但包含所有小数位。
type Decimal struct {
	text        string
	coefficient *big.Int
	scale       int
	precision   int
}

// ParseDecimal 接受 [+-]?[0-9]+(\.[0-9]+)?，允许整数前导零，不接受空白、指数、.5 或 1.。
// 返回值保留原始文本及小数位，不舍入、不截断；调用方可先限制文本大小以约束解析成本。
func ParseDecimal(value string) (Decimal, error) {
	if value == "" {
		return Decimal{}, check.NewViolation("decimal.parse", "must be decimal text with digits on both sides of an optional point")
	}
	start := 0
	if value[0] == '+' || value[0] == '-' {
		start++
	}
	if start == len(value) {
		return Decimal{}, check.NewViolation("decimal.parse", "must contain decimal digits")
	}
	point := -1
	for i := start; i < len(value); i++ {
		if value[i] == '.' && point == -1 {
			point = i
			continue
		}
		if value[i] < '0' || value[i] > '9' {
			return Decimal{}, check.NewViolation("decimal.parse", "must contain only an optional sign, decimal digits, and one optional decimal point")
		}
	}
	if point == start || point == len(value)-1 {
		return Decimal{}, check.NewViolation("decimal.parse", "must contain digits on both sides of the decimal point")
	}
	integerEnd := len(value)
	scale := 0
	if point >= 0 {
		integerEnd = point
		scale = len(value) - point - 1
	}
	integerDigits := len(strings.TrimLeft(value[start:integerEnd], "0"))
	precision := integerDigits + scale
	if precision == 0 {
		precision = 1
	}
	digits := value[start:integerEnd]
	if point >= 0 {
		digits += value[point+1:]
	}
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, check.NewViolation("decimal.parse", "must contain valid decimal digits")
	}
	if value[0] == '-' {
		coefficient.Neg(coefficient)
	}
	return Decimal{text: value, coefficient: coefficient, scale: scale, precision: precision}, nil
}

// String 返回解析时的原始文本；零值返回 0，不隐式去掉前导正号或小数尾随零。
func (value Decimal) String() string {
	if value.text == "" {
		return "0"
	}
	return value.text
}

// Scale 返回小数位数，保留输入尾随零。
func (value Decimal) Scale() int { return value.scale }

// Precision 返回去掉整数前导零后的整数位数加小数位数，至少为 1。
func (value Decimal) Precision() int {
	if value.precision == 0 {
		return 1
	}
	return value.precision
}

// Sign 返回 -1、0、1；不同写法的正负零均返回 0。
func (value Decimal) Sign() int {
	if value.coefficient == nil {
		return 0
	}
	return value.coefficient.Sign()
}

// Cmp 精确比较两个十进制值，返回 -1、0、1；不会修改任一值。
func (value Decimal) Cmp(other Decimal) int {
	left := value.coefficientCopy()
	right := other.coefficientCopy()
	if value.scale < other.scale {
		left.Mul(left, decimalPower(other.scale-value.scale))
	} else if value.scale > other.scale {
		right.Mul(right, decimalPower(value.scale-other.scale))
	}
	return left.Cmp(right)
}

// DecimalAtLeast 检查精确十进制下限，允许等于 minimum。
func DecimalAtLeast(value, minimum Decimal) error {
	if value.Cmp(minimum) < 0 {
		return check.NewViolation("decimal.minimum", "must be greater than or equal to the decimal minimum")
	}
	return nil
}

// DecimalAtMost 检查精确十进制上限，允许等于 maximum。
func DecimalAtMost(value, maximum Decimal) error {
	if value.Cmp(maximum) > 0 {
		return check.NewViolation("decimal.maximum", "must be less than or equal to the decimal maximum")
	}
	return nil
}

// DecimalInRange 检查精确十进制闭区间，不经过 float64 转换。
func DecimalInRange(value, minimum, maximum Decimal) error {
	if minimum.Cmp(maximum) > 0 {
		return check.NewRuleError("decimal.range", "minimum must not exceed maximum")
	}
	if value.Cmp(minimum) < 0 || value.Cmp(maximum) > 0 {
		return check.NewViolation("decimal.range", "must be within the decimal bounds inclusive")
	}
	return nil
}

// DecimalScaleInRange 检查原始小数位数闭区间，尾随零参与计算。
func DecimalScaleInRange(value Decimal, minimum, maximum int) error {
	if minimum < 0 || minimum > maximum {
		return check.NewRuleError("decimal.scale", "scale bounds must be nonnegative and ordered")
	}
	if value.Scale() < minimum || value.Scale() > maximum {
		return check.NewViolation("decimal.scale", fmt.Sprintf("must have between %d and %d fractional digits inclusive", minimum, maximum))
	}
	return nil
}

// DecimalPrecisionAtMost 检查总精度上限；计数保留小数尾随零，maximum 至少为 1。
func DecimalPrecisionAtMost(value Decimal, maximum int) error {
	if maximum < 1 {
		return check.NewRuleError("decimal.precision", "maximum precision must be positive")
	}
	if value.Precision() > maximum {
		return check.NewViolation("decimal.precision", fmt.Sprintf("must have at most %d total digits", maximum))
	}
	return nil
}

// DecimalFits 检查能否不舍入地放入指定精度和小数位的十进制表示。
// precision 至少为 1，scale 在 0～precision 内；整数位最多 precision-scale，必要小数位最多 scale。
// 数值比较不计整数前导零与小数尾随零，例如 1.2300 可精确放入 precision=3、scale=2。
// 零不占用整数容量，因此可表示于 precision=scale 的类型；原始文本及 Scale 均不修改。
// 需要约束输入文本保留的小数位数时，使用 DecimalScaleInRange。
func DecimalFits(value Decimal, precision, scale int) error {
	if precision < 1 || scale < 0 || scale > precision {
		return check.NewRuleError("decimal.fits", "precision must be positive and scale must be between zero and precision")
	}
	integerDigits := value.Precision() - value.Scale()
	necessaryScale := value.Scale()
	if value.Sign() == 0 {
		integerDigits = 0
		necessaryScale = 0
	} else {
		// 只扫描原文本末尾，最多访问原始小数位数，不反复对大整数作除法。
		for end := len(value.text); necessaryScale > 0 && end > 0 && value.text[end-1] == '0'; end-- {
			necessaryScale--
		}
	}
	if integerDigits > precision-scale || necessaryScale > scale {
		return check.NewViolation("decimal.fits", "must fit the decimal precision and scale without rounding")
	}
	return nil
}

func (value Decimal) coefficientCopy() *big.Int {
	if value.coefficient == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(value.coefficient)
}

func decimalPower(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}
