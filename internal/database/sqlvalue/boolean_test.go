package sqlvalue_test

import (
	"database/sql"
	"testing"

	"go-service-template/internal/database/sqlvalue"
)

func TestToNullBool(t *testing.T) {
	falseValue, trueValue := false, true
	testToNull(t, sqlvalue.ToNullBool, []toNullCase[bool, sql.NullBool]{
		// nil 指针代表未提供布尔值，验证转换结果为 SQL NULL。
		{name: "nil", input: nil, want: sql.NullBool{}},
		// 显式提供 false，验证零值与 SQL NULL 保持区别。
		{name: "valid false", input: &falseValue, want: sql.NullBool{Bool: false, Valid: true}},
		// 显式提供 true，验证写入的有效值保留真值。
		{name: "valid true", input: &trueValue, want: sql.NullBool{Bool: true, Valid: true}},
	})
}

func TestFromNullBool(t *testing.T) {
	falseValue, trueValue := false, true
	testFromNull(t, sqlvalue.FromNullBool, []fromNullCase[sql.NullBool, bool]{
		// 无效 SQL 值中残留 true，验证读取结果仍为 nil。
		{name: "invalid with residual true", input: sql.NullBool{Bool: true}, want: nil},
		// 有效的 false 应返回非 nil 指针，调用方可区分显式假值和缺失。
		{name: "valid false", input: sql.NullBool{Bool: false, Valid: true}, want: &falseValue},
		// 有效的 true 应按原值读出为独立副本。
		{name: "valid true", input: sql.NullBool{Bool: true, Valid: true}, want: &trueValue},
	})
}
