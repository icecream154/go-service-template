// Package datetime 检查时间和时长，所有参考时间、时区与规则均由调用方显式提供。
// 本包不读取当前时间，不改变输入时区，不访问外部资源。
package datetime

import (
	"strconv"
	"time"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// TimestampUnit 明确整型 Unix 时间戳的单位，不根据数字长度猜测。
type TimestampUnit uint8

const (
	UnixSeconds TimestampUnit = iota
	UnixMilliseconds
	UnixMicroseconds
	UnixNanoseconds
)

// ParseTime 使用指定布局和时区解析文本；文本自身的时区按 Go 解析语义处理。
// location 不能为 nil；普通解析不承诺文本是唯一规范表示。
func ParseTime(value, layout string, location *time.Location) (time.Time, error) {
	if layout == "" || !utf8.ValidString(layout) || location == nil {
		return time.Time{}, check.NewRuleError("time", "a valid non-empty layout and location are required")
	}
	if !utf8.ValidString(value) {
		return time.Time{}, check.NewViolation("time", "must be valid UTF-8 text")
	}
	parsed, err := time.ParseInLocation(layout, value, location)
	if err != nil {
		return time.Time{}, check.NewViolation("time", "must match the specified time layout")
	}
	return parsed, nil
}

// ParseCanonicalTime 额外要求解析结果按同一布局格式化后与原文完全相同。
// 因此会拒绝可解析但被布局省略的额外小数秒或不同的文本表示。
func ParseCanonicalTime(value, layout string, location *time.Location) (time.Time, error) {
	parsed, err := ParseTime(value, layout, location)
	if err != nil {
		return time.Time{}, err
	}
	if parsed.Format(layout) != value {
		return time.Time{}, check.NewViolation("canonical_time", "must use the canonical text representation for the layout")
	}
	return parsed, nil
}

// UnixTime 将给定单位的整数转换为 UTC 时刻；不经过乘法放大而造成中间溢出。
// Unix 秒数超出 time.Time 可表示的内部范围时返回输入错误。
func UnixTime(value int64, unit TimestampUnit) (time.Time, error) {
	var seconds, nanoseconds int64
	switch unit {
	case UnixSeconds:
		seconds = value
	case UnixMilliseconds:
		seconds, nanoseconds = value/1_000, value%1_000*1_000_000
	case UnixMicroseconds:
		seconds, nanoseconds = value/1_000_000, value%1_000_000*1_000
	case UnixNanoseconds:
		seconds, nanoseconds = value/1_000_000_000, value%1_000_000_000
	default:
		return time.Time{}, check.NewRuleError("unix_time", "unsupported timestamp unit")
	}
	parsed := time.Unix(seconds, nanoseconds).UTC()
	// 极大的正秒数可能溢出 time.Time 内部从公元 1 年开始的秒计数。
	// 这种值即使 Unix() 可以反向绕回，也会错误落在 Unix 纪元之前。
	if seconds > 0 && parsed.Before(time.Unix(0, 0)) {
		return time.Time{}, check.NewViolation("unix_time", "timestamp exceeds the supported time range")
	}
	return parsed, nil
}

// ParseUnixTime 解析十进制 int64 时间戳后按明确单位转换；允许前导正号和前导零。
// 不接受前后空白、小数、科学计数法或超出 int64 的文本。
func ParseUnixTime(value string, unit TimestampUnit) (time.Time, error) {
	if unit > UnixNanoseconds {
		return time.Time{}, check.NewRuleError("unix_time", "unsupported timestamp unit")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, check.NewViolation("unix_time", "must be a decimal 64-bit integer timestamp")
	}
	return UnixTime(parsed, unit)
}

// NonZeroTime 拒绝 Go 的零时间，不把零时间自动解释成当前时间。
func NonZeroTime(value time.Time) error {
	if value.IsZero() {
		return check.NewViolation("non_zero_time", "must not be the zero time")
	}
	return nil
}

// TimeInRange 检查闭区间 [minimum, maximum]，按实际时刻比较而非时区或结构体相等性。
func TimeInRange(value, minimum, maximum time.Time) error {
	if minimum.After(maximum) {
		return check.NewRuleError("time_range", "minimum must not be after maximum")
	}
	if value.Before(minimum) || value.After(maximum) {
		return check.NewViolation("time_range", "must be within the inclusive time range")
	}
	return nil
}

// Before 要求 value 严格早于 reference，不包含相同的时刻。
func Before(value, reference time.Time) error {
	if !value.Before(reference) {
		return check.NewViolation("time_before", "must be before the reference time")
	}
	return nil
}

// After 要求 value 严格晚于 reference，不包含相同的时刻。
func After(value, reference time.Time) error {
	if !value.After(reference) {
		return check.NewViolation("time_after", "must be after the reference time")
	}
	return nil
}

// WithinPast 要求 value 位于 [reference-maximumAge, reference]，按固定时长而非日历天计算。
// 参考时间与时长无法构成可表示的边界时，返回规则错误，不接受 Add 的饱和值。
func WithinPast(value, reference time.Time, maximumAge time.Duration) error {
	if maximumAge < 0 {
		return check.NewRuleError("time_past", "maximum age must not be negative")
	}
	minimum := reference.Add(-maximumAge)
	if !minimum.Add(maximumAge).Equal(reference) {
		return check.NewRuleError("time_past", "reference and maximum age exceed the representable time range")
	}
	return TimeInRange(value, minimum, reference)
}

// WithinFuture 要求 value 位于 [reference, reference+maximumDelay]，不读取系统时钟。
// 参考时间与时长无法构成可表示的边界时，返回规则错误。
func WithinFuture(value, reference time.Time, maximumDelay time.Duration) error {
	if maximumDelay < 0 {
		return check.NewRuleError("time_future", "maximum delay must not be negative")
	}
	maximum := reference.Add(maximumDelay)
	if !maximum.Add(-maximumDelay).Equal(reference) {
		return check.NewRuleError("time_future", "reference and maximum delay exceed the representable time range")
	}
	return TimeInRange(value, reference, maximum)
}

// TimePrecision 要求时刻可按 resolution 对齐，使用 Go Truncate 的绝对时间语义。
// resolution 必须大于零；检查不截断或改写输入。
func TimePrecision(value time.Time, resolution time.Duration) error {
	if resolution <= 0 {
		return check.NewRuleError("time_precision", "resolution must be positive")
	}
	if !value.Equal(value.Truncate(resolution)) {
		return check.NewViolation("time_precision", "must align with the specified time resolution")
	}
	return nil
}
