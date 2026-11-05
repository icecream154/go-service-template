// Package sqlvalue 提供 Go 指针与 database/sql 可空类型之间的双向转换。
// 转换保留有效零值及原始内容，不执行校验、填充默认值或调整时间。
package sqlvalue

import "database/sql"

// ToNullString 将字符串指针转换为 SQL 可空字符串，不校验或修改字符串内容。
// nil 转换为 SQL NULL；非 nil 时 Valid 为 true，包括指向空字符串的情况。
func ToNullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

// FromNullString 将 SQL 可空字符串转换为独立字符串副本的指针。
// Valid 为 false 时返回 nil，忽略 String 中的值；有效空字符串仍返回非 nil 指针。
func FromNullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
