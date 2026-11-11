package sqlvalue_test

import (
	"database/sql"
	"math"
	"testing"

	"go-service-template/internal/database/sqlvalue"
)

type toNullCase[T any, N comparable] struct {
	name  string
	input *T
	want  N
}

func TestToNullInt16(t *testing.T) {
	zero, minimum, maximum := int16(0), int16(math.MinInt16), int16(math.MaxInt16)
	testToNull(t, sqlvalue.ToNullInt16, []toNullCase[int16, sql.NullInt16]{
		// nil 指针表示缺失，验证转换得到无效的 SQL 值。
		{name: "nil", input: nil, want: sql.NullInt16{}},
		// 显式提供零，验证零值仍标记为有效。
		{name: "valid zero", input: &zero, want: sql.NullInt16{Int16: 0, Valid: true}},
		// 选择 16 位有符号最小值，验证负数边界按原值写入。
		{name: "minimum", input: &minimum, want: sql.NullInt16{Int16: math.MinInt16, Valid: true}},
		// 选择 16 位有符号最大值，验证上界按原值写入。
		{name: "maximum", input: &maximum, want: sql.NullInt16{Int16: math.MaxInt16, Valid: true}},
	})
}

type fromNullCase[N any, T comparable] struct {
	name  string
	input N
	want  *T
}

func TestFromNullInt16(t *testing.T) {
	zero, minimum, maximum := int16(0), int16(math.MinInt16), int16(math.MaxInt16)
	testFromNull(t, sqlvalue.FromNullInt16, []fromNullCase[sql.NullInt16, int16]{
		// 无效值带有非零内容，验证 Valid 为 false 时丢弃该内容并返回 nil。
		{name: "invalid with residual value", input: sql.NullInt16{Int16: 7}, want: nil},
		// 显式标记零值有效，验证返回指向零的非 nil 指针。
		{name: "valid zero", input: sql.NullInt16{Int16: 0, Valid: true}, want: &zero},
		// 选择 16 位有符号最小值，验证读取时保留符号和下界。
		{name: "minimum", input: sql.NullInt16{Int16: math.MinInt16, Valid: true}, want: &minimum},
		// 选择 16 位有符号最大值，验证读取结果保留上界。
		{name: "maximum", input: sql.NullInt16{Int16: math.MaxInt16, Valid: true}, want: &maximum},
	})
}

func TestToNullInt32(t *testing.T) {
	zero, minimum, maximum := int32(0), int32(math.MinInt32), int32(math.MaxInt32)
	testToNull(t, sqlvalue.ToNullInt32, []toNullCase[int32, sql.NullInt32]{
		// nil 指针表示缺失，验证转换得到无效的 SQL 值。
		{name: "nil", input: nil, want: sql.NullInt32{}},
		// 显式提供零，验证零值仍标记为有效。
		{name: "valid zero", input: &zero, want: sql.NullInt32{Int32: 0, Valid: true}},
		// 选择 32 位有符号最小值，验证负数边界按原值写入。
		{name: "minimum", input: &minimum, want: sql.NullInt32{Int32: math.MinInt32, Valid: true}},
		// 选择 32 位有符号最大值，验证上界按原值写入。
		{name: "maximum", input: &maximum, want: sql.NullInt32{Int32: math.MaxInt32, Valid: true}},
	})
}

func TestFromNullInt32(t *testing.T) {
	zero, minimum, maximum := int32(0), int32(math.MinInt32), int32(math.MaxInt32)
	testFromNull(t, sqlvalue.FromNullInt32, []fromNullCase[sql.NullInt32, int32]{
		// 无效值带有非零内容，验证 Valid 为 false 时丢弃该内容并返回 nil。
		{name: "invalid with residual value", input: sql.NullInt32{Int32: 7}, want: nil},
		// 显式标记零值有效，验证返回指向零的非 nil 指针。
		{name: "valid zero", input: sql.NullInt32{Int32: 0, Valid: true}, want: &zero},
		// 选择 32 位有符号最小值，验证读取时保留符号和下界。
		{name: "minimum", input: sql.NullInt32{Int32: math.MinInt32, Valid: true}, want: &minimum},
		// 选择 32 位有符号最大值，验证读取结果保留上界。
		{name: "maximum", input: sql.NullInt32{Int32: math.MaxInt32, Valid: true}, want: &maximum},
	})
}

func TestToNullInt64(t *testing.T) {
	zero, minimum, maximum := int64(0), int64(math.MinInt64), int64(math.MaxInt64)
	testToNull(t, sqlvalue.ToNullInt64, []toNullCase[int64, sql.NullInt64]{
		// nil 指针表示缺失，验证转换得到无效的 SQL 值。
		{name: "nil", input: nil, want: sql.NullInt64{}},
		// 显式提供零，验证零值仍标记为有效。
		{name: "valid zero", input: &zero, want: sql.NullInt64{Int64: 0, Valid: true}},
		// 选择 64 位有符号最小值，验证负数边界按原值写入。
		{name: "minimum", input: &minimum, want: sql.NullInt64{Int64: math.MinInt64, Valid: true}},
		// 选择 64 位有符号最大值，验证上界按原值写入。
		{name: "maximum", input: &maximum, want: sql.NullInt64{Int64: math.MaxInt64, Valid: true}},
	})
}

func TestFromNullInt64(t *testing.T) {
	zero, minimum, maximum := int64(0), int64(math.MinInt64), int64(math.MaxInt64)
	testFromNull(t, sqlvalue.FromNullInt64, []fromNullCase[sql.NullInt64, int64]{
		// 无效值带有非零内容，验证 Valid 为 false 时丢弃该内容并返回 nil。
		{name: "invalid with residual value", input: sql.NullInt64{Int64: 7}, want: nil},
		// 显式标记零值有效，验证返回指向零的非 nil 指针。
		{name: "valid zero", input: sql.NullInt64{Int64: 0, Valid: true}, want: &zero},
		// 选择 64 位有符号最小值，验证读取时保留符号和下界。
		{name: "minimum", input: sql.NullInt64{Int64: math.MinInt64, Valid: true}, want: &minimum},
		// 选择 64 位有符号最大值，验证读取结果保留上界。
		{name: "maximum", input: sql.NullInt64{Int64: math.MaxInt64, Valid: true}, want: &maximum},
	})
}

func TestToNullByte(t *testing.T) {
	zero, maximum := byte(0), byte(math.MaxUint8)
	testToNull(t, sqlvalue.ToNullByte, []toNullCase[byte, sql.NullByte]{
		// nil 指针表示缺失，验证转换得到无效的 SQL 值。
		{name: "nil", input: nil, want: sql.NullByte{}},
		// 显式提供零，验证零值仍标记为有效。
		{name: "valid zero", input: &zero, want: sql.NullByte{Byte: 0, Valid: true}},
		// 选择字节上界 255，验证最高位不会被当成符号位。
		{name: "maximum", input: &maximum, want: sql.NullByte{Byte: math.MaxUint8, Valid: true}},
	})
}

func TestFromNullByte(t *testing.T) {
	zero, maximum := byte(0), byte(math.MaxUint8)
	testFromNull(t, sqlvalue.FromNullByte, []fromNullCase[sql.NullByte, byte]{
		// 无效值带有非零内容，验证 Valid 为 false 时丢弃该内容并返回 nil。
		{name: "invalid with residual value", input: sql.NullByte{Byte: 7}, want: nil},
		// 显式标记零值有效，验证返回指向零的非 nil 指针。
		{name: "valid zero", input: sql.NullByte{Byte: 0, Valid: true}, want: &zero},
		// 选择字节上界 255，验证返回指针保留无符号上界。
		{name: "maximum", input: sql.NullByte{Byte: math.MaxUint8, Valid: true}, want: &maximum},
	})
}

func testToNull[T any, N comparable](t *testing.T, convert func(*T) N, tests []toNullCase[T, N]) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := convert(test.input); got != test.want {
				t.Errorf("conversion = %#v, want %#v", got, test.want)
			}
		})
	}
}

func testFromNull[N any, T comparable](t *testing.T, convert func(N) *T, tests []fromNullCase[N, T]) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := convert(test.input)
			if test.want == nil {
				if got != nil {
					t.Errorf("conversion = pointer to %#v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("conversion = nil, want pointer to %#v", *test.want)
			}
			if *got != *test.want {
				t.Errorf("converted value = %#v, want %#v", *got, *test.want)
			}

			// 同一输入的两次转换各自返回副本，调用者可以独立修改返回值。
			if second := convert(test.input); second == nil || second == got {
				t.Error("two conversions must return distinct non-nil pointers")
			}
		})
	}
}
