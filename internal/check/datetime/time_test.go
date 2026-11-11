package datetime_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"go-service-template/internal/check"
	"go-service-template/internal/check/datetime"
)

type timeParseCase struct {
	name     string
	input    string
	layout   string
	location *time.Location
	want     time.Time
	wantErr  error
}

func TestParseTime(t *testing.T) {
	const layout = "2006-01-02 15:04:05"
	east := time.FixedZone("UTC+08", 8*60*60)
	testTimeParser(t, datetime.ParseTime, []timeParseCase{
		// 无偏移的闰日文本验证日期规则及默认使用指定时区。
		{name: "unqualified time in provided location", input: "2024-02-29 01:02:03", layout: layout, location: east, want: time.Date(2024, 2, 29, 1, 2, 3, 0, east)},
		// 文本偏移与指定时区不同，验证采用文本中的偏移。
		{name: "explicit offset", input: "2024-02-29T01:02:03-03:00", layout: time.RFC3339, location: east, want: time.Date(2024, 2, 29, 1, 2, 3, 0, time.FixedZone("UTC-03", -3*60*60))},
		// Go 的普通解析允许布局未显式写出的小数秒。
		{name: "extra fractional seconds", input: "2024-02-29 01:02:03.5", layout: layout, location: time.UTC, want: time.Date(2024, 2, 29, 1, 2, 3, 500_000_000, time.UTC)},
		// 非闰年的二月二十九日验证日期超界为输入错误。
		{name: "invalid calendar date", input: "2023-02-29 01:02:03", layout: layout, location: time.UTC, wantErr: check.ErrInvalidValue},
		// 用不完整的 UTF-8 验证输入编码单独校验。
		{name: "invalid input UTF-8", input: "\xff", layout: layout, location: time.UTC, wantErr: check.ErrInvalidValue},
		// 空布局表示无法解析的规则配置。
		{name: "empty layout", input: "2024-02-29", location: time.UTC, wantErr: check.ErrInvalidRule},
		// 非 UTF-8 布局属于规则错误。
		{name: "invalid layout UTF-8", input: "2024-02-29", layout: "\xff", location: time.UTC, wantErr: check.ErrInvalidRule},
		// 有效文本配 nil 时区，验证调用方必须显式指定位置。
		{name: "nil location", input: "2024-02-29 01:02:03", layout: layout, wantErr: check.ErrInvalidRule},
	})
}

func TestParseCanonicalTime(t *testing.T) {
	const layout = "2006-01-02 15:04:05"
	testTimeParser(t, datetime.ParseCanonicalTime, []timeParseCase{
		// 严格符合布局的文本验证可正常往返格式化。
		{name: "exact format", input: "2024-02-29 01:02:03", layout: layout, location: time.UTC, want: time.Date(2024, 2, 29, 1, 2, 3, 0, time.UTC)},
		// 额外小数秒能被普通解析接受，验证规范入口要求往返文本完全一致。
		{name: "extra fractional seconds", input: "2024-02-29 01:02:03.5", layout: layout, location: time.UTC, wantErr: check.ErrInvalidValue},
		// 无效月份验证规范入口沿用输入错误分类。
		{name: "invalid date", input: "2024-13-01 01:02:03", layout: layout, location: time.UTC, wantErr: check.ErrInvalidValue},
		// 空布局验证下层规则错误得到保留。
		{name: "invalid rule", input: "2024-02-29", location: time.UTC, wantErr: check.ErrInvalidRule},
	})
}

type timestampCase[T int64 | string] struct {
	name    string
	input   T
	unit    datetime.TimestampUnit
	want    time.Time
	wantErr error
}

func TestUnixTime(t *testing.T) {
	testTimestamp(t, datetime.UnixTime, []timestampCase[int64]{
		// 零秒表示 Unix 纪元且结果使用 UTC。
		{name: "epoch", unit: datetime.UnixSeconds, want: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)},
		// 负一秒验证秒时间戳可表示纪元前的时刻。
		{name: "negative seconds", input: -1, unit: datetime.UnixSeconds, want: time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC)},
		// 负一毫秒验证除法余数会正确跨过秒边界。
		{name: "negative milliseconds", input: -1, unit: datetime.UnixMilliseconds, want: time.Date(1969, 12, 31, 23, 59, 59, 999_000_000, time.UTC)},
		// 一秒多一微秒验证秒和微秒余量同时保留。
		{name: "microsecond remainder", input: 1_000_001, unit: datetime.UnixMicroseconds, want: time.Date(1970, 1, 1, 0, 0, 1, 1_000, time.UTC)},
		// 一秒多一纳秒验证最高精度的余量。
		{name: "nanosecond remainder", input: 1_000_000_001, unit: datetime.UnixNanoseconds, want: time.Date(1970, 1, 1, 0, 0, 1, 1, time.UTC)},
		// 最大 int64 毫秒仍可表示，验证拆分不会先放大而溢出。
		{name: "maximum milliseconds", input: math.MaxInt64, unit: datetime.UnixMilliseconds, want: time.Unix(9_223_372_036_854_775, 807_000_000).UTC()},
		// 同一个最大整数按秒解释会溢出内部时间范围。
		{name: "seconds overflow", input: math.MaxInt64, unit: datetime.UnixSeconds, wantErr: check.ErrInvalidValue},
		// 未知单位应归类为规则配置错误。
		{name: "unknown unit", input: 1, unit: datetime.TimestampUnit(255), wantErr: check.ErrInvalidRule},
	})
}

func TestParseUnixTime(t *testing.T) {
	testTimestamp(t, datetime.ParseUnixTime, []timestampCase[string]{
		// 正号和前导零验证十进制文本合法，并按毫秒解释。
		{name: "signed padded milliseconds", input: "+001001", unit: datetime.UnixMilliseconds, want: time.Date(1970, 1, 1, 0, 0, 1, 1_000_000, time.UTC)},
		// 负一纳秒验证解析保留负号与显式单位。
		{name: "negative nanoseconds", input: "-1", unit: datetime.UnixNanoseconds, want: time.Date(1969, 12, 31, 23, 59, 59, 999_999_999, time.UTC)},
		// 最大整数本身能解析，按秒转换时仍须报告时间范围错误。
		{name: "representable integer with time overflow", input: "9223372036854775807", unit: datetime.UnixSeconds, wantErr: check.ErrInvalidValue},
		// 只超出 int64 一位，验证文本数值范围。
		{name: "integer overflow", input: "9223372036854775808", unit: datetime.UnixMilliseconds, wantErr: check.ErrInvalidValue},
		// 前后附加空白验证时间戳文本必须精确匹配。
		{name: "surrounding spaces", input: " 1 ", unit: datetime.UnixSeconds, wantErr: check.ErrInvalidValue},
		// 含小数点验证输入单位不隐式转换小数秒。
		{name: "fractional text", input: "1.5", unit: datetime.UnixSeconds, wantErr: check.ErrInvalidValue},
		// 数值虽然是整数，指数形式仍违反时间戳语法。
		{name: "exponent text", input: "1e3", unit: datetime.UnixSeconds, wantErr: check.ErrInvalidValue},
		// 无效文本配未知单位，验证单位错误优先分类。
		{name: "unknown unit", input: "invalid", unit: datetime.TimestampUnit(255), wantErr: check.ErrInvalidRule},
	})
}

func TestNonZeroTime(t *testing.T) {
	tests := []struct {
		name    string
		value   time.Time
		wantErr error
	}{
		// 未设置的 time.Time 必须被识别为零时间。
		{name: "zero time", value: time.Time{}, wantErr: check.ErrInvalidValue},
		// 更换时区表示后，同一零时刻仍应被拒绝。
		{name: "zero instant in another location", value: time.Time{}.In(time.FixedZone("UTC+08", 8*60*60)), wantErr: check.ErrInvalidValue},
		// Unix 时间戳的零秒是一个有效时刻。
		{name: "Unix epoch", value: time.Unix(0, 0).UTC()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTimeError(t, datetime.NonZeroTime(test.value), test.wantErr)
		})
	}
}

func TestTimeInRange(t *testing.T) {
	minimum := timeTestReference()
	maximum := minimum.Add(2 * time.Second)
	tests := []struct {
		name    string
		value   time.Time
		minimum time.Time
		maximum time.Time
		wantErr error
	}{
		// 下界以不同的时区表示，验证同一时刻仍等于端点。
		{name: "lower endpoint in another location", value: minimum.In(time.FixedZone("UTC+08", 8*60*60)), minimum: minimum, maximum: maximum},
		// 两端之间的一秒验证普通区间内值。
		{name: "inside range", value: minimum.Add(time.Second), minimum: minimum, maximum: maximum},
		// 等于上界验证闭区间的另一端。
		{name: "upper endpoint", value: maximum, minimum: minimum, maximum: maximum},
		// 相差一个纳秒就已经越过下界。
		{name: "below minimum", value: minimum.Add(-time.Nanosecond), minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 相差一个纳秒也足以越过上界。
		{name: "above maximum", value: maximum.Add(time.Nanosecond), minimum: minimum, maximum: maximum, wantErr: check.ErrInvalidValue},
		// 上下界相等时，等于该时刻仍可通过。
		{name: "equal bounds", value: minimum, minimum: minimum, maximum: minimum},
		// 交换上下界验证顺序错误归入规则错误。
		{name: "reversed bounds", value: minimum, minimum: maximum, maximum: minimum, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTimeError(t, datetime.TimeInRange(test.value, test.minimum, test.maximum), test.wantErr)
		})
	}
}

type timeOrderCase struct {
	name    string
	value   time.Time
	wantErr error
}

func TestBefore(t *testing.T) {
	reference := timeTestReference()
	testTimeOrder(t, datetime.Before, reference, []timeOrderCase{
		// 一个纳秒的差异已足以严格早于参考值。
		{name: "earlier", value: reference.Add(-time.Nanosecond)},
		// 用不同时区表达同一时刻，验证严格比较拒绝相等值。
		{name: "same instant", value: reference.In(time.FixedZone("UTC+08", 8*60*60)), wantErr: check.ErrInvalidValue},
		// 晚一个纳秒属于相反方向，应被拒绝。
		{name: "later", value: reference.Add(time.Nanosecond), wantErr: check.ErrInvalidValue},
	})
}

func TestAfter(t *testing.T) {
	reference := timeTestReference()
	testTimeOrder(t, datetime.After, reference, []timeOrderCase{
		// 晚一个纳秒已满足严格晚于。
		{name: "later", value: reference.Add(time.Nanosecond)},
		// 不同时区中的同一时刻仍不满足严格比较。
		{name: "same instant", value: reference.In(time.FixedZone("UTC+08", 8*60*60)), wantErr: check.ErrInvalidValue},
		// 早一个纳秒违反时间顺序。
		{name: "earlier", value: reference.Add(-time.Nanosecond), wantErr: check.ErrInvalidValue},
	})
}

type timeWindowCase struct {
	name      string
	value     time.Time
	reference time.Time
	limit     time.Duration
	wantErr   error
}

func TestWithinPast(t *testing.T) {
	reference := timeTestReference()
	// 公元一年对应内部秒数零。Unix 秒超过其正侧容量后可构造内部最早秒附近的时刻。
	earliest := time.Unix(math.MaxInt64+time.Time{}.Unix()+2, 0).UTC()
	testTimeWindow(t, datetime.WithinPast, []timeWindowCase{
		// 参考时刻本身属于过去窗口的闭合上界。
		{name: "reference endpoint", value: reference, reference: reference, limit: time.Hour},
		// 刚好早一个最大时长时仍属于闭合下界。
		{name: "earliest endpoint", value: reference.Add(-time.Hour), reference: reference, limit: time.Hour},
		// 比最早时刻再早一个纳秒，验证超出最大年龄。
		{name: "too old", value: reference.Add(-time.Hour - time.Nanosecond), reference: reference, limit: time.Hour, wantErr: check.ErrInvalidValue},
		// 参考时刻之后不属于过去窗口。
		{name: "future instant", value: reference.Add(time.Nanosecond), reference: reference, limit: time.Hour, wantErr: check.ErrInvalidValue},
		// 零窗口只含参考时刻，验证时长可为零。
		{name: "zero window", value: reference, reference: reference},
		// 负最大年龄无法定义有效窗口。
		{name: "negative age", value: reference, reference: reference, limit: -time.Nanosecond, wantErr: check.ErrInvalidRule},
		// 从接近最早时刻再向前两秒导致边界饱和，应报告规则错误。
		{name: "unrepresentable lower bound", value: earliest, reference: earliest, limit: 2 * time.Second, wantErr: check.ErrInvalidRule},
	})
}

func TestWithinFuture(t *testing.T) {
	reference := timeTestReference()
	// 从最大内部秒数换算到 Unix 秒，构造可表示范围的上端附近。
	latest := time.Unix(math.MaxInt64+time.Time{}.Unix(), 0).UTC()
	testTimeWindow(t, datetime.WithinFuture, []timeWindowCase{
		// 参考时刻是未来窗口的闭合下界。
		{name: "reference endpoint", value: reference, reference: reference, limit: time.Hour},
		// 参考时刻加最大时长仍落在闭合上界。
		{name: "latest endpoint", value: reference.Add(time.Hour), reference: reference, limit: time.Hour},
		// 上界之后一个纳秒就超出允许延迟。
		{name: "too late", value: reference.Add(time.Hour + time.Nanosecond), reference: reference, limit: time.Hour, wantErr: check.ErrInvalidValue},
		// 参考时刻之前不属于未来窗口。
		{name: "past instant", value: reference.Add(-time.Nanosecond), reference: reference, limit: time.Hour, wantErr: check.ErrInvalidValue},
		// 取零延迟且值等于参考时刻应被接受。
		{name: "zero window", value: reference, reference: reference},
		// 负延迟属于规则配置错误。
		{name: "negative delay", value: reference, reference: reference, limit: -time.Nanosecond, wantErr: check.ErrInvalidRule},
		// 最晚可表示秒再前进一秒导致边界饱和，应报告规则错误。
		{name: "unrepresentable upper bound", value: latest, reference: latest, limit: time.Second, wantErr: check.ErrInvalidRule},
	})
}

func TestTimePrecision(t *testing.T) {
	location := time.FixedZone("UTC+00:30", 30*60)
	aligned := time.Date(2024, 2, 29, 0, 30, 0, 0, location)
	tests := []struct {
		name       string
		value      time.Time
		resolution time.Duration
		wantErr    error
	}{
		// 本地半点对应 UTC 整点，验证采用绝对时间对齐。
		{name: "aligned absolute hour", value: aligned, resolution: time.Hour},
		// 本地整点对应 UTC 半点，应按绝对时间拒绝。
		{name: "unaligned local hour", value: aligned.Add(-30 * time.Minute), resolution: time.Hour, wantErr: check.ErrInvalidValue},
		// 对齐时刻再加一个纳秒，验证最小差异也影响整小时精度。
		{name: "one nanosecond off", value: aligned.Add(time.Nanosecond), resolution: time.Hour, wantErr: check.ErrInvalidValue},
		// 同一带纳秒的时刻可按纳秒分辨率精确对齐。
		{name: "nanosecond precision", value: aligned.Add(time.Nanosecond), resolution: time.Nanosecond},
		// 零分辨率无法定义精度，验证规则错误。
		{name: "zero resolution", value: aligned, wantErr: check.ErrInvalidRule},
		// 负分辨率也无法形成精度规则。
		{name: "negative resolution", value: aligned, resolution: -time.Second, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTimeError(t, datetime.TimePrecision(test.value, test.resolution), test.wantErr)
		})
	}
}

func testTimeParser(t *testing.T, parse func(string, string, *time.Location) (time.Time, error), tests []timeParseCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input, test.layout, test.location)
			assertTimeError(t, err, test.wantErr)
			if test.wantErr == nil {
				_, gotOffset := got.Zone()
				_, wantOffset := test.want.Zone()
				if !got.Equal(test.want) || gotOffset != wantOffset {
					t.Errorf("parse(%q) = %v, want %v", test.input, got, test.want)
				}
			}
		})
	}
}

func testTimestamp[T int64 | string](t *testing.T, parse func(T, datetime.TimestampUnit) (time.Time, error), tests []timestampCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input, test.unit)
			assertTimeError(t, err, test.wantErr)
			if test.wantErr == nil && (!got.Equal(test.want) || got.Location() != time.UTC) {
				t.Errorf("parse(%v, %v) = %v in %v, want %v in UTC", test.input, test.unit, got, got.Location(), test.want)
			}
		})
	}
}

func testTimeOrder(t *testing.T, validate func(time.Time, time.Time) error, reference time.Time, tests []timeOrderCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTimeError(t, validate(test.value, reference), test.wantErr)
		})
	}
}

func testTimeWindow(t *testing.T, validate func(time.Time, time.Time, time.Duration) error, tests []timeWindowCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTimeError(t, validate(test.value, test.reference, test.limit), test.wantErr)
		})
	}
}

func assertTimeError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func timeTestReference() time.Time {
	return time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
}
