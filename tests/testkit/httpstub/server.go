// Package httpstub 提供可跨测试用例复用的通用下游 HTTP 协议模拟服务，不绑定任何业务域名或传输契约。
package httpstub

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

const maxCapturedRequestBodyBytes = 8 << 20 // 8 MiB

// RecordedRequest 保存单次到达模拟服务的 HTTP 请求完整快照，供测试断言下游调用参数与追踪头。
type RecordedRequest struct {
	Method      string
	Path        string
	RawQuery    string
	Header      http.Header
	Body        []byte
	onExhausted func()
}

// Responder 定义匹配路由后的受控响应行为。
type Responder func(w http.ResponseWriter, req RecordedRequest)

// JSONResponse 返回指定 HTTP 状态码与 JSON 序列化正文的 Responder。
func JSONResponse(statusCode int, payload any) Responder {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("httpstub.JSONResponse marshal failed: %v", err))
	}
	return RawResponse(statusCode, "application/json; charset=utf-8", data)
}

// RawResponse 返回指定 HTTP 状态码、Content-Type 与原始字节正文的 Responder。
func RawResponse(statusCode int, contentType string, body []byte) Responder {
	copied := append([]byte(nil), body...)
	return func(w http.ResponseWriter, _ RecordedRequest) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(statusCode)
		_, _ = w.Write(copied)
	}
}

// DelayedResponse 在等待指定时长后再执行内层 Responder，用于模拟慢下游与超时控制。
func DelayedResponse(delay time.Duration, next Responder) Responder {
	return func(w http.ResponseWriter, req RecordedRequest) {
		if delay > 0 {
			time.Sleep(delay)
		}
		next(w, req)
	}
}

// Sequence 按调用先后顺序依次返回 responders 中的响应；超出预定次数时返回 502 并记入未预期调用违规列表。
func Sequence(responders ...Responder) Responder {
	if len(responders) == 0 {
		panic("httpstub.Sequence requires at least one Responder")
	}
	copied := make([]Responder, len(responders))
	for i, responder := range responders {
		if responder == nil {
			panic(fmt.Sprintf("httpstub.Sequence responder at index %d is nil", i))
		}
		copied[i] = responder
	}
	var (
		mu  sync.Mutex
		idx int
	)
	return func(w http.ResponseWriter, req RecordedRequest) {
		mu.Lock()
		current := idx
		idx++
		mu.Unlock()

		if current >= len(copied) {
			if req.onExhausted != nil {
				req.onExhausted()
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"httpstub sequence exhausted"}`))
			return
		}
		copied[current](w, req)
	}
}

type routeKey struct {
	method string
	path   string
}

// RouteExpectation 保存某条已注册路由的预期响应函数及实际收到的请求记录。
type RouteExpectation struct {
	mu        sync.Mutex
	method    string
	path      string
	responder Responder
	calls     []RecordedRequest
}

// Calls 返回当前路由已收到的请求副本切片。
func (r *RouteExpectation) Calls() []RecordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedRequest, len(r.calls))
	copy(out, r.calls)
	return out
}

// RequireCallCount 断言当前路由被调用了恰好 want 次，并返回全部请求快照供进一步检查正文与请求头。
func (r *RouteExpectation) RequireCallCount(t testing.TB, want int) []RecordedRequest {
	t.Helper()
	calls := r.Calls()
	if len(calls) != want {
		t.Fatalf("httpstub route %s %s call count = %d, want %d", r.method, r.path, len(calls), want)
	}
	return calls
}

func (r *RouteExpectation) record(req RecordedRequest) Responder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	return r.responder
}

// Server 封装基于本机回环端口的可重置 HTTP 模拟服务端，自身不持有单次测试的 testing.T。
type Server struct {
	server *httptest.Server

	mu         sync.Mutex
	routes     map[routeKey]*RouteExpectation
	unexpected []RecordedRequest
}

// NewServer 启动监听 127.0.0.1 动态端口的受控 HTTP 模拟服务。
// 默认未注册任何路由：任何未显式声明的出站调用均返回 502 并记入违规列表。
func NewServer() *Server {
	s := &Server{
		routes: make(map[routeKey]*RouteExpectation),
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL 返回当前模拟服务端的基础地址（如 "http://127.0.0.1:52345"）。
func (s *Server) URL() string {
	return s.server.URL
}

// Close 关闭底层 HTTP 监听并断开残余连接。
func (s *Server) Close() {
	s.server.CloseClientConnections()
	s.server.Close()
}

// Expect 为当前测试轮次注册 (Method, Path) 的受控响应行为，返回可用于核对调用详情的句柄。
func (s *Server) Expect(method, path string, responder Responder) *RouteExpectation {
	if responder == nil {
		panic("httpstub.Server.Expect requires a non-nil Responder")
	}
	normalizedMethod := strings.ToUpper(strings.TrimSpace(method))
	expectation := &RouteExpectation{
		method:    normalizedMethod,
		path:      path,
		responder: responder,
	}
	s.mu.Lock()
	s.routes[routeKey{method: normalizedMethod, path: path}] = expectation
	s.mu.Unlock()
	return expectation
}

// RequireNoCalls 断言本模拟服务在当前用例期间未收到任何已注册或未注册的请求。
func (s *Server) RequireNoCalls(t testing.TB) {
	t.Helper()
	s.mu.Lock()
	unexpectedCount := len(s.unexpected)
	var matchedSummaries []string
	for key, exp := range s.routes {
		if n := len(exp.Calls()); n > 0 {
			matchedSummaries = append(matchedSummaries, fmt.Sprintf("%s %s (%d)", key.method, key.path, n))
		}
	}
	var unexpectedSummaries []string
	for _, req := range s.unexpected {
		unexpectedSummaries = append(unexpectedSummaries, fmt.Sprintf("%s %s", req.Method, req.Path))
	}
	s.mu.Unlock()

	if unexpectedCount > 0 || len(matchedSummaries) > 0 {
		t.Fatalf("expected zero outbound HTTP calls to stub %s, got matched=%v unexpected=%v",
			s.server.URL, matchedSummaries, unexpectedSummaries)
	}
}

// VerifyAndReset 检查当前轮次是否存在未注册的意外请求，同时清空全部路由规则与请求记录，供下一条用例干净复用。
func (s *Server) VerifyAndReset() error {
	s.mu.Lock()
	unexpected := s.unexpected
	s.unexpected = nil
	s.routes = make(map[routeKey]*RouteExpectation)
	s.mu.Unlock()

	if len(unexpected) == 0 {
		return nil
	}
	summaries := make([]string, 0, len(unexpected))
	for _, req := range unexpected {
		summaries = append(summaries, fmt.Sprintf("%s %s", req.Method, req.Path))
	}
	return fmt.Errorf("httpstub received %d unexpected request(s): %s", len(unexpected), strings.Join(summaries, ", "))
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxCapturedRequestBodyBytes))
	_ = r.Body.Close()

	recorded := RecordedRequest{
		Method:   strings.ToUpper(r.Method),
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Header:   r.Header.Clone(),
		Body:     body,
	}
	recorded.onExhausted = func() {
		cleanCopy := recorded
		cleanCopy.onExhausted = nil
		s.mu.Lock()
		s.unexpected = append(s.unexpected, cleanCopy)
		s.mu.Unlock()
	}

	s.mu.Lock()
	expectation, ok := s.routes[routeKey{method: recorded.Method, path: recorded.Path}]
	if !ok {
		s.unexpected = append(s.unexpected, recorded)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"unexpected httpstub request"}`))
		return
	}
	s.mu.Unlock()

	responder := expectation.record(recorded)
	responder(w, recorded)
}
