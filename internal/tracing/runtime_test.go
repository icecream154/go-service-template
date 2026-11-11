package tracing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-service-template/env"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const runtimeEndpointVariable = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"

type tracingInitCase struct {
	name        string
	ctx         context.Context
	serviceName string
	instanceID  string
	environment string
	endpoint    string
	wantError   string
}

func TestInit(t *testing.T) {
	tests := []tracingInitCase{
		// 启动时缺少 Context，验证初始化直接返回参数错误。
		{name: "missing context", serviceName: "profile", instanceID: "instance", environment: "test", wantError: "startup context is required"},
		// 空白服务名不能建立明确的服务资源，验证拒绝初始化。
		{name: "blank service name", ctx: t.Context(), serviceName: " \t", instanceID: "instance", environment: "test", wantError: "service name is required"},
		// 空白实例标识不能关联本进程的日志，验证拒绝初始化。
		{name: "blank instance ID", ctx: t.Context(), serviceName: "profile", instanceID: "\n", environment: "test", wantError: "instance ID is required"},
		// 空白环境未提供部署来源，验证初始化不会补默认值。
		{name: "blank environment", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: " ", wantError: "environment is required"},
		// IPv6 地址缺少闭合方括号，验证无法解析的 URL 会被拒绝。
		{name: "malformed endpoint", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "https://[::1", wantError: runtimeEndpointVariable},
		// 本包使用 OTLP HTTP 导出，验证其他 URL 协议会被拒绝。
		{name: "wrong endpoint scheme", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "ftp://collector.invalid/v1/traces", wantError: runtimeEndpointVariable},
		// URL 只有路径时没有接收地址，验证主机名是必需项。
		{name: "endpoint without host", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "https:///v1/traces", wantError: runtimeEndpointVariable},
		// 在 URL 中放入测试账号，验证接收地址不能包含用户信息。
		{name: "endpoint with user information", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "https://fixture@collector.invalid/v1/traces", wantError: runtimeEndpointVariable},
		// 只有问号的 URL 仍带查询部分，验证空查询也被拒绝。
		{name: "endpoint with empty query", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "https://collector.invalid/v1/traces?", wantError: runtimeEndpointVariable},
		// 带参数的接收地址应在创建导出器前被拒绝。
		{name: "endpoint with query", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "https://collector.invalid/v1/traces?fixture=yes", wantError: runtimeEndpointVariable},
		// URL 片段属于客户端定位信息，验证接收地址禁止片段。
		{name: "endpoint with fragment", ctx: t.Context(), serviceName: "profile", instanceID: "instance", environment: "test", endpoint: "https://collector.invalid/v1/traces#fixture", wantError: runtimeEndpointVariable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolateTracingRuntime(t)
			t.Setenv(runtimeEndpointVariable, test.endpoint)
			err := Init(test.ctx, test.serviceName, test.instanceID, env.Config{Environment: test.environment})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("Init() error = %v; want an error containing %q", err, test.wantError)
			}
			if shared.tracer != nil || shared.provider != nil {
				t.Error("failed Init() published an instance")
			}
		})
	}

	// 关闭导出地址后初始化真实 SDK，验证资源来自入参、根节点采样且原全局 Provider 保持原样。
	t.Run("initializes in-memory SDK", func(t *testing.T) {
		isolateTracingRuntime(t)
		previousGlobal := otel.GetTracerProvider()
		initTracingRuntimeFixture(t)
		instance := Current()
		recorder := tracetest.NewSpanRecorder()
		shared.provider.RegisterSpanProcessor(recorder)
		ctx, span := instance.StartHTTP(t.Context(), "GET", "/profiles")
		span.End()

		if !trace.SpanContextFromContext(ctx).IsSampled() {
			t.Error("root span is not sampled")
		}
		if got := otel.GetTracerProvider(); got != previousGlobal {
			t.Error("Init() changed the global OpenTelemetry provider")
		}
		ended := recorder.Ended()
		if len(ended) != 1 {
			t.Fatalf("ended spans = %d; want 1", len(ended))
		}
		resources := ended[0].Resource().Set()
		for _, want := range []attribute.KeyValue{
			attribute.String("service.name", "profile-service"),
			attribute.String("service.instance.id", "instance-a"),
			attribute.String("deployment.environment.name", "test"),
		} {
			if got, ok := resources.Value(want.Key); !ok || got != want.Value {
				t.Errorf("resource %q = %v, present %v; want %v", want.Key, got, ok, want.Value)
			}
		}
	})

	// 第二次传入不同实例信息，验证重复初始化报错且原 Tracer 和 Provider 均未被替换。
	t.Run("rejects a second initialization", func(t *testing.T) {
		isolateTracingRuntime(t)
		initTracingRuntimeFixture(t)
		previousTracer, previousProvider := Current(), shared.provider
		err := Init(t.Context(), "another-service", "instance-b", env.Config{Environment: "test"})
		if err == nil || !strings.Contains(err.Error(), "already initialized") {
			t.Errorf("second Init() error = %v; want already initialized", err)
		}
		if Current() != previousTracer || shared.provider != previousProvider {
			t.Error("second Init() replaced the published instance")
		}
	})
}

func TestCurrent(t *testing.T) {
	// 启动未完成时没有可用实例，验证调用 Current 会暴露装配顺序问题。
	t.Run("before initialization", func(t *testing.T) {
		isolateTracingRuntime(t)
		defer func() {
			if got := recover(); got != "tracing is not initialized; call Init before Current" {
				t.Errorf("Current() panic = %v; want initialization error", got)
			}
		}()
		Current()
	})

	// 成功初始化后连续获取实例，验证 Current 始终返回已发布的同一 Tracer。
	t.Run("returns the shared instance", func(t *testing.T) {
		isolateTracingRuntime(t)
		initTracingRuntimeFixture(t)
		first := Current()
		if first == nil || first != Current() {
			t.Error("Current() did not return a stable shared tracer")
		}
	})
}

type tracingShutdownProbe struct {
	*tracetest.SpanRecorder
	wantError error
	contexts  []context.Context
}

func TestShutdown(t *testing.T) {
	// 初始化失败后可能进入清理流程，验证没有 Provider 时即使无 Context 也可安全返回。
	t.Run("without a provider", func(t *testing.T) {
		isolateTracingRuntime(t)
		if err := Shutdown(nil); err != nil {
			t.Errorf("Shutdown() error = %v; want nil", err)
		}
	})

	// 已有 Provider 时缺少 Context 会阻止关闭，随后使用有效 Context 应只清理一次并保留原错误。
	t.Run("closes the provider once", func(t *testing.T) {
		isolateTracingRuntime(t)
		initTracingRuntimeFixture(t)
		instance := Current()
		wantError := errors.New("fixture processor shutdown failure")
		probe := &tracingShutdownProbe{SpanRecorder: tracetest.NewSpanRecorder(), wantError: wantError}
		shared.provider.RegisterSpanProcessor(probe)

		if err := Shutdown(nil); err == nil || !strings.Contains(err.Error(), "shutdown context is required") {
			t.Errorf("Shutdown(nil) error = %v; want missing context error", err)
		}
		if len(probe.contexts) != 0 {
			t.Error("Shutdown(nil) invoked the processor")
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := Shutdown(ctx); !errors.Is(err, wantError) {
			t.Errorf("Shutdown() error = %v; want %v", err, wantError)
		}
		if err := Shutdown(ctx); err != nil {
			t.Errorf("repeated Shutdown() error = %v; want nil", err)
		}
		if len(probe.contexts) != 1 || probe.contexts[0] != ctx {
			t.Errorf("processor shutdown contexts = %v; want exactly the supplied context", probe.contexts)
		}
		if Current() != instance {
			t.Error("Shutdown() discarded the published tracer")
		}
		if err := Init(ctx, "profile-service", "instance-b", env.Config{Environment: "test"}); err == nil || !strings.Contains(err.Error(), "already initialized") {
			t.Errorf("Init() after Shutdown() error = %v; want already initialized", err)
		}
	})
}

func isolateTracingRuntime(t *testing.T) {
	t.Helper()
	shared.Lock()
	previousTracer, previousProvider := shared.tracer, shared.provider
	shared.tracer, shared.provider = nil, nil
	shared.Unlock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := Shutdown(ctx); err != nil {
			t.Errorf("shutdown test runtime: %v", err)
		}
		shared.Lock()
		shared.tracer, shared.provider = previousTracer, previousProvider
		shared.Unlock()
	})
}

func initTracingRuntimeFixture(t *testing.T) {
	t.Helper()
	t.Setenv(runtimeEndpointVariable, "")
	if err := Init(t.Context(), "profile-service", "instance-a", env.Config{Environment: "test"}); err != nil {
		t.Fatalf("Init(): %v", err)
	}
}

func (probe *tracingShutdownProbe) Shutdown(ctx context.Context) error {
	probe.contexts = append(probe.contexts, ctx)
	return probe.wantError
}
