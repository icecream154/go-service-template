// Package apicall 提供面向 HTTP JSON 契约的通用测试调用器与脱敏响应断言辅助，不依赖具体业务 DTO。
package apicall

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	defaultRequestTimeout = 15 * time.Second
	maxResponseBodyBytes  = 1 << 20 // 1 MiB
)

// Response 保存一次测试 HTTP 请求的完整状态码、响应头及有界原始正文。
type Response struct {
	StatusCode int
	Header     http.Header
	RawBody    []byte
}

// CallRecord 保存单次 HTTP 请求的非敏感执行轨迹摘要（严禁记录请求头凭证或响应正文）。
type CallRecord struct {
	Method     string
	Path       string
	StatusCode int
	TraceID    string
	Duration   time.Duration
}

type requestBuilder struct {
	query   url.Values
	headers http.Header
	body    []byte
	hasBody bool
}

// Option 配置单次测试 HTTP 请求的查询参数、请求头或请求体。
type Option func(t testing.TB, rb *requestBuilder)

// WithQuery 追加单个 URL Query 键值对（多次调用同名键会按序保留多个值）。
func WithQuery(key, value string) Option {
	return func(_ testing.TB, rb *requestBuilder) {
		rb.query.Add(key, value)
	}
}

// WithBearerToken 设置标准 Authorization: Bearer <token> 请求头。
func WithBearerToken(token string) Option {
	return func(_ testing.TB, rb *requestBuilder) {
		rb.headers.Set("Authorization", "Bearer "+token)
	}
}

// WithHeader 设置指定名称与值的请求头（若同一键需发多值可配合 AddHeader）。
func WithHeader(key, value string) Option {
	return func(_ testing.TB, rb *requestBuilder) {
		rb.headers.Set(key, value)
	}
}

// WithAdditionalHeader 在同一请求头名称下追加额外值（用于验证多值请求头被中间件拒绝的场景）。
func WithAdditionalHeader(key, value string) Option {
	return func(_ testing.TB, rb *requestBuilder) {
		rb.headers.Add(key, value)
	}
}

// WithHeaderIfNonEmpty 仅当 value 非空时设置请求头，便于表驱动用例统一表达匿名与认证分支。
func WithHeaderIfNonEmpty(key, value string) Option {
	return func(_ testing.TB, rb *requestBuilder) {
		if value != "" {
			rb.headers.Set(key, value)
		}
	}
}

// WithRawJSON 直接使用原始 JSON 文本作为请求体并声明 Content-Type: application/json。
func WithRawJSON(raw string) Option {
	return func(_ testing.TB, rb *requestBuilder) {
		rb.body = []byte(raw)
		rb.hasBody = true
		if rb.headers.Get("Content-Type") == "" {
			rb.headers.Set("Content-Type", "application/json")
		}
	}
}

// WithJSONBody 将 payload 编码为 JSON 请求体并声明 Content-Type: application/json。
func WithJSONBody(payload any) Option {
	return func(t testing.TB, rb *requestBuilder) {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode JSON request body: %v", err)
		}
		rb.body = data
		rb.hasBody = true
		if rb.headers.Get("Content-Type") == "" {
			rb.headers.Set("Content-Type", "application/json")
		}
	}
}

// Client 绑定单个被测服务基础地址，禁用跨请求连接复用与自动重定向，保证每次测试调用边界清晰。
type Client struct {
	baseURL    string
	httpClient *http.Client
	observer   func(CallRecord)
}

// WithObserver 返回共享底层连接配置但附加调用轨迹观察器的浅拷贝客户端，供步骤执行器记录脱敏请求摘要。
func (c *Client) WithObserver(observer func(CallRecord)) *Client {
	return &Client{
		baseURL:    c.baseURL,
		httpClient: c.httpClient,
		observer:   observer,
	}
}

// NewClient 创建指向 baseURL 的测试 HTTP 客户端；timeout <= 0 时采用 15 秒默认单次超时。
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Get 发送 HTTP GET 请求并返回响应快照。
func (c *Client) Get(t testing.TB, path string, opts ...Option) *Response {
	t.Helper()
	return c.Do(t, http.MethodGet, path, opts...)
}

// PostJSON 发送 HTTP POST 请求并返回响应快照。
func (c *Client) PostJSON(t testing.TB, path string, opts ...Option) *Response {
	t.Helper()
	return c.Do(t, http.MethodPost, path, opts...)
}

// Do 按指定的 HTTP 方法、路径及选项构造并发送一次真实请求。
func (c *Client) Do(t testing.TB, method, path string, opts ...Option) *Response {
	t.Helper()
	rb := &requestBuilder{
		query:   make(url.Values),
		headers: make(http.Header),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t, rb)
		}
	}

	targetURL := c.baseURL + path
	if encodedQuery := rb.query.Encode(); encodedQuery != "" {
		if strings.Contains(targetURL, "?") {
			targetURL += "&" + encodedQuery
		} else {
			targetURL += "?" + encodedQuery
		}
	}

	var bodyReader io.Reader
	if rb.hasBody {
		bodyReader = bytes.NewReader(rb.body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, targetURL, bodyReader)
	if err != nil {
		t.Fatalf("build HTTP request %s %s: %v", method, path, err)
	}
	req.Header = rb.headers

	startedAt := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("execute HTTP request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		t.Fatalf("read HTTP response body for %s %s: %v", method, path, err)
	}
	if len(rawBody) > maxResponseBodyBytes {
		t.Fatalf("HTTP response body for %s %s exceeds %d bytes", method, path, maxResponseBodyBytes)
	}

	clonedHeader := resp.Header.Clone()
	if c.observer != nil {
		c.observer(CallRecord{
			Method:     strings.ToUpper(method),
			Path:       path,
			StatusCode: resp.StatusCode,
			TraceID:    clonedHeader.Get("TraceId"),
			Duration:   elapsed,
		})
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Header:     clonedHeader,
		RawBody:    rawBody,
	}
}
