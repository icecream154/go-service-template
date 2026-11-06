// Package middleware 提供由 HTTP 装配层显式启用的公共中间件。
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"go-service-template/internal/response"
)

// ResponseTimeout 限制等待 HTTP 响应的时间，不取消已经开始的内部处理。
// 它包装完整的 http.Handler（例如 Gin Engine），不能通过 router.Use 注册。
// 每个请求在独立处理协程内完整运行；只有外层协程可以写真实的 HTTP 响应。
// 仅适用于完整输出的普通响应，不支持流式输出、Flush、Hijack 或协议升级。
// 构造实例不会启用超时，只有显式使用 Wrap 返回的 Handler 才会生效。
type ResponseTimeout struct {
	timeout time.Duration

	timeoutBody  []byte
	internalBody []byte
	shutdownBody []byte

	// 登记工作与开始关闭共用锁，避免关闭已确认没有工作后又接纳新请求。
	mu       sync.Mutex
	active   int
	stopping bool
	drained  chan struct{}
}

// NewResponseTimeout 配置响应等待时限；timeout 必须为正数，不隐式采用默认值。
// 时限从包装器接收请求时开始计算，包含内部绑定、校验、业务和响应编码。
// 超时返回 HTTP 503 / SERVICE_UNAVAILABLE，表示未及时取得结果，不表示业务失败。
func NewResponseTimeout(timeout time.Duration) (*ResponseTimeout, error) {
	if timeout <= 0 {
		return nil, errors.New("response timeout must be positive")
	}
	encode := func(failure response.ErrorInfo) ([]byte, error) {
		return json.Marshal(response.Response[any]{Error: &failure})
	}
	timeoutBody, err := encode(response.ServiceUnavailable("response wait timeout; operation outcome is unknown"))
	if err != nil {
		return nil, err
	}
	internalBody, err := encode(response.InternalError())
	if err != nil {
		return nil, err
	}
	shutdownBody, err := encode(response.ServiceUnavailable("server is shutting down"))
	if err != nil {
		return nil, err
	}
	return &ResponseTimeout{
		timeout: timeout, timeoutBody: timeoutBody, internalBody: internalBody,
		shutdownBody: shutdownBody, drained: make(chan struct{}),
	}, nil
}

// Wrap 包装完整的 HTTP 处理器，共享本实例的时限与内部工作计数。
// next 必须非 nil，且必须在 ServeHTTP 返回前完成所有对请求和响应的访问。
// 不在 Gin 中间件中启动 go ctx.Next()，避免 Gin Context 提前归池或并发使用。
func (m *ResponseTimeout) Wrap(next http.Handler) http.Handler {
	if next == nil {
		panic("response timeout handler must not be nil")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(m.timeout)
		if r.Context().Err() != nil {
			return
		}
		if !m.start() {
			m.writeFailure(w, r, http.StatusServiceUnavailable, m.shutdownBody)
			return
		}

		// 内部保留请求 Context 的值，但不继承断连、父截止时间或响应等待超时。
		// Clone 隔离可变的请求头等数据；Body 仍依赖连接，因此读取参数尚未完成时，
		// 超时或断连可能造成读取失败，不能承诺此时业务一定已经开始。
		request := r.Clone(context.WithoutCancel(r.Context()))
		writer := newResponseBuffer()
		done := make(chan struct{})
		var finishedAt time.Time
		var panicked bool
		go func() {
			returned := false
			// 无论正常结束还是 panic，都释放工作计数；晚到 panic 也必须被捕获并记录。
			defer m.finish()
			defer func() {
				if value := recover(); value != nil {
					panicked = true
					log.Printf("HTTP handler panicked: method=%s path=%s panic=%v stack=%s", request.Method, request.URL.Path, value, debug.Stack())
				} else if !returned {
					// runtime.Goexit 也会执行 defer，但没有 panic 值；不能把它当作成功返回。
					panicked = true
					log.Printf("HTTP handler exited without returning: method=%s path=%s", request.Method, request.URL.Path)
				}
				finishedAt = time.Now()
				if !finishedAt.Before(deadline) || writer.isDiscarded() {
					log.Printf("HTTP handler finished after response wait ended: method=%s path=%s panicked=%t", request.Method, request.URL.Path, panicked)
				}
				close(done)
			}()
			next.ServeHTTP(writer, request)
			returned = true
		}()

		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-done:
			// Handler 返回后才读取其私有 Header；超时路径从不访问这张可变 map。
			// 完成与定时器同时就绪时，以实际完成时间判断是否超过期限。
			if !finishedAt.Before(deadline) {
				m.timeoutResponse(w, r, writer)
				return
			}
			if r.Context().Err() != nil {
				writer.discard()
				return
			}
			if panicked {
				writer.discard()
				m.writeFailure(w, r, http.StatusInternalServerError, m.internalBody)
				return
			}
			header, status, body := writer.result()
			writeTimeoutResponse(w, r, header, status, body)
		case <-timer.C:
			// 如果内部早已完成、只是外层稍后才获得调度，保留期限内完成的结果。
			select {
			case <-done:
				if finishedAt.Before(deadline) && r.Context().Err() == nil {
					if panicked {
						writer.discard()
						m.writeFailure(w, r, http.StatusInternalServerError, m.internalBody)
					} else {
						header, status, body := writer.result()
						writeTimeoutResponse(w, r, header, status, body)
					}
					return
				}
			default:
			}
			m.timeoutResponse(w, r, writer)
		case <-r.Context().Done():
			// 客户端已经离开，只关闭输出通道；内部处理仍按自身规则结束。
			writer.discard()
			log.Printf("HTTP response wait canceled: method=%s path=%s error=%v", r.Method, r.URL.Path, r.Context().Err())
		}
	})
}

// Shutdown 停止接纳新工作，并等待包括已返回超时响应在内的所有内部 Handler 结束。
// 可重复调用；等待 Context 到期只结束本次等待，不取消工作，也不关闭数据库。
// 装配层须同时停止 HTTP 接入，等 HTTP 与内部工作都结束后才能关闭连接池。
// 返回 ctx.Err() 时仍可能有工作运行，不能据此宣称收尾完成或事务已经回滚。
func (m *ResponseTimeout) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if !m.stopping {
		m.stopping = true
		if m.active == 0 {
			close(m.drained)
		}
	}
	m.mu.Unlock()
	select {
	case <-m.drained:
		return nil
	default:
	}
	select {
	case <-m.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *ResponseTimeout) start() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return false
	}
	m.active++
	return true
}

func (m *ResponseTimeout) finish() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active--
	if m.stopping && m.active == 0 {
		close(m.drained)
	}
}

func (m *ResponseTimeout) timeoutResponse(w http.ResponseWriter, r *http.Request, writer *responseBuffer) {
	writer.discard()
	log.Printf("HTTP response wait timed out: method=%s path=%s status=%d timeout=%s", r.Method, r.URL.Path, http.StatusServiceUnavailable, m.timeout)
	if r.Context().Err() == nil {
		m.writeFailure(w, r, http.StatusServiceUnavailable, m.timeoutBody)
	}
}

func (m *ResponseTimeout) writeFailure(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json; charset=utf-8")
	if r.ProtoMajor == 1 && r.ContentLength != 0 {
		// 提前响应时，请求体可能仍在读取。HTTP/1 默认会先消费剩余请求体再发送响应，
		// 这可能让超时响应反而阻塞；关闭连接复用以跳过该读取，不取消已开始的业务。
		header.Set("Connection", "close")
	}
	writeTimeoutResponse(w, r, header, status, body)
}

// writeTimeoutResponse 仅由外层调用一次，内部 Handler 永远接触不到真实 Writer。
// response.Success/Fail 在包装器内写入的是缓冲；真正的网络写入故障在这里记录。
func writeTimeoutResponse(w http.ResponseWriter, r *http.Request, header http.Header, status int, body []byte) {
	for key, values := range header {
		w.Header()[key] = append([]string(nil), values...)
	}
	// 最终正文由包装器选择，不沿用内部预设的长度。
	w.Header().Del("Content-Length")
	w.WriteHeader(status)
	if len(body) == 0 {
		return
	}
	written, err := w.Write(body)
	if err == nil && written != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		log.Printf("Write HTTP response failed: method=%s path=%s status=%d written=%d expected=%d error=%v", r.Method, r.URL.Path, status, written, len(body), err)
	}
}
