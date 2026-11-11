package tracing_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go-service-template/internal/tracing"
)

func TestNewTraceID(t *testing.T) {
	// 先提供 16 个零字节，再提供编码后含有 a–f 的字节，验证生成器跳过全零并输出小写十六进制。
	const next = "\x12\x34\x56\x78\x90\xab\xcd\xef\x01\x23\x45\x67\x89\xab\xcd\xef"
	originalReader := rand.Reader
	rand.Reader = io.MultiReader(strings.NewReader(strings.Repeat("\x00", 16)+next), originalReader)
	t.Cleanup(func() { rand.Reader = originalReader })

	const want = "1234567890abcdef0123456789abcdef"
	got, err := tracing.NewTraceID()
	if err != nil || got != want {
		t.Errorf("NewTraceID() = %q, %v, want %q, nil", got, err, want)
	}
}

type validTraceIDCase struct {
	name string
	id   string
	want bool
}

func TestIsValidTraceID(t *testing.T) {
	const valid = "0123456789abcdef0123456789abcdef"
	tests := []validTraceIDCase{
		// 混合数字和小写 a–f，验证包含 32 个十六进制字符的标准标识可通过。
		{name: "lowercase hex", id: valid, want: true},
		// 仅最后一位非零，验证允许前导零并检查整个标识是否全零。
		{name: "nonzero last digit", id: strings.Repeat("0", 31) + "1", want: true},
		// 长度和字符均合法但所有位均为零，验证拒绝保留标识。
		{name: "all zero", id: strings.Repeat("0", 32)},
		// 缺少请求头时可能得到空串，验证空串不是合法标识。
		{name: "empty"},
		// 去掉最后一位，验证合法字符也必须恰好出现 32 次。
		{name: "one digit short", id: valid[:31]},
		// 多附加一位，验证长度检查不会接受合法的 32 位前缀。
		{name: "one digit long", id: valid + "0"},
		// 只把一个十六进制字母改为大写，验证输入必须采用小写形式。
		{name: "uppercase letter", id: valid[:10] + "A" + valid[11:]},
		// 使用紧邻 f 的 g，验证字母范围到 f 为止。
		{name: "letter outside hex", id: valid[:31] + "g"},
		// 用空格替换首位并保持长度，验证输入不会自动裁剪或补齐。
		{name: "leading space", id: " " + valid[1:]},
		// 用连字符替换一位并保持长度，验证不接受带分隔符的格式。
		{name: "separator", id: valid[:16] + "-" + valid[17:]},
		// 用一个双字节字符替换两位 ASCII，验证总字节数相同时仍拒绝非 ASCII 字符。
		{name: "non ASCII", id: "é" + valid[2:]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := tracing.IsValidTraceID(test.id); got != test.want {
				t.Errorf("IsValidTraceID(%q) = %t, want %t", test.id, got, test.want)
			}
		})
	}
}

func TestWithTraceID(t *testing.T) {
	// 父上下文含自定义值和截止时间，验证保存 TraceId 只派生上下文并保留父状态。
	type markerKey struct{}
	const id = "0123456789abcdef0123456789abcdef"
	parent, cancel := context.WithTimeout(context.WithValue(t.Context(), markerKey{}, "keep"), time.Hour)
	t.Cleanup(cancel)
	child := tracing.WithTraceID(parent, id)

	if got := tracing.TraceID(child); got != id {
		t.Errorf("child TraceID = %q, want %q", got, id)
	}
	if got := tracing.TraceID(parent); got != "" {
		t.Errorf("parent TraceID = %q, want empty", got)
	}
	if got := tracing.SpanID(child); got != "" {
		t.Errorf("child SpanID = %q, want empty", got)
	}
	if got := child.Value(markerKey{}); got != "keep" {
		t.Errorf("child context value = %v, want keep", got)
	}
	wantDeadline, _ := parent.Deadline()
	if got, ok := child.Deadline(); !ok || !got.Equal(wantDeadline) {
		t.Errorf("child deadline = %v, %t, want %v, true", got, ok, wantDeadline)
	}

	cancel()
	if err := child.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("child Err() after cancel = %v, want %v", err, context.Canceled)
	}
}
