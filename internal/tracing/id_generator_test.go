package tracing

import (
	"context"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const (
	idGeneratorTraceBytes = "\x01\x23\x45\x67\x89\xab\xcd\xef\xfe\xdc\xba\x98\x76\x54\x32\x10"
	idGeneratorSpanBytes  = "\x01\x02\x03\x04\x05\x06\x07\x08"
)

type newTraceAndSpanIDsCase struct {
	name      string
	ctx       context.Context
	random    string
	wantTrace string
}

func TestContextIDGeneratorNewIDs(t *testing.T) {
	const incomingID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const generatedID = "0123456789abcdeffedcba9876543210"
	tests := []newTraceAndSpanIDsCase{
		// 兼容 TraceId 合法时只提供生成 SpanId 所需的字节，验证根节点沿用原链路。
		{
			name: "preserves valid trace ID", ctx: WithTraceID(t.Context(), incomingID),
			random: idGeneratorSpanBytes, wantTrace: incomingID,
		},
		// 没有入口标识时提供两段随机字节，验证为新链路和执行节点分别生成标识。
		{
			name: "generates missing trace ID", ctx: t.Context(),
			random: idGeneratorTraceBytes + idGeneratorSpanBytes, wantTrace: generatedID,
		},
		// 兼容值非法且首次读取全零，验证生成器舍弃二者，再使用下一组有效字节。
		{
			name: "retries all-zero generated trace ID", ctx: WithTraceID(t.Context(), "invalid"),
			random: strings.Repeat("\x00", 16) + idGeneratorTraceBytes + idGeneratorSpanBytes, wantTrace: generatedID,
		},
	}
	const wantSpan = "0102030405060708"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useIDGeneratorRandom(t, test.random)
			traceID, spanID := (contextIDGenerator{}).NewIDs(test.ctx)
			if traceID.String() != test.wantTrace || spanID.String() != wantSpan {
				t.Errorf("NewIDs() = %s/%s, want %s/%s", traceID, spanID, test.wantTrace, wantSpan)
			}
		})
	}
}

func TestContextIDGeneratorNewSpanID(t *testing.T) {
	// 连续为同一 TraceId 生成节点标识，首组全零应被跳过，两次调用应分别使用之后的独立字节。
	const second = "\x11\x12\x13\x14\x15\x16\x17\x18"
	useIDGeneratorRandom(t, strings.Repeat("\x00", 8)+idGeneratorSpanBytes+second)
	generator := contextIDGenerator{}
	traceID := trace.TraceID{1}
	firstSpan := generator.NewSpanID(t.Context(), traceID)
	secondSpan := generator.NewSpanID(t.Context(), traceID)
	if firstSpan.String() != "0102030405060708" || secondSpan.String() != "1112131415161718" {
		t.Errorf("NewSpanID() = %s, %s, want 0102030405060708, 1112131415161718", firstSpan, secondSpan)
	}
}

func useIDGeneratorRandom(t *testing.T, fixture string) {
	t.Helper()
	original := rand.Reader
	rand.Reader = io.MultiReader(strings.NewReader(fixture), original)
	t.Cleanup(func() { rand.Reader = original })
}
