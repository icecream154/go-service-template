package logger

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"go-service-template/internal/tracing"
)

type writeFailureReportCase struct {
	name string
	ctx  context.Context
	want string
}

func TestReportWriteFailure(t *testing.T) {
	const traceID = "0123456789abcdef0123456789abcdef"
	canceled, cancel := context.WithCancel(tracing.WithTraceID(t.Context(), traceID))
	cancel()
	outputError := errors.New("fixture output failed")
	tests := []writeFailureReportCase{
		// 没有请求上下文时也要报告日志故障，并留空链路标识。
		{name: "nil context", want: "Write application log failed: trace_id=\"\" error=fixture output failed\n"},
		// 上下文已经取消但仍携带链路标识，验证故障报告保留原请求归属。
		{
			name: "canceled trace context", ctx: canceled,
			want: "Write application log failed: trace_id=\"0123456789abcdef0123456789abcdef\" error=fixture output failed\n",
		},
		// 人为放入带换行和引号的异常兼容值，验证转义使故障报告仍占一行。
		{
			name: "escapes unexpected trace ID", ctx: tracing.WithTraceID(t.Context(), "legacy\n\"id"),
			want: "Write application log failed: trace_id=\"legacy\\n\\\"id\" error=fixture output failed\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := captureLoggerFallbackOutput(t, func() { reportWriteFailure(test.ctx, outputError) })
			if got != test.want {
				t.Errorf("stderr = %q, want %q", got, test.want)
			}
		})
	}
}

func captureLoggerFallbackOutput(t *testing.T, action func()) string {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "stderr-")
	if err != nil {
		t.Fatalf("create stderr fixture: %v", err)
	}
	defer output.Close()

	previous := os.Stderr
	os.Stderr = output
	restore := func() { os.Stderr = previous }
	t.Cleanup(restore)
	defer restore()

	action()
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind stderr fixture: %v", err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		t.Fatalf("read stderr fixture: %v", err)
	}
	return string(data)
}
