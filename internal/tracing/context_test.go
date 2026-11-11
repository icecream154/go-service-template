package tracing_test

import (
	"context"
	"maps"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"go-service-template/internal/tracing"
)

const (
	contextTestTraceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
	contextTestSpanID   = "00f067aa0ba902b7"
	contextTestLegacyID = "0123456789abcdef0123456789abcdef"
)

type extractCase struct {
	name       string
	carrier    propagation.TextMapCarrier
	wantRemote bool
	wantFlags  trace.TraceFlags
	wantState  string
}

func TestExtract(t *testing.T) {
	type markerKey struct{}
	tests := []extractCase{
		// 标准头带有效父节点、采样标志和 tracestate，验证提取后保留这些远端信息。
		{
			name: "sampled remote parent",
			carrier: propagation.MapCarrier{
				"traceparent": "00-" + contextTestTraceID + "-" + contextTestSpanID + "-01",
				"tracestate":  "vendor=state",
			},
			wantRemote: true, wantFlags: trace.FlagsSampled, wantState: "vendor=state",
		},
		// 采样标志为零的父节点依然有效，验证提取不会要求采样。
		{
			name:       "unsampled remote parent",
			carrier:    propagation.MapCarrier{"traceparent": "00-" + contextTestTraceID + "-" + contextTestSpanID + "-00"},
			wantRemote: true,
		},
		// nil 载体表示没有追踪头，验证直接沿用父上下文。
		{name: "nil carrier"},
		// 空载体存在但没有标准头，验证不创建远端父节点。
		{name: "no headers", carrier: propagation.MapCarrier{}},
		// 截断 traceparent 只留下版本和 TraceId，验证不完整的父节点被忽略。
		{name: "incomplete parent", carrier: propagation.MapCarrier{"traceparent": "00-" + contextTestTraceID}},
		// 只传入项目旧 TraceId 头，验证标准提取不把它当成远端 SpanContext。
		{name: "legacy header only", carrier: propagation.MapCarrier{tracing.TraceIDHeaderName: contextTestTraceID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := tracing.WithTraceID(context.WithValue(t.Context(), markerKey{}, "keep"), contextTestLegacyID)
			got := tracing.Extract(parent, test.carrier)
			if !test.wantRemote {
				if got != parent {
					t.Error("Extract() did not return the original context without a valid parent")
				}
				return
			}

			remote := trace.SpanContextFromContext(got)
			if !remote.IsValid() || !remote.IsRemote() {
				t.Fatalf("Extract() span context = %v, want a valid remote parent", remote)
			}
			if remote.TraceID().String() != contextTestTraceID || remote.SpanID().String() != contextTestSpanID {
				t.Errorf("remote trace/span = %s/%s, want %s/%s",
					remote.TraceID(), remote.SpanID(), contextTestTraceID, contextTestSpanID)
			}
			if remote.TraceFlags() != test.wantFlags || remote.TraceState().String() != test.wantState {
				t.Errorf("remote flags/state = %v/%q, want %v/%q",
					remote.TraceFlags(), remote.TraceState().String(), test.wantFlags, test.wantState)
			}
			if got.Value(markerKey{}) != "keep" || tracing.TraceID(parent) != contextTestLegacyID {
				t.Error("Extract() lost the parent context value or changed the parent trace ID")
			}
		})
	}
}

func TestExtractRequiresContext(t *testing.T) {
	// 给 nil 上下文提供合法载体，验证缺少父上下文时暴露调用错误。
	defer func() {
		if recover() == nil {
			t.Error("Extract() with a nil context did not panic")
		}
	}()
	tracing.Extract(nil, propagation.MapCarrier{"traceparent": "00-" + contextTestTraceID + "-" + contextTestSpanID + "-01"})
}

type injectCase struct {
	name    string
	ctx     context.Context
	carrier propagation.MapCarrier
	want    propagation.MapCarrier
}

func TestInject(t *testing.T) {
	tests := []injectCase{
		// 当前节点已采样且含状态，验证出站头反映当前 TraceId、SpanId、采样标志和 tracestate。
		{
			name: "sampled span with state", ctx: withContextTestSpan(t, t.Context(), trace.FlagsSampled, "vendor=state"),
			carrier: propagation.MapCarrier{"existing": "keep"},
			want: propagation.MapCarrier{
				"existing": "keep", "traceparent": "00-" + contextTestTraceID + "-" + contextTestSpanID + "-01",
				"tracestate": "vendor=state",
			},
		},
		// 有效但未采样的节点也应传播，验证标志保留为 00，空 tracestate 不生成头。
		{
			name: "unsampled span without state", ctx: withContextTestSpan(t, t.Context(), 0, ""),
			carrier: propagation.MapCarrier{}, want: propagation.MapCarrier{"traceparent": "00-" + contextTestTraceID + "-" + contextTestSpanID + "-00"},
		},
		// 只保存兼容 TraceId 时缺少 SpanId，验证不会构造标准追踪头。
		{
			name: "legacy ID without a span", ctx: tracing.WithTraceID(t.Context(), contextTestLegacyID),
			carrier: propagation.MapCarrier{"existing": "keep"}, want: propagation.MapCarrier{"existing": "keep"},
		},
		// 可写载体配 nil 上下文，验证不会添加任何头。
		{name: "nil context", carrier: propagation.MapCarrier{"existing": "keep"}, want: propagation.MapCarrier{"existing": "keep"}},
		// 有效 Span 配 nil 载体，验证无处写入时安全返回。
		{name: "nil carrier", ctx: withContextTestSpan(t, t.Context(), trace.FlagsSampled, "")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var carrier propagation.TextMapCarrier
			if test.carrier != nil {
				carrier = test.carrier
			}
			tracing.Inject(test.ctx, carrier)
			if !maps.Equal(test.carrier, test.want) {
				t.Errorf("Inject() carrier = %v, want %v", test.carrier, test.want)
			}
		})
	}
}

type readContextIDCase struct {
	name string
	ctx  context.Context
	want string
}

func TestTraceID(t *testing.T) {
	legacy := tracing.WithTraceID(t.Context(), contextTestLegacyID)
	tests := []readContextIDCase{
		// 同时存在采样的标准节点和不同的兼容值，验证优先返回标准 TraceId。
		{name: "standard over legacy", ctx: withContextTestSpan(t, legacy, trace.FlagsSampled, ""), want: contextTestTraceID},
		// 未采样的标准节点仍可关联日志，验证其优先级与已采样节点相同。
		{name: "unsampled standard over legacy", ctx: withContextTestSpan(t, legacy, 0, ""), want: contextTestTraceID},
		// 当前标准节点无效时，验证兼容 TraceId 仍可读取。
		{name: "invalid standard falls back", ctx: trace.ContextWithSpanContext(legacy, trace.SpanContext{}), want: contextTestLegacyID},
		// 只有兼容标识时，验证当前节点缺失也能取到已保存的值。
		{name: "legacy only", ctx: legacy, want: contextTestLegacyID},
		// 普通上下文没有追踪值，验证读取不会生成标识。
		{name: "empty context", ctx: t.Context()},
		// 调用方可能没有请求上下文，验证 nil 安全返回空串。
		{name: "nil context"},
	}
	testContextIDReading(t, tracing.TraceID, tests)
}

func TestSpanID(t *testing.T) {
	tests := []readContextIDCase{
		// 有效标准节点提供实际执行标识，验证以 16 个小写十六进制字符返回。
		{name: "valid span", ctx: withContextTestSpan(t, t.Context(), trace.FlagsSampled, ""), want: contextTestSpanID},
		// 未采样节点仍然有效，验证读取不会丢弃其 SpanId。
		{name: "unsampled span", ctx: withContextTestSpan(t, t.Context(), 0, ""), want: contextTestSpanID},
		// 标准节点只提供 SpanId 而缺少 TraceId，验证不会输出无效节点的标识。
		{name: "invalid span", ctx: trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{SpanID: trace.SpanID{1}}))},
		// 保存兼容 TraceId 不会生成执行节点，验证 SpanId 仍为空。
		{name: "legacy trace only", ctx: tracing.WithTraceID(t.Context(), contextTestLegacyID)},
		// 无上下文时也没有执行节点，验证返回空串。
		{name: "nil context"},
	}
	testContextIDReading(t, tracing.SpanID, tests)
}

func withContextTestSpan(t *testing.T, ctx context.Context, flags trace.TraceFlags, state string) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(contextTestTraceID)
	if err != nil {
		t.Fatalf("parse fixture trace ID: %v", err)
	}
	spanID, err := trace.SpanIDFromHex(contextTestSpanID)
	if err != nil {
		t.Fatalf("parse fixture span ID: %v", err)
	}
	traceState, err := trace.ParseTraceState(state)
	if err != nil {
		t.Fatalf("parse fixture trace state: %v", err)
	}
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: flags, TraceState: traceState,
	}))
}

func testContextIDReading(t *testing.T, read func(context.Context) string, tests []readContextIDCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := read(test.ctx); got != test.want {
				t.Errorf("context ID = %q, want %q", got, test.want)
			}
		})
	}
}
