package tracing_test

import (
	"context"
	"testing"

	"go-service-template/internal/tracing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type boundaryNilTracerProvider struct {
	trace.TracerProvider
}

type newBoundaryTracerCase struct {
	name        string
	provider    trace.TracerProvider
	wantFailure bool
}

func TestNew(t *testing.T) {
	tests := []newBoundaryTracerCase{
		// 缺少 Provider 时应报告构造失败，避免在请求开始后才发现追踪入口不可用。
		{name: "missing provider", wantFailure: true},
		// Provider 存在但返回空 Tracer，应在创建追踪入口时报告错误。
		{name: "provider returns nil tracer", provider: boundaryNilTracerProvider{}, wantFailure: true},
		// 显式传入空操作 Provider，验证构造成功且创建的 Span 保持不采集。
		{name: "explicit noop provider", provider: noop.NewTracerProvider()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance, err := tracing.New(test.provider)
			if test.wantFailure {
				if instance != nil || err == nil {
					t.Errorf("New() = %v, %v; want nil, error", instance, err)
				}
				return
			}
			if err != nil || instance == nil {
				t.Fatalf("New() = %v, %v; want a tracer", instance, err)
			}
			ctx, span := instance.StartService(t.Context(), "service Lookup")
			defer span.End()
			if trace.SpanFromContext(ctx).IsRecording() {
				t.Error("span records with an explicit noop provider")
			}
		})
	}
}

func TestStartHTTP(t *testing.T) {
	// 为入站请求提供已采样的远端父节点和注册路由，验证子节点延续父链路并保留原有 Context。
	instance, recorder := newRecordingBoundaryTracer(t)
	type requestValueKey struct{}
	parent := newBoundaryParentSpanContext()
	parentContext := trace.ContextWithRemoteSpanContext(t.Context(), parent)
	parentContext = context.WithValue(parentContext, requestValueKey{}, "request-value")

	ctx, span := instance.StartHTTP(parentContext, "GET", "/profiles/:id")
	span.End()
	recorded := requireOnlyEndedBoundarySpan(t, recorder)

	if recorded.Name() != "GET /profiles/:id" || recorded.SpanKind() != trace.SpanKindServer {
		t.Errorf("recorded span = %q, %v; want GET /profiles/:id, server", recorded.Name(), recorded.SpanKind())
	}
	if !recorded.Parent().Equal(parent) || recorded.SpanContext().TraceID() != parent.TraceID() {
		t.Errorf("recorded parent = %v, trace ID = %v; want parent %v and its trace ID",
			recorded.Parent(), recorded.SpanContext().TraceID(), parent)
	}
	if spanContext := trace.SpanContextFromContext(ctx); !spanContext.Equal(recorded.SpanContext()) || spanContext.SpanID() == parent.SpanID() {
		t.Errorf("derived span context = %v; want recorded child %v", spanContext, recorded.SpanContext())
	}
	if !trace.SpanContextFromContext(parentContext).Equal(parent) {
		t.Error("starting a child changed the parent context")
	}
	if ctx.Value(requestValueKey{}) != "request-value" {
		t.Error("derived context lost the caller's value")
	}
	if recorded.InstrumentationScope().Name != "go-service-template/internal/tracing" {
		t.Errorf("instrumentation scope = %q; want package import path", recorded.InstrumentationScope().Name)
	}
	assertBoundaryAttribute(t, recorded, "http.request.method", attribute.StringValue("GET"))
	assertBoundaryAttribute(t, recorded, "http.route", attribute.StringValue("/profiles/:id"))
}

type boundaryOperationCase struct {
	name      string
	operation string
	wantName  string
}

func TestStartController(t *testing.T) {
	tests := []boundaryOperationCase{
		// 传入解析后的控制器操作名，验证它直接成为内部 Span 的名称。
		{name: "resolved operation", operation: "controller GetProfile", wantName: "controller GetProfile"},
		// 未提供控制器操作名时，使用控制器专属占位名。
		{name: "unresolved operation", wantName: "controller <unresolved>"},
	}
	testBoundaryOperations(t, (*tracing.Tracer).StartController, trace.SpanKindInternal, tests)
}

func TestStartService(t *testing.T) {
	tests := []boundaryOperationCase{
		// 传入解析后的业务操作名，验证创建对应的内部 Span。
		{name: "resolved operation", operation: "service SaveProfile", wantName: "service SaveProfile"},
		// 省略业务操作名，验证占位名称仍表达 Service 边界。
		{name: "unresolved operation", wantName: "service <unresolved>"},
	}
	testBoundaryOperations(t, (*tracing.Tracer).StartService, trace.SpanKindInternal, tests)
}

func TestStartCompanyClient(t *testing.T) {
	tests := []boundaryOperationCase{
		// 传入公司接口操作名，验证创建独立的客户端 Span。
		{name: "resolved operation", operation: "company_client FetchProfile", wantName: "company_client FetchProfile"},
		// 省略具体接口名，验证占位名称仍表达公司客户端边界。
		{name: "unresolved operation", wantName: "company_client <unresolved>"},
	}
	testBoundaryOperations(t, (*tracing.Tracer).StartCompanyClient, trace.SpanKindClient, tests)
}

func TestStartSDKClient(t *testing.T) {
	tests := []boundaryOperationCase{
		// 传入项目封装的 SDK 操作名，验证该名称对应客户端 Span。
		{name: "resolved operation", operation: "sdk_client Generate", wantName: "sdk_client Generate"},
		// 省略 SDK 操作名时，验证占位名称仍区分 SDK 与公司客户端。
		{name: "unresolved operation", wantName: "sdk_client <unresolved>"},
	}
	testBoundaryOperations(t, (*tracing.Tracer).StartSDKClient, trace.SpanKindClient, tests)
}

type boundaryStartFailureCase struct {
	name      string
	start     func()
	wantPanic string
}

func TestStartRejectsInvalidArguments(t *testing.T) {
	instance, recorder := newRecordingBoundaryTracer(t)
	var absent *tracing.Tracer
	tests := []boundaryStartFailureCase{
		// 入站方法为空时无法形成操作名，应立即报告调用错误。
		{name: "missing HTTP method", start: func() { instance.StartHTTP(t.Context(), "", "/profiles") }, wantPanic: "tracing HTTP method is required"},
		// nil Tracer 不能创建节点，应以统一的未初始化错误退出。
		{name: "nil tracer", start: func() { absent.StartService(t.Context(), "service Read") }, wantPanic: "tracer is not initialized"},
		// Tracer 零值也没有底层实例，验证拒绝从零值创建节点。
		{name: "zero tracer", start: func() { new(tracing.Tracer).StartService(t.Context(), "service Read") }, wantPanic: "tracer is not initialized"},
		// 传入 nil Context 时应拒绝操作，避免悄悄丢失上游链路。
		{name: "missing context", start: func() { instance.StartService(nil, "service Read") }, wantPanic: "tracing context is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertBoundaryPanic(t, test.start, test.wantPanic)
		})
	}
	if got := len(recorder.Started()); got != 0 {
		t.Errorf("started spans = %d; want 0", got)
	}
}

func (boundaryNilTracerProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	return nil
}

func newRecordingBoundaryTracer(t *testing.T) (*tracing.Tracer, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder), sdktrace.WithoutPanicRecording())
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown in-memory tracer provider: %v", err)
		}
	})
	instance, err := tracing.New(provider)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return instance, recorder
}

func newBoundaryParentSpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3},
		SpanID:     trace.SpanID{4, 5, 6},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

func testBoundaryOperations(
	t *testing.T,
	start func(*tracing.Tracer, context.Context, string) (context.Context, *tracing.Span),
	wantKind trace.SpanKind,
	tests []boundaryOperationCase,
) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance, recorder := newRecordingBoundaryTracer(t)
			parent := newBoundaryParentSpanContext()
			ctx, span := start(instance, trace.ContextWithRemoteSpanContext(t.Context(), parent), test.operation)
			span.End()
			recorded := requireOnlyEndedBoundarySpan(t, recorder)
			if recorded.Name() != test.wantName || recorded.SpanKind() != wantKind {
				t.Errorf("recorded span = %q, %v; want %q, %v", recorded.Name(), recorded.SpanKind(), test.wantName, wantKind)
			}
			if !recorded.Parent().Equal(parent) || !trace.SpanContextFromContext(ctx).Equal(recorded.SpanContext()) {
				t.Errorf("recorded parent = %v, derived context = %v; want %v, %v",
					recorded.Parent(), trace.SpanContextFromContext(ctx), parent, recorded.SpanContext())
			}
		})
	}
}

func requireOnlyEndedBoundarySpan(t *testing.T, recorder *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d; want 1", len(spans))
	}
	return spans[0]
}

func assertBoundaryAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key attribute.Key, want attribute.Value) {
	t.Helper()
	for _, kv := range span.Attributes() {
		if kv.Key == key {
			if kv.Value != want {
				t.Errorf("span attribute %q = %v; want %v", key, kv.Value, want)
			}
			return
		}
	}
	if want.Type() != attribute.INVALID {
		t.Errorf("span attribute %q is absent; want %v", key, want)
	}
}

func assertBoundaryPanic(t *testing.T, action func(), want any) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Errorf("panic = %v; want %v", got, want)
		}
	}()
	action()
}
