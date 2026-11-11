package sqlvalue_test

import (
	"database/sql"
	"math"
	"testing"

	"go-service-template/internal/database/sqlvalue"
)

func TestToNullFloat64(t *testing.T) {
	zero, fractional := 0.0, -1.25
	negativeZero := math.Copysign(0, -1)
	nan := math.Float64frombits(0x7ff8000000000042)
	positiveInfinity, negativeInfinity := math.Inf(1), math.Inf(-1)
	tests := []struct {
		name  string
		input *float64
		want  sql.NullFloat64
	}{
		// nil 指针代表缺失，验证转换为无效 SQL 值。
		{name: "nil", input: nil, want: sql.NullFloat64{}},
		// 显式提供正零，验证零值仍标记为有效。
		{name: "valid zero", input: &zero, want: sql.NullFloat64{Float64: 0, Valid: true}},
		// 负零与正零数值相等，验证位模式仍保留负号。
		{name: "negative zero", input: &negativeZero, want: sql.NullFloat64{Float64: negativeZero, Valid: true}},
		// 使用带符号的小数，验证写入时保留符号及小数部分。
		{name: "fractional value", input: &fractional, want: sql.NullFloat64{Float64: -1.25, Valid: true}},
		// 选择带指定载荷的 NaN，验证写入保留完整位模式。
		{name: "NaN", input: &nan, want: sql.NullFloat64{Float64: nan, Valid: true}},
		// 选择正无穷，验证转换保留无穷和正号。
		{name: "positive infinity", input: &positiveInfinity, want: sql.NullFloat64{Float64: math.Inf(1), Valid: true}},
		// 选择负无穷，验证转换保留无穷和负号。
		{name: "negative infinity", input: &negativeInfinity, want: sql.NullFloat64{Float64: math.Inf(-1), Valid: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sqlvalue.ToNullFloat64(test.input)
			if got.Valid != test.want.Valid {
				t.Errorf("conversion.Valid = %t, want %t", got.Valid, test.want.Valid)
			}
			assertFloat64Bits(t, got.Float64, test.want.Float64)
		})
	}
}

func TestFromNullFloat64(t *testing.T) {
	zero, fractional := 0.0, -1.25
	negativeZero := math.Copysign(0, -1)
	nan := math.Float64frombits(0x7ff8000000000042)
	positiveInfinity, negativeInfinity := math.Inf(1), math.Inf(-1)
	tests := []struct {
		name  string
		input sql.NullFloat64
		want  *float64
	}{
		// 无效 SQL 值携带 NaN，验证读取时忽略残留位模式并返回 nil。
		{name: "invalid with residual NaN", input: sql.NullFloat64{Float64: nan}, want: nil},
		// 有效的正零应读为指向零的非 nil 指针。
		{name: "valid zero", input: sql.NullFloat64{Float64: 0, Valid: true}, want: &zero},
		// 负零与正零数值相等，验证读取后负号位仍保留。
		{name: "negative zero", input: sql.NullFloat64{Float64: negativeZero, Valid: true}, want: &negativeZero},
		// 使用带符号的小数，验证读取时保留符号及小数部分。
		{name: "fractional value", input: sql.NullFloat64{Float64: -1.25, Valid: true}, want: &fractional},
		// 选择带指定载荷的 NaN，验证读回完整位模式。
		{name: "NaN", input: sql.NullFloat64{Float64: nan, Valid: true}, want: &nan},
		// 选择正无穷，验证返回有效指针及正号。
		{name: "positive infinity", input: sql.NullFloat64{Float64: math.Inf(1), Valid: true}, want: &positiveInfinity},
		// 选择负无穷，验证返回有效指针及负号。
		{name: "negative infinity", input: sql.NullFloat64{Float64: math.Inf(-1), Valid: true}, want: &negativeInfinity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sqlvalue.FromNullFloat64(test.input)
			if test.want == nil {
				if got != nil {
					t.Errorf("conversion = pointer to %v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("conversion = nil, want pointer to %v", *test.want)
			}
			assertFloat64Bits(t, *got, *test.want)

			// 对同一 SQL 值重复转换，两次结果应指向独立的存储。
			if second := sqlvalue.FromNullFloat64(test.input); second == nil || second == got {
				t.Error("two conversions must return distinct non-nil pointers")
			}
		})
	}
}

func assertFloat64Bits(t *testing.T, got, want float64) {
	t.Helper()

	gotBits, wantBits := math.Float64bits(got), math.Float64bits(want)
	if gotBits != wantBits {
		t.Errorf("conversion = %v (%#016x), want %v (%#016x)", got, gotBits, want, wantBits)
	}
}
