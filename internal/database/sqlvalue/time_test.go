package sqlvalue_test

import (
	"database/sql"
	"testing"
	"time"

	"go-service-template/internal/database/sqlvalue"
)

func TestToNullTime(t *testing.T) {
	zero := time.Time{}
	zoned := time.Date(2026, time.November, 11, 13, 14, 15, 123_456_789, time.FixedZone("UTC+05:45", 5*60*60+45*60))
	testToNull(t, sqlvalue.ToNullTime, []toNullCase[time.Time, sql.NullTime]{
		// nil 指针表示时间缺失，验证转换结果为 SQL NULL。
		{name: "nil", input: nil, want: sql.NullTime{}},
		// 显式提供 Go 的零时间，验证它仍标记为有效值。
		{name: "valid zero time", input: &zero, want: sql.NullTime{Time: time.Time{}, Valid: true}},
		// 使用非整小时的固定时区和纳秒分量，验证写入保留时区与完整精度。
		{name: "zone and nanoseconds", input: &zoned, want: sql.NullTime{Time: zoned, Valid: true}},
	})
}

func TestFromNullTime(t *testing.T) {
	zero := time.Time{}
	zoned := time.Date(2026, time.November, 11, 13, 14, 15, 123_456_789, time.FixedZone("UTC+05:45", 5*60*60+45*60))
	testFromNull(t, sqlvalue.FromNullTime, []fromNullCase[sql.NullTime, time.Time]{
		// 无效 SQL 值带有一个非零时间，验证忽略残留的时间内容。
		{name: "invalid with residual time", input: sql.NullTime{Time: zoned}, want: nil},
		// 有效零时间应返回非 nil 指针，调用方可区分零时间与缺失。
		{name: "valid zero time", input: sql.NullTime{Time: time.Time{}, Valid: true}, want: &zero},
		// 使用非整小时时区和纳秒分量，验证读取保留时区与完整精度。
		{name: "zone and nanoseconds", input: sql.NullTime{Time: zoned, Valid: true}, want: &zoned},
	})
}
