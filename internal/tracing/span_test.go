package tracing_test

import (
	"testing"
	"time"

	"go-service-template/internal/tracing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSpanFail(t *testing.T) {
	// 空指针和零值都没有可更新的节点，标记失败时应安全返回。
	t.Run("empty handles", func(t *testing.T) {
		var absent *tracing.Span
		absent.Fail("operation failed")
		new(tracing.Span).Fail("operation failed")
	})

	// 为运行中的节点提供固定失败说明，验证标记状态后仍由调用方负责结束。
	t.Run("marks failure without ending", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartService(t.Context(), "service Save")
		span.Fail("Unable to save")
		if got := len(recorder.Ended()); got != 0 {
			t.Errorf("ended spans after Fail() = %d; want 0", got)
		}
		span.End()
		recorded := requireOnlyEndedBoundarySpan(t, recorder)
		assertBoundaryStatus(t, recorded, codes.Error, "Unable to save")
		if got := len(recorded.Events()); got != 0 {
			t.Errorf("recorded events = %d; want 0", got)
		}
	})
}

func TestSpanEnd(t *testing.T) {
	// 对未标记失败的节点重复 End，验证正常结束只提交一次且状态保持 Unset。
	t.Run("ends once", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartController(t.Context(), "controller Get")
		span.End()
		span.End()
		assertBoundaryStatus(t, requireOnlyEndedBoundarySpan(t, recorder), codes.Unset, "")
	})

	// defer 直接调用 End 时，验证记录受控说明并向上抛出同一个 panic 值。
	t.Run("records and propagates panic", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartService(t.Context(), "service Save")
		panicValue := &struct{ message string }{message: "private panic fixture"}
		assertBoundaryPanic(t, func() {
			defer span.End()
			panic(panicValue)
		}, panicValue)

		recorded := requireOnlyEndedBoundarySpan(t, recorder)
		assertBoundaryStatus(t, recorded, codes.Error, "Operation terminated by panic")
		if got := len(recorded.Events()); got != 0 {
			t.Errorf("recorded panic events = %d; want 0", got)
		}
	})

	// 在没有有效节点的路径仍可安全执行收尾，正常返回和 panic 都保留原有控制流程。
	t.Run("empty handles", func(t *testing.T) {
		var absent *tracing.Span
		absent.End()
		new(tracing.Span).End()
		assertBoundaryPanic(t, func() {
			defer absent.End()
			panic("operation panic")
		}, "operation panic")
	})
}

type boundaryResultCase struct {
	name        string
	statusCode  int
	failure     string
	wantCode    codes.Code
	wantMessage string
}

func TestSpanEndHTTP(t *testing.T) {
	tests := []boundaryResultCase{
		// 正常响应保存状态码，节点状态保持 Unset。
		{name: "success", statusCode: 200, wantCode: codes.Unset},
		// 5xx 的前一个状态属于客户端错误，单凭该状态不标记服务端故障。
		{name: "client error at 499", statusCode: 499, wantCode: codes.Unset},
		// 500 是服务端故障的起点，即使没有说明也应标记失败。
		{name: "server error at 500", statusCode: 500, wantCode: codes.Error},
		// 599 仍在服务端错误范围内，应标记失败。
		{name: "server error at 599", statusCode: 599, wantCode: codes.Error},
		// 600 超出 5xx 范围，验证仅保存实际状态。
		{name: "outside 5xx", statusCode: 600, wantCode: codes.Unset},
		// 未知状态传 0，验证不会产生 HTTP 状态属性。
		{name: "unknown status", wantCode: codes.Unset},
		// 响应头已写出 200 后仍可能发生写入错误，验证显式说明会标记失败。
		{name: "write failure after success", statusCode: 200, failure: "Response write failed", wantCode: codes.Error, wantMessage: "Response write failed"},
	}
	testBoundaryResults(t, func(instance *tracing.Tracer, t *testing.T) *tracing.Span {
		_, span := instance.StartHTTP(t.Context(), "GET", "/profiles")
		return span
	}, (*tracing.Span).EndHTTP, tests)

	// 控制器节点不接收入站 HTTP 结果，空句柄也无需记录。
	t.Run("ignores other boundaries", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartController(t.Context(), "controller Get")
		testIgnoredBoundaryFinish(t, recorder, span, (*tracing.Span).EndHTTP)
	})
}

func TestSpanEndCompanyHTTP(t *testing.T) {
	tests := []boundaryResultCase{
		// 已确认的正常公司响应应记录其状态码并保持成功路径的默认状态。
		{name: "success", statusCode: 201, wantCode: codes.Unset},
		// 收到成功状态后发生解码失败，验证同时保留真实状态和受控失败说明。
		{name: "decode failure", statusCode: 200, failure: "Decode failed", wantCode: codes.Error, wantMessage: "Decode failed"},
		// 发送失败时没有实际响应，验证只记录失败说明。
		{name: "no response", failure: "Request failed", wantCode: codes.Error, wantMessage: "Request failed"},
	}
	testBoundaryResults(t, func(instance *tracing.Tracer, t *testing.T) *tracing.Span {
		_, span := instance.StartCompanyClient(t.Context(), "company_client Lookup")
		return span
	}, (*tracing.Span).EndCompanyHTTP, tests)

	// SDK 节点不能被公司协议的结果结束，空句柄也应安全返回。
	t.Run("ignores other boundaries", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartSDKClient(t.Context(), "sdk_client Lookup")
		testIgnoredBoundaryFinish(t, recorder, span, (*tracing.Span).EndCompanyHTTP)
	})
}

func TestSpanEndSDKClient(t *testing.T) {
	tests := []boundaryResultCase{
		// SDK 已取得实际 HTTP 状态时，验证该状态正常保留。
		{name: "response status", statusCode: 200, wantCode: codes.Unset},
		// SDK 返回成功但未提供 HTTP 状态，验证不伪造状态属性。
		{name: "no HTTP status", wantCode: codes.Unset},
		// HTTP 请求成功但返回值校验失败，验证受控说明决定 SDK 调用结果。
		{name: "validation failure", statusCode: 200, failure: "Response invalid", wantCode: codes.Error, wantMessage: "Response invalid"},
	}
	testBoundaryResults(t, func(instance *tracing.Tracer, t *testing.T) *tracing.Span {
		_, span := instance.StartSDKClient(t.Context(), "sdk_client Generate")
		return span
	}, (*tracing.Span).EndSDKClient, tests)

	// 公司客户端节点不能被第三方 SDK 结果结束，空句柄也应安全返回。
	t.Run("ignores other boundaries", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartCompanyClient(t.Context(), "company_client Generate")
		testIgnoredBoundaryFinish(t, recorder, span, (*tracing.Span).EndSDKClient)
	})
}

type boundaryRouteCase struct {
	name      string
	route     string
	wantName  string
	wantRoute string
}

func TestSpanSetHTTPRoute(t *testing.T) {
	tests := []boundaryRouteCase{
		// 未确定路由时保持未解析名称，不写入注册路由属性。
		{name: "empty route", wantName: "GET <unresolved>"},
		// 显式传入未解析占位也只更新名称。
		{name: "unresolved route", route: "<unresolved>", wantName: "GET <unresolved>"},
		// 确认没有匹配时更新名称，未匹配状态仍不属于真实注册模板。
		{name: "unmatched route", route: "<unmatched>", wantName: "GET <unmatched>"},
		// 匹配成功后用注册模板更新原节点的名称和路由属性。
		{name: "registered route", route: "/profiles/:id", wantName: "GET /profiles/:id", wantRoute: "/profiles/:id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance, recorder := newRecordingBoundaryTracer(t)
			_, span := instance.StartHTTP(t.Context(), "GET", "")
			if started := recorder.Started(); len(started) != 1 || started[0].Name() != "GET <unresolved>" {
				t.Fatalf("start did not create the unresolved HTTP span")
			}
			span.SetHTTPRoute(test.route)
			span.End()
			recorded := requireOnlyEndedBoundarySpan(t, recorder)
			if recorded.Name() != test.wantName {
				t.Errorf("span name = %q; want %q", recorded.Name(), test.wantName)
			}
			var wantRoute attribute.Value
			if test.wantRoute != "" {
				wantRoute = attribute.StringValue(test.wantRoute)
			}
			assertBoundaryAttribute(t, recorded, "http.route", wantRoute)
			if got := len(recorder.Started()); got != 1 {
				t.Errorf("started spans = %d; want 1", got)
			}
		})
	}

	// 将注册模板提交给业务节点和空句柄，验证路由更新仅作用于入站 HTTP 节点。
	t.Run("ignores other boundaries", func(t *testing.T) {
		instance, recorder := newRecordingBoundaryTracer(t)
		_, span := instance.StartService(t.Context(), "service Lookup")
		var absent *tracing.Span
		absent.SetHTTPRoute("/profiles/:id")
		new(tracing.Span).SetHTTPRoute("/profiles/:id")
		span.SetHTTPRoute("/profiles/:id")
		span.End()
		recorded := requireOnlyEndedBoundarySpan(t, recorder)
		if recorded.Name() != "service Lookup" {
			t.Errorf("span name = %q; want service Lookup", recorded.Name())
		}
		assertBoundaryAttribute(t, recorded, "http.route", attribute.Value{})
	})
}

func testBoundaryResults(
	t *testing.T,
	start func(*tracing.Tracer, *testing.T) *tracing.Span,
	end func(*tracing.Span, int, string, time.Time),
	tests []boundaryResultCase,
) {
	t.Helper()
	finishedAt := time.Date(2100, time.January, 1, 0, 0, 1, 0, time.UTC)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance, recorder := newRecordingBoundaryTracer(t)
			span := start(instance, t)
			end(span, test.statusCode, test.failure, finishedAt)
			span.End()
			recorded := requireOnlyEndedBoundarySpan(t, recorder)
			if !recorded.EndTime().Equal(finishedAt) {
				t.Errorf("span end time = %v; want %v", recorded.EndTime(), finishedAt)
			}
			var statusCode attribute.Value
			if test.statusCode != 0 {
				statusCode = attribute.IntValue(test.statusCode)
			}
			assertBoundaryAttribute(t, recorded, "http.response.status_code", statusCode)
			assertBoundaryStatus(t, recorded, test.wantCode, test.wantMessage)
		})
	}
}

func testIgnoredBoundaryFinish(
	t *testing.T,
	recorder *tracetest.SpanRecorder,
	span *tracing.Span,
	end func(*tracing.Span, int, string, time.Time),
) {
	t.Helper()
	finishedAt := time.Date(2100, time.January, 1, 0, 0, 1, 0, time.UTC)
	end(nil, 500, "Unexpected failure", finishedAt)
	end(new(tracing.Span), 500, "Unexpected failure", finishedAt)
	end(span, 500, "Unexpected failure", finishedAt)
	if got := len(recorder.Ended()); got != 0 {
		t.Errorf("ended spans after finishing the wrong boundary = %d; want 0", got)
	}
	span.End()
	recorded := requireOnlyEndedBoundarySpan(t, recorder)
	assertBoundaryAttribute(t, recorded, "http.response.status_code", attribute.Value{})
	assertBoundaryStatus(t, recorded, codes.Unset, "")
}

func assertBoundaryStatus(t *testing.T, span sdktrace.ReadOnlySpan, code codes.Code, description string) {
	t.Helper()
	status := span.Status()
	if status.Code != code || status.Description != description {
		t.Errorf("span status = %v, %q; want %v, %q", status.Code, status.Description, code, description)
	}
}
