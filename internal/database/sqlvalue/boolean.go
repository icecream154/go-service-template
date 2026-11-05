package sqlvalue

import "database/sql"

// ToNullBool 将布尔指针转换为 SQL 可空布尔值。
// nil 转换为 SQL NULL；非 nil 时保留原值，false 也标记为有效值。
func ToNullBool(value *bool) sql.NullBool {
	if value == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *value, Valid: true}
}

// FromNullBool 将 SQL 可空布尔值转换为独立布尔副本的指针。
// Valid 为 false 时返回 nil，忽略 Bool 中的值；有效 false 仍返回非 nil 指针。
func FromNullBool(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Bool
	return &result
}
