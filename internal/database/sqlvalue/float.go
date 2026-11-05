package sqlvalue

import "database/sql"

// ToNullFloat64 将 float64 指针转换为 SQL 可空浮点数，不校验或改变数值。
// nil 对应 SQL NULL；非 nil 时 Valid 为 true，保留 0、负零、NaN 和无穷值。
func ToNullFloat64(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

// FromNullFloat64 在 Valid 为 false 时返回 nil，不使用内部残留的浮点值。
// Valid 为 true 时返回原值的独立副本，保留 0、负零、NaN 和无穷值。
func FromNullFloat64(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}
