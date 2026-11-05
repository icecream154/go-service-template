package datetime

import (
	"time"

	"go-service-template/internal/check"
)

// ParseDuration 接受 Go 时长文本，包括正负号和显式单位；不猜测裸数字的单位。
// 采用 time.ParseDuration 语法，特殊的裸文本 "0" 表示零时长。
func ParseDuration(value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, check.NewViolation("duration", "must be a supported duration within the int64 nanosecond range")
	}
	return parsed, nil
}

// DurationInRange 检查包含两端点的时长范围；负时长是否允许由边界决定。
func DurationInRange(value, minimum, maximum time.Duration) error {
	if minimum > maximum {
		return check.NewRuleError("duration_range", "minimum must not exceed maximum")
	}
	if value < minimum || value > maximum {
		return check.NewViolation("duration_range", "must be within the inclusive duration range")
	}
	return nil
}

// PositiveDuration 要求时长严格大于零。
func PositiveDuration(value time.Duration) error {
	if value <= 0 {
		return check.NewViolation("positive_duration", "must be a positive duration")
	}
	return nil
}

// DurationPrecision 要求时长是正 resolution 的整数倍，负时长采用相同整除语义。
func DurationPrecision(value, resolution time.Duration) error {
	if resolution <= 0 {
		return check.NewRuleError("duration_precision", "resolution must be positive")
	}
	if value%resolution != 0 {
		return check.NewViolation("duration_precision", "must be a multiple of the duration resolution")
	}
	return nil
}
