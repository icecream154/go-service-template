package sqlvalue_test

import (
	"database/sql"
	"testing"

	"go-service-template/internal/database/sqlvalue"
)

func TestToNullString(t *testing.T) {
	empty, raw := "", " 头像\t\x00"
	testToNull(t, sqlvalue.ToNullString, []toNullCase[string, sql.NullString]{
		// nil 指针代表未提供文本，验证写入 SQL NULL。
		{name: "nil", input: nil, want: sql.NullString{}},
		// 显式提供空串，验证它仍是有效值。
		{name: "valid empty string", input: &empty, want: sql.NullString{String: "", Valid: true}},
		// 在多字节文本两端放入空白和 NUL，验证原始字节完整保留。
		{name: "unmodified content", input: &raw, want: sql.NullString{String: " 头像\t\x00", Valid: true}},
	})
}

func TestFromNullString(t *testing.T) {
	empty, raw := "", " 头像\t\x00"
	testFromNull(t, sqlvalue.FromNullString, []fromNullCase[sql.NullString, string]{
		// 无效 SQL 值携带残留文本，验证只依据 Valid 判定缺失。
		{name: "invalid with residual text", input: sql.NullString{String: "旧值"}, want: nil},
		// 有效的空串应返回非 nil 指针，调用方可区分清空与缺失。
		{name: "valid empty string", input: sql.NullString{String: "", Valid: true}, want: &empty},
		// 文本带有多字节字符、空白和 NUL，验证读取保留原始字节。
		{name: "unmodified content", input: sql.NullString{String: " 头像\t\x00", Valid: true}, want: &raw},
	})
}
