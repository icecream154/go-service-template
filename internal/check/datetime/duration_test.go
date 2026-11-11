package datetime_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"go-service-template/internal/check"
	"go-service-template/internal/check/datetime"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr error
	}{
		// 裸文本零验证 Go 时长语法的零值特例。
		{name: "bare zero", input: "0"},
		// 带正号的多段时长验证各单位累加。
		{name: "compound duration", input: "+1h30m0.5s", want: time.Hour + 30*time.Minute + 500*time.Millisecond},
		// 带小数的负毫秒数验证精确转换为纳秒。
		{name: "negative fractional unit", input: "-1.5ms", want: -1500 * time.Microsecond},
		// 最小纳秒值验证有符号范围的下端点。
		{name: "minimum duration", input: "-9223372036854775808ns", want: math.MinInt64},
		// 最大纳秒值验证范围的上端点。
		{name: "maximum duration", input: "9223372036854775807ns", want: math.MaxInt64},
		// 只大一个纳秒的值超过表示范围，应报告输入错误。
		{name: "above maximum", input: "9223372036854775808ns", wantErr: check.ErrInvalidValue},
		// 只小一个纳秒的值验证负侧溢出。
		{name: "below minimum", input: "-9223372036854775809ns", wantErr: check.ErrInvalidValue},
		// 非零裸数字验证解析要求显式单位。
		{name: "missing unit", input: "3", wantErr: check.ErrInvalidValue},
		// 日历天不是固定时长文本中的单位。
		{name: "unsupported unit", input: "1d", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := datetime.ParseDuration(test.input)
			assertDurationError(t, err, test.wantErr)
			if test.wantErr == nil && got != test.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestDurationInRange(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Duration
		minimum time.Duration
		maximum time.Duration
		wantErr error
	}{
		// 负值等于下限验证闭区间接受负时长端点。
		{name: "equal to minimum", value: -time.Second, minimum: -time.Second, maximum: time.Second},
		// 零位于两侧端点之间，验证跨零的区间。
		{name: "inside range", value: 0, minimum: -time.Second, maximum: time.Second},
		// 正值等于上限验证闭区间接受上端点。
		{name: "equal to maximum", value: time.Second, minimum: -time.Second, maximum: time.Second},
		// 下限之前一个纳秒应报告输入错误。
		{name: "below minimum", value: -time.Second - time.Nanosecond, minimum: -time.Second, maximum: time.Second, wantErr: check.ErrInvalidValue},
		// 上限之后一个纳秒同样超过边界。
		{name: "above maximum", value: time.Second + time.Nanosecond, minimum: -time.Second, maximum: time.Second, wantErr: check.ErrInvalidValue},
		// 同值边界构成单点区间，验证零可以落入其中。
		{name: "equal bounds"},
		// 交换有效边界验证顺序错误属于规则配置错误。
		{name: "reversed bounds", minimum: time.Second, maximum: -time.Second, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDurationError(t, datetime.DurationInRange(test.value, test.minimum, test.maximum), test.wantErr)
		})
	}
}

func TestPositiveDuration(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Duration
		wantErr error
	}{
		// 一个纳秒是最小正时长，验证刚超过零即可通过。
		{name: "smallest positive", value: time.Nanosecond},
		// 零位于严格正数范围之外。
		{name: "zero", wantErr: check.ErrInvalidValue},
		// 负一个纳秒验证零以下也应报告输入错误。
		{name: "negative", value: -time.Nanosecond, wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDurationError(t, datetime.PositiveDuration(test.value), test.wantErr)
		})
	}
}

func TestDurationPrecision(t *testing.T) {
	tests := []struct {
		name       string
		value      time.Duration
		resolution time.Duration
		wantErr    error
	}{
		// 两个毫秒验证正时长是分辨率的整数倍时通过。
		{name: "positive multiple", value: 2 * time.Millisecond, resolution: time.Millisecond},
		// 对应负时长验证沿用有符号整除语义。
		{name: "negative multiple", value: -2 * time.Millisecond, resolution: time.Millisecond},
		// 零时长应能按任一正分辨率对齐。
		{name: "zero duration", resolution: time.Millisecond},
		// 增加一个纳秒使时长失去对齐，应报告输入错误。
		{name: "misaligned duration", value: time.Millisecond + time.Nanosecond, resolution: time.Millisecond, wantErr: check.ErrInvalidValue},
		// 零分辨率无法定义精度，应报告规则错误。
		{name: "zero resolution", value: time.Millisecond, wantErr: check.ErrInvalidRule},
		// 负分辨率虽可整除仍违反正值规则。
		{name: "negative resolution", value: time.Millisecond, resolution: -time.Millisecond, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDurationError(t, datetime.DurationPrecision(test.value, test.resolution), test.wantErr)
		})
	}
}

func assertDurationError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
