// Package flow 提供与具体业务解耦的多步骤流程执行引擎、单步级外部依赖结算检查点与脱敏故障轨迹输出。
package flow

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go-service-template/tests/testkit/apicall"
	"go-service-template/tests/testkit/httpstub"
	"go-service-template/tests/testkit/sdkmock"
)

// Freezable 抽象前置数据封存会话（如 *seed.Session），用于在进入业务步骤时确保前置写库入口已关闭。
type Freezable interface {
	Freeze()
	IsFrozen() bool
}

// Config 定义多步业务流程执行器关联的通用协议客户端与受控外部依赖模拟器。
type Config struct {
	HTTP      *apicall.Client
	SDKMock   *sdkmock.Controller
	HTTPStubs map[string]*httpstub.Server
}

// Option 配置 Runner 的可选扩展行为。
type Option func(*Runner)

// WithSeed 绑定前置数据会话；首次进入 Step 时会自动校验并封存，杜绝流程中途违规写库。
func WithSeed(seedSession Freezable) Option {
	return func(r *Runner) {
		r.seedGuard = seedSession
	}
}

type stepHistoryEntry struct {
	name     string
	status   string
	duration time.Duration
	mu       sync.Mutex
	calls    []apicall.CallRecord
}

func (e *stepHistoryEntry) recordCall(rec apicall.CallRecord) {
	e.mu.Lock()
	e.calls = append(e.calls, rec)
	e.mu.Unlock()
}

func (e *stepHistoryEntry) snapshotCalls() []apicall.CallRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

// Runner 按产品操作顺序推进具名业务步骤，负责单步外部 Mock 隔离结算、前置步骤失败熔断与执行轨迹输出。
type Runner struct {
	t         testing.TB
	http      *apicall.Client
	sdkMock   *sdkmock.Controller
	httpStubs map[string]*httpstub.Server
	seedGuard Freezable

	seenNames map[string]struct{}
	history   []*stepHistoryEntry
}

// New 创建绑定当前测试上下文的业务流程执行器。
func New(t testing.TB, cfg Config, opts ...Option) *Runner {
	t.Helper()
	if cfg.HTTP == nil {
		t.Fatal("flow.New requires a non-nil apicall.Client")
	}
	copiedStubs := make(map[string]*httpstub.Server, len(cfg.HTTPStubs))
	maps.Copy(copiedStubs, cfg.HTTPStubs)

	r := &Runner{
		t:         t,
		http:      cfg.HTTP,
		sdkMock:   cfg.SDKMock,
		httpStubs: copiedStubs,
		seenNames: make(map[string]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

type expectedSDKItem struct {
	label string
	rule  *sdkmock.Rule
}

type expectedHTTPItem struct {
	label string
	route *httpstub.RouteExpectation
}

// Step 表示业务流程中的单个有序执行步骤，暴露观测版 HTTP 客户端与本步专属外部依赖注册入口。
type Step struct {
	runner       *Runner
	name         string
	stepHTTP     *apicall.Client
	expectedSDK  []expectedSDKItem
	expectedHTTP []expectedHTTPItem
}

// T 返回当前测试句柄，供领域薄调用函数与标准断言辅助使用。
func (s *Step) T() testing.TB {
	return s.runner.t
}

// Name 返回当前业务步骤名称。
func (s *Step) Name() string {
	return s.name
}

// HTTP 返回自动记录本步请求轨迹（脱敏）的测试 HTTP 客户端。
func (s *Step) HTTP() *apicall.Client {
	return s.stepHTTP
}

// ExpectSDK 在当前步骤内声明第三方 SDK 调用预期；本步骤结束时自动校验其已被实际调用并清空规则，不泄漏至后续步骤。
func (s *Step) ExpectSDK(provider, operation string, behavior sdkmock.Behavior) *sdkmock.Rule {
	s.runner.t.Helper()
	if s.runner.sdkMock == nil {
		s.runner.t.Fatalf("step %q called ExpectSDK, but Runner has no sdkmock.Controller configured", s.name)
	}
	rule := s.runner.sdkMock.Expect(provider, operation, behavior)
	s.expectedSDK = append(s.expectedSDK, expectedSDKItem{
		label: provider + "." + operation,
		rule:  rule,
	})
	return rule
}

// ExpectHTTP 在当前步骤内声明内部 HTTP 服务调用预期；本步骤结束时自动校验其已被实际调用并清空规则，不泄漏至后续步骤。
func (s *Step) ExpectHTTP(serviceName, method, path string, responder httpstub.Responder) *httpstub.RouteExpectation {
	s.runner.t.Helper()
	stub, ok := s.runner.httpStubs[serviceName]
	if !ok || stub == nil {
		s.runner.t.Fatalf("step %q called ExpectHTTP for unknown service stub %q", s.name, serviceName)
	}
	route := stub.Expect(method, path, responder)
	s.expectedHTTP = append(s.expectedHTTP, expectedHTTPItem{
		label: fmt.Sprintf("%s %s %s", serviceName, strings.ToUpper(strings.TrimSpace(method)), path),
		route: route,
	})
	return route
}

// Step 按顺序执行一个具名产品步骤：
//  1. 首次执行时自动确保绑定的前置数据会话已封存（Freeze）；
//  2. 执行步骤闭包并采集每次 HTTP 调用的脱敏元数据（方法、路径、状态码、耗时与 TraceId）；
//  3. 步骤结束时立即结算本步外部依赖预期（未消费预期或未声明意外调用均判败）并重置模拟端点；
//  4. 若步骤失败，输出从第 1 步至当前步骤的完整执行轨迹并立即终止后续依赖步骤。
func (r *Runner) Step(name string, fn func(s *Step)) {
	r.t.Helper()
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		r.t.Fatal("flow.Runner.Step requires a non-blank step name")
	}
	if _, dup := r.seenNames[trimmed]; dup {
		r.t.Fatalf("duplicate step name %q in business flow", trimmed)
	}
	r.seenNames[trimmed] = struct{}{}

	if r.seedGuard != nil && !r.seedGuard.IsFrozen() {
		r.seedGuard.Freeze()
	}
	if preErr := r.verifyAndResetMocks(); preErr != nil {
		r.t.Fatalf("unclean mock state before step %q: %v", trimmed, preErr)
	}

	entry := &stepHistoryEntry{
		name:   trimmed,
		status: "RUNNING",
	}
	r.history = append(r.history, entry)

	stepCtx := &Step{
		runner:   r,
		name:     trimmed,
		stepHTTP: r.http.WithObserver(entry.recordCall),
	}

	startedAt := time.Now()
	completedNormally := false
	settledMocks := false

	defer func() {
		entry.duration = time.Since(startedAt)
		if !completedNormally {
			entry.status = "FAIL"
			var extraDiag string
			if !settledMocks {
				if mockErr := r.verifyAndResetMocks(); mockErr != nil {
					extraDiag = fmt.Sprintf("\nOutbound mock diagnostic during failed step %q: %v", trimmed, mockErr)
				}
			}
			r.t.Logf("\n%s%s", r.FormatTrail(trimmed), extraDiag)
		}
	}()

	fn(stepCtx)
	completedNormally = true
	entry.duration = time.Since(startedAt)

	unconsumedErr := verifyConsumedExpectations(stepCtx)
	unexpectedErr := r.verifyAndResetMocks()
	settledMocks = true

	checkpointErr := errors.Join(unconsumedErr, unexpectedErr)
	if checkpointErr != nil || r.t.Failed() {
		entry.status = "FAIL"
		r.t.Logf("\n%s", r.FormatTrail(trimmed))
		if checkpointErr != nil {
			r.t.Fatalf("step %q outbound dependency checkpoint failed: %v", trimmed, checkpointErr)
		}
		r.t.FailNow()
	}

	entry.status = "PASS"
}

func verifyConsumedExpectations(s *Step) error {
	var unconsumed []string
	for _, item := range s.expectedSDK {
		if len(item.rule.Calls()) == 0 {
			unconsumed = append(unconsumed, "sdk:"+item.label)
		}
	}
	for _, item := range s.expectedHTTP {
		if len(item.route.Calls()) == 0 {
			unconsumed = append(unconsumed, "http:"+item.label)
		}
	}
	if len(unconsumed) == 0 {
		return nil
	}
	return fmt.Errorf("expected outbound call(s) never occurred in step %q: %s", s.name, strings.Join(unconsumed, ", "))
}

func (r *Runner) verifyAndResetMocks() error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(r.httpStubs)) {
		stub := r.httpStubs[name]
		if stub == nil {
			continue
		}
		if err := stub.VerifyAndReset(); err != nil {
			errs = append(errs, fmt.Errorf("http stub %q: %w", name, err))
		}
	}
	if r.sdkMock != nil {
		if err := r.sdkMock.VerifyAndReset(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// FormatTrail 生成当前已执行步骤的脱敏轨迹摘要表（仅含步骤状态、HTTP 方法路径、状态码、耗时与 TraceId）。
func (r *Runner) FormatTrail(failedStep string) string {
	var b strings.Builder
	if failedStep != "" {
		fmt.Fprintf(&b, "=== Business Flow Execution Trail (failed at step [%s]) ===\n", failedStep)
	} else {
		b.WriteString("=== Business Flow Execution Trail ===\n")
	}
	for _, item := range r.history {
		fmt.Fprintf(&b, "  [%s] %s (%s)\n", item.status, item.name, item.duration.Round(time.Millisecond))
		for _, call := range item.snapshotCalls() {
			tracePart := ""
			if call.TraceID != "" {
				tracePart = fmt.Sprintf(", TraceId=%s", call.TraceID)
			}
			fmt.Fprintf(&b, "         -> %-4s %s => HTTP %d (%s%s)\n",
				call.Method, call.Path, call.StatusCode, call.Duration.Round(time.Millisecond), tracePart)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
