package sdkmock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const maxInvokeBodyBytes = 4 << 20 // 4 MiB

// RecordedCall 保存子进程对某一 (provider, operation) 发起的实际调用入参快照。
type RecordedCall struct {
	Provider   string
	Operation  string
	ParamsJSON json.RawMessage
}

// Behavior 定义匹配到 (provider, operation) 后的受控模拟动作。
type Behavior struct {
	delay      time.Duration
	resultJSON json.RawMessage
	fault      *FaultSpec
}

// RespondJSON 构造返回指定 JSON 载荷的成功模拟动作。
func RespondJSON(payload any) Behavior {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("sdkmock.RespondJSON marshal failed: %v", err))
	}
	return Behavior{resultJSON: data}
}

// RespondError 构造返回指定错误分类、HTTP 状态码、供应商业务码与消息的失败模拟动作。
func RespondError(code string, statusCode, providerCode int, message string) Behavior {
	if strings.TrimSpace(message) == "" {
		message = "simulated SDK error"
	}
	return Behavior{
		fault: &FaultSpec{
			Code:         code,
			StatusCode:   statusCode,
			ProviderCode: providerCode,
			Message:      message,
		},
	}
}

// WithDelay 在执行原 Behavior 前附加固定等待时间，用于模拟第三方 SDK 慢响应或超时。
func WithDelay(delay time.Duration, behavior Behavior) Behavior {
	behavior.delay = delay
	return behavior
}

type ruleKey struct {
	provider  string
	operation string
}

// Rule 跟踪某条已注册 (provider, operation) 规则及其收到的调用序列。
type Rule struct {
	mu        sync.Mutex
	provider  string
	operation string
	behavior  Behavior
	calls     []RecordedCall
}

// Calls 返回该规则已收到的调用快照副本。
func (r *Rule) Calls() []RecordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// RequireCallCount 断言当前 SDK 方法被调用了恰好 want 次，并返回每次调用快照。
func (r *Rule) RequireCallCount(t testing.TB, want int) []RecordedCall {
	t.Helper()
	calls := r.Calls()
	if len(calls) != want {
		t.Fatalf("sdkmock %s.%s call count = %d, want %d", r.provider, r.operation, len(calls), want)
	}
	return calls
}

func (r *Rule) record(call RecordedCall) Behavior {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	return r.behavior
}

// Controller 在父测试进程内运行本地 HTTP 控制面，集中管控子进程内的全部第三方 SDK 调用。
type Controller struct {
	server *httptest.Server

	mu         sync.Mutex
	rules      map[ruleKey]*Rule
	unexpected []RecordedCall
}

// NewController 启动监听 127.0.0.1 动态端口的跨进程 SDK Mock 控制面。
func NewController() *Controller {
	c := &Controller{
		rules: make(map[ruleKey]*Rule),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/invoke", c.handleInvoke)
	c.server = httptest.NewServer(mux)
	return c
}

// URL 返回控制面基础 URL，通过环境变量注入给被测子进程的装配边界。
func (c *Controller) URL() string {
	return c.server.URL
}

// Close 停止控制面监听并关闭空闲连接。
func (c *Controller) Close() {
	c.server.CloseClientConnections()
	c.server.Close()
}

// Expect 为当前用例注册 (provider, operation) 的模拟行为，返回用于核对调用记录的 Rule。
func (c *Controller) Expect(provider, operation string, behavior Behavior) *Rule {
	rule := &Rule{
		provider:  provider,
		operation: operation,
		behavior:  behavior,
	}
	c.mu.Lock()
	c.rules[ruleKey{provider: provider, operation: operation}] = rule
	c.mu.Unlock()
	return rule
}

// RequireNoCalls 断言当前用例期间没有任何第三方 SDK 被调用（无论是否注册过规则）。
func (c *Controller) RequireNoCalls(t testing.TB) {
	t.Helper()
	c.mu.Lock()
	var matchedSummaries []string
	for key, rule := range c.rules {
		if n := len(rule.Calls()); n > 0 {
			matchedSummaries = append(matchedSummaries, fmt.Sprintf("%s.%s (%d)", key.provider, key.operation, n))
		}
	}
	var unexpectedSummaries []string
	for _, call := range c.unexpected {
		unexpectedSummaries = append(unexpectedSummaries, fmt.Sprintf("%s.%s", call.Provider, call.Operation))
	}
	c.mu.Unlock()

	if len(matchedSummaries) > 0 || len(unexpectedSummaries) > 0 {
		t.Fatalf("expected zero third-party SDK calls, got matched=%v unexpected=%v",
			matchedSummaries, unexpectedSummaries)
	}
}

// VerifyAndReset 检查是否存在未注册的意外 SDK 调用，并清空所有规则与调用历史。
func (c *Controller) VerifyAndReset() error {
	c.mu.Lock()
	unexpected := c.unexpected
	c.unexpected = nil
	c.rules = make(map[ruleKey]*Rule)
	c.mu.Unlock()

	if len(unexpected) == 0 {
		return nil
	}
	summaries := make([]string, 0, len(unexpected))
	for _, call := range unexpected {
		summaries = append(summaries, fmt.Sprintf("%s.%s", call.Provider, call.Operation))
	}
	return fmt.Errorf("sdkmock received %d unexpected SDK call(s): %s", len(unexpected), strings.Join(summaries, ", "))
}

func (c *Controller) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxInvokeBodyBytes))
	_ = r.Body.Close()
	if err != nil {
		http.Error(w, "read request body failed", http.StatusBadRequest)
		return
	}
	var req InvokeRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		http.Error(w, "invalid invoke payload", http.StatusBadRequest)
		return
	}

	recorded := RecordedCall{
		Provider:   req.Provider,
		Operation:  req.Operation,
		ParamsJSON: append(json.RawMessage(nil), req.Params...),
	}

	c.mu.Lock()
	rule, ok := c.rules[ruleKey{provider: req.Provider, operation: req.Operation}]
	if !ok {
		c.unexpected = append(c.unexpected, recorded)
		c.mu.Unlock()
		writeInvokeJSON(w, http.StatusBadGateway, InvokeResponse{
			Error: &FaultSpec{
				Code:       "UNEXPECTED_CALL",
				StatusCode: http.StatusBadGateway,
				Message:    fmt.Sprintf("unexpected SDK call to %s.%s", req.Provider, req.Operation),
			},
		})
		return
	}
	c.mu.Unlock()

	behavior := rule.record(recorded)
	if behavior.delay > 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(behavior.delay):
		}
	}

	if behavior.fault != nil {
		faultCopy := *behavior.fault
		writeInvokeJSON(w, http.StatusOK, InvokeResponse{Error: &faultCopy})
		return
	}
	writeInvokeJSON(w, http.StatusOK, InvokeResponse{Result: behavior.resultJSON})
}

func writeInvokeJSON(w http.ResponseWriter, statusCode int, resp InvokeResponse) {
	data, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write(data)
}
