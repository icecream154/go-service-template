package models_test

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"go-service-template/app/domains/user/models"
	"go-service-template/internal/check"
)

func TestParseMembershipChangeRecordPageToken(t *testing.T) {
	// 固定 v1 样本独立编码为版本 1、用户 9223372036854775807、Unix 毫秒 1794448800123、记录 9223372036854775806。
	// 不调用生产编码器准备解析输入，防止编码和解码同时改错后仍能往返通过。
	const token = "AX__________AAABoc17uXt__________g"
	cursor := models.MembershipChangeRecordCursor{UserID: math.MaxInt64, ID: math.MaxInt64 - 1, CreatedAt: time.Date(2026, 11, 12, 2, 0, 0, 123000000, time.UTC)}
	parsed, err := models.ParseMembershipChangeRecordPageToken(token, cursor.UserID)
	if err != nil || parsed == nil || *parsed != cursor {
		t.Fatalf("Parse token = (%+v, %v), want %+v", parsed, err, cursor)
	}
	// 空令牌明确代表第一页，不生成零值游标。
	if got, err := models.ParseMembershipChangeRecordPageToken("", 123); got != nil || err != nil {
		t.Fatalf("empty token = (%+v, %v), want (nil, nil)", got, err)
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode fixed v1 fixture: %v", err)
	}
	alter := func(change func([]byte)) string {
		copyData := append([]byte(nil), data...)
		change(copyData)
		return base64.RawURLEncoding.EncodeToString(copyData)
	}
	tests := []struct {
		name   string
		token  string
		userID int64
	}{
		// 令牌中的所属用户必须与本次已校验的目标用户一致。
		{name: "another user", token: token, userID: 123},
		// 不支持的版本不能被按当前结构猜测解析。
		{name: "unknown version", token: alter(func(b []byte) { b[0] = 2 }), userID: cursor.UserID},
		// 零记录标识不能形成合法分页边界。
		{name: "zero record ID", token: alter(func(b []byte) { binary.BigEndian.PutUint64(b[17:25], 0) }), userID: cursor.UserID},
		// 超出有符号整数范围的标识不能绕过正数校验。
		{name: "overflow record ID", token: alter(func(b []byte) { binary.BigEndian.PutUint64(b[17:25], uint64(math.MaxInt64)+1) }), userID: cursor.UserID},
		// 不能将任意极端毫秒数作为数据库日期。
		{name: "out of range time", token: alter(func(b []byte) { binary.BigEndian.PutUint64(b[9:17], math.MaxInt64) }), userID: cursor.UserID},
		// 已知编码但长度不足仍必须拒绝，不能越界读取。
		{name: "short payload", token: base64.RawURLEncoding.EncodeToString(data[:24]), userID: cursor.UserID},
		// 解码前即限制最大长度，防止无限制分配。
		{name: "oversized payload", token: strings.Repeat("A", 1024), userID: cursor.UserID},
		// RawURL 令牌不允许额外填充。
		{name: "padded token", token: token + "==", userID: cursor.UserID},
		// 尾部换行不能被静默忽略并接受为同一令牌。
		{name: "whitespace", token: token + "\n", userID: cursor.UserID},
		// 非法字符应报告输入错误而不是编码组件原始错误。
		{name: "invalid encoding", token: "%", userID: cursor.UserID},
		// 空令牌也不能跳过用户标识前提。
		{name: "zero target user", userID: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := models.ParseMembershipChangeRecordPageToken(tt.token, tt.userID)
			if got != nil || !errors.Is(err, check.ErrInvalidValue) {
				t.Fatalf("Parse token = (%+v, %v), want invalid input", got, err)
			}
		})
	}
}

func TestEncodeMembershipChangeRecordPageToken(t *testing.T) {
	tests := []struct {
		name      string
		cursor    models.MembershipChangeRecordCursor
		wantToken string
		failed    bool
	}{
		// 独立固定 v1 字符串锁定版本、字段次序、大端整数和毫秒时间，避免只靠解码器验证编码器。
		{name: "fixed v1 fixture", cursor: models.MembershipChangeRecordCursor{UserID: 9223372036854775807, ID: 9223372036854775806, CreatedAt: time.Date(2026, 11, 12, 2, 0, 0, 123000000, time.UTC)}, wantToken: "AX__________AAABoc17uXt__________g"},
		// MySQL 最小有效年支持负 Unix 毫秒，往返转换不能视为无符号时间。
		{name: "minimum date", cursor: models.MembershipChangeRecordCursor{UserID: 1, ID: 2, CreatedAt: time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)}},
		// 最大日期边界保持毫秒精度。
		{name: "maximum date", cursor: models.MembershipChangeRecordCursor{UserID: 1, ID: 2, CreatedAt: time.Date(9999, 12, 31, 23, 59, 59, 999000000, time.UTC)}},
		// 零ID不能作为分页边界输出。
		{name: "zero user", cursor: models.MembershipChangeRecordCursor{ID: 2, CreatedAt: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}, failed: true},
		// 不允许静默丢弃亚毫秒精度而改变边界。
		{name: "submillisecond time", cursor: models.MembershipChangeRecordCursor{UserID: 1, ID: 2, CreatedAt: time.Date(2026, 11, 12, 0, 0, 0, 1, time.UTC)}, failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := models.EncodeMembershipChangeRecordPageToken(tt.cursor)
			if tt.failed {
				if token != "" || !errors.Is(err, check.ErrInvalidValue) {
					t.Fatalf("Encode = (%q, %v), want invalid input", token, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantToken != "" && token != tt.wantToken {
				t.Errorf("Encode = %q, want fixed v1 token %q", token, tt.wantToken)
			}
			got, err := models.ParseMembershipChangeRecordPageToken(token, tt.cursor.UserID)
			if err != nil || got == nil || *got != tt.cursor {
				t.Fatalf("round trip = (%+v, %v), want %+v", got, err, tt.cursor)
			}
		})
	}
}
