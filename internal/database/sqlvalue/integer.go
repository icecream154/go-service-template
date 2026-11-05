package sqlvalue

import "database/sql"

// ToNullInt16 将 int16 指针转换为 SQL 可空整数，不改变数值。
// nil 对应 SQL NULL；非 nil 时 Valid 为 true，包括指向 0 的情况。
func ToNullInt16(value *int16) sql.NullInt16 {
	if value == nil {
		return sql.NullInt16{}
	}
	return sql.NullInt16{Int16: *value, Valid: true}
}

// FromNullInt16 在 Valid 为 false 时返回 nil，不使用内部残留的整数值。
// Valid 为 true 时返回数值的独立副本，包括有效的 0。
func FromNullInt16(value sql.NullInt16) *int16 {
	if !value.Valid {
		return nil
	}
	result := value.Int16
	return &result
}

// ToNullInt32 将 int32 指针转换为 SQL 可空整数，不改变数值。
// nil 对应 SQL NULL；非 nil 时 Valid 为 true，包括指向 0 的情况。
func ToNullInt32(value *int32) sql.NullInt32 {
	if value == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *value, Valid: true}
}

// FromNullInt32 在 Valid 为 false 时返回 nil，不使用内部残留的整数值。
// Valid 为 true 时返回数值的独立副本，包括有效的 0。
func FromNullInt32(value sql.NullInt32) *int32 {
	if !value.Valid {
		return nil
	}
	result := value.Int32
	return &result
}

// ToNullInt64 将 int64 指针转换为 SQL 可空整数，不改变数值。
// nil 对应 SQL NULL；非 nil 时 Valid 为 true，包括指向 0 的情况。
func ToNullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

// FromNullInt64 在 Valid 为 false 时返回 nil，不使用内部残留的整数值。
// Valid 为 true 时返回数值的独立副本，包括有效的 0。
func FromNullInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

// ToNullByte 将 byte 指针转换为 SQL 可空字节，不改变数值。
// nil 对应 SQL NULL；非 nil 时 Valid 为 true，包括指向 0 的情况。
func ToNullByte(value *byte) sql.NullByte {
	if value == nil {
		return sql.NullByte{}
	}
	return sql.NullByte{Byte: *value, Valid: true}
}

// FromNullByte 在 Valid 为 false 时返回 nil，不使用内部残留的字节值。
// Valid 为 true 时返回数值的独立副本，包括有效的 0。
func FromNullByte(value sql.NullByte) *byte {
	if !value.Valid {
		return nil
	}
	result := value.Byte
	return &result
}
