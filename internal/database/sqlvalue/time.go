package sqlvalue

import (
	"database/sql"
	"time"
)

// ToNullTime 将时间指针转换为 SQL 可空时间。
// nil 转换为 SQL NULL；非 nil 时保留完整时间值，包括零时间，不转换时区或截断精度。
func ToNullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

// FromNullTime 将 SQL 可空时间转换为独立时间副本的指针。
// Valid 为 false 时返回 nil，忽略 Time 中的值；有效零时间仍返回非 nil 指针。
// 时间值、时区及精度保持原样。
func FromNullTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
