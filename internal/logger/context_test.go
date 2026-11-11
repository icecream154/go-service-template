package logger

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"go-service-template/internal/tracing"
)

type operationCallerFixture struct{}

type currentOperationCase struct {
	name       string
	enter      func(context.Context) context.Context
	nilContext bool
	want       operationInfo
}

func TestWithCurrentOperation(t *testing.T) {
	fixture := &operationCallerFixture{}
	entry := operationInfo{component: ComponentService, name: "profile.FixtureService.Entry"}
	seedOperationCaller(t, (*operationCallerFixture).Entry, entry)
	seedOperationCaller(t, (*operationCallerFixture).ThroughHelper, entry)

	tests := []currentOperationCase{
		// 直接进入已登记的具名方法，验证通过当前调用帧识别入口并覆盖父 HTTP 操作。
		{name: "recognized direct caller", enter: fixture.Entry, want: entry},
		// 已登记的入口经未识别的辅助函数建立作用域，验证只看直接调用帧且不借用父操作。
		{name: "unrecognized helper shadows parent", enter: fixture.ThroughHelper},
		// 无父上下文时进入已登记的入口，验证可建立根操作作用域。
		{name: "nil context uses background", enter: fixture.Entry, nilContext: true, want: entry},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var parent context.Context
			var cancel context.CancelFunc
			if !test.nilContext {
				parent, cancel = newOperationTestParent(t)
			}

			child := test.enter(parent)
			assertOperationInfo(t, child, test.want)
			if test.nilContext {
				if child.Done() != nil || tracing.TraceID(child) != "" {
					t.Error("root operation context has cancellation or a trace ID")
				}
				return
			}
			assertOperationParentPreserved(t, child, parent, cancel)
		})
	}
}

type operationReadCase struct {
	name string
	ctx  context.Context
	want string
}

func TestOperation(t *testing.T) {
	parent := WithHTTPOperation(t.Context(), "GET", "/profiles/:id")
	tests := []operationReadCase{
		// 使用已保存的 HTTP 作用域，验证读取已有名称而不重新识别调用栈。
		{name: "reads saved operation", ctx: parent, want: "GET /profiles/:id"},
		// 在有效操作下覆盖空作用域，验证读取不会继续查找更上层的名称。
		{name: "empty scope shadows parent", ctx: context.WithValue(parent, operationContextKey{}, operationInfo{})},
		// 普通上下文没有私有操作键，验证读取返回空值。
		{name: "no operation", ctx: t.Context()},
		// 传入 nil 表示没有上下文，验证读取返回空值。
		{name: "nil context"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Operation(test.ctx); got != test.want {
				t.Errorf("Operation() = %q, want %q", got, test.want)
			}
		})
	}
}

type httpOperationCase struct {
	name          string
	method        string
	routeTemplate string
	nilContext    bool
	want          operationInfo
}

func TestWithHTTPOperation(t *testing.T) {
	tests := []httpOperationCase{
		// 带路径参数的注册路由应保持模板原样，并以 HTTP 入口组件标记。
		{
			name: "registered route", method: "GET", routeTemplate: "/profiles/:id",
			want: operationInfo{component: ComponentHTTPEntry, name: "GET /profiles/:id"},
		},
		// 小写方法配未匹配占位值，验证组件直接采用框架提供的文本。
		{
			name: "preserves supplied text", method: "get", routeTemplate: "<unmatched>",
			want: operationInfo{component: ComponentHTTPEntry, name: "get <unmatched>"},
		},
		// 只有模板而没有方法时，验证派生空作用域并遮蔽父操作。
		{name: "missing method", routeTemplate: "/profiles/:id"},
		// 只有方法而没有模板时，验证派生空作用域并遮蔽父操作。
		{name: "missing route", method: "GET"},
		// 无父上下文且已知方法和模板，验证可建立根 HTTP 操作作用域。
		{
			name: "nil context uses background", method: "GET", routeTemplate: "<unresolved>", nilContext: true,
			want: operationInfo{component: ComponentHTTPEntry, name: "GET <unresolved>"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var parent context.Context
			var cancel context.CancelFunc
			if !test.nilContext {
				parent, cancel = newOperationTestParent(t)
			}

			child := WithHTTPOperation(parent, test.method, test.routeTemplate)
			assertOperationInfo(t, child, test.want)
			if test.nilContext {
				if child.Done() != nil || tracing.TraceID(child) != "" {
					t.Error("root HTTP operation context has cancellation or a trace ID")
				}
				return
			}
			assertOperationParentPreserved(t, child, parent, cancel)
		})
	}
}

type operationTestValueKey struct{}

const operationTestTraceID = "0123456789abcdef0123456789abcdef"

func (*operationCallerFixture) Entry(ctx context.Context) context.Context {
	return WithCurrentOperation(ctx)
}

func (*operationCallerFixture) ThroughHelper(ctx context.Context) context.Context {
	return withUnrecognizedOperation(ctx)
}

func withUnrecognizedOperation(ctx context.Context) context.Context {
	return WithCurrentOperation(ctx)
}

func seedOperationCaller(t *testing.T, method func(*operationCallerFixture, context.Context) context.Context, info operationInfo) {
	t.Helper()
	function := runtime.FuncForPC(reflect.ValueOf(method).Pointer())
	if function == nil {
		t.Fatal("runtime symbol for operation fixture is unavailable")
	}
	symbol := function.Name()
	preserveOperationCacheEntry(t, symbol)
	operationCache.Store(symbol, info)
}

func newOperationTestParent(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)
	ctx = context.WithValue(ctx, operationTestValueKey{}, "preserved")
	ctx = tracing.WithTraceID(ctx, operationTestTraceID)
	return WithHTTPOperation(ctx, "GET", "/parent/:id"), cancel
}

func assertOperationInfo(t *testing.T, ctx context.Context, want operationInfo) {
	t.Helper()
	if ctx == nil {
		t.Fatal("operation context is nil")
	}
	if got := operationFromContext(ctx); got != want {
		t.Errorf("operation info = %+v, want %+v", got, want)
	}
}

func assertOperationParentPreserved(t *testing.T, child, parent context.Context, cancel context.CancelFunc) {
	t.Helper()
	assertOperationInfo(t, parent, operationInfo{component: ComponentHTTPEntry, name: "GET /parent/:id"})
	if got := tracing.TraceID(child); got != operationTestTraceID {
		t.Errorf("child trace ID = %q, want %q", got, operationTestTraceID)
	}
	if got := child.Value(operationTestValueKey{}); got != "preserved" {
		t.Errorf("child context value = %v, want preserved", got)
	}
	wantDeadline, _ := parent.Deadline()
	if got, ok := child.Deadline(); !ok || !got.Equal(wantDeadline) {
		t.Errorf("child deadline = %v, %t, want %v, true", got, ok, wantDeadline)
	}
	cancel()
	if err := child.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("child error after parent cancellation = %v, want %v", err, context.Canceled)
	}
}
