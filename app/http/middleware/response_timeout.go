// Package middleware 提供由 HTTP 装配层显式启用的中间件。
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

// ResponseTimeout 使用标准库管理计时、协程和响应缓冲，只保留项目的响应及停机约定。
// 通过 NewResponseTimeout 创建；仅包装完整的普通 JSON Handler，不支持流式响应。
// 构造不会启用超时，只有显式使用 Wrap 返回的 Handler 才生效。
type ResponseTimeout struct {
	timeout      time.Duration
	timeoutBody  string
	shutdownBody string

	mu       sync.Mutex
	active   int
	stopping bool
	drained  chan struct{}
}

// NewResponseTimeout 配置正的响应等待时间，不设置业务执行时限。
func NewResponseTimeout(timeout time.Duration) (*ResponseTimeout, error) {
	if timeout <= 0 {
		return nil, errors.New("response timeout must be positive")
	}
	encode := func(message string) ([]byte, error) {
		failure := response.ServiceUnavailable(message)
		return json.Marshal(response.Response[any]{Error: &failure})
	}
	timeoutBody, err := encode("response wait timeout; operation outcome is unknown")
	if err != nil {
		return nil, err
	}
	shutdownBody, err := encode("server is shutting down")
	if err != nil {
		return nil, err
	}
	return &ResponseTimeout{
		timeout: timeout, timeoutBody: string(timeoutBody), shutdownBody: string(shutdownBody),
		drained: make(chan struct{}),
	}, nil
}

// Wrap 包装整个 Gin Engine，不通过 router.Use 注册；next 应包含既有的异常恢复中间件。
// 标准库超时后返回 503，内部处理继续，后续响应被拒绝；停机时须另行调用 Shutdown。
func (m *ResponseTimeout) Wrap(next http.Handler) http.Handler {
	if next == nil {
		panic("response timeout handler must not be nil")
	}
	work := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 外层登记，内层结束才减计数：返回超时响应不等于业务已经结束。
		defer m.finish()
		defer func() {
			if value := recover(); value != nil {
				// Gin Recovery 之外的异常也记录；尤其不能漏掉超时之后才发生的 panic。
				log.Printf("HTTP handler panicked: method=%s path=%s panic=%v stack=%s", r.Method, r.URL.Path, value, debug.Stack())
				panic(value)
			}
		}()
		// 标准库的等待 Context 会超时；交给业务前移除取消和截止时间，保留其值。
		// Clone 不复制 Body，参数尚未读取完整时仍可能因连接关闭而绑定失败。
		request := r.Clone(context.WithoutCancel(r.Context()))
		next.ServeHTTP(lateResponseWriter{w}, request)
	})
	timed := http.TimeoutHandler(work, m.timeout, m.timeoutBody)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		output := &responseWriter{ResponseWriter: w, request: r}
		output.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !m.start() {
			output.WriteHeader(http.StatusServiceUnavailable)
			_, _ = output.Write([]byte(m.shutdownBody)) // 写入失败由 output 记录。
			return
		}
		startedAt := time.Now()
		timed.ServeHTTP(output, r)
		// 这里只记录选中的 HTTP 结果；业务本身也可能返回 503，不能一律称为超时。
		log.Printf("HTTP response wait ended: method=%s path=%s status=%d duration=%s", r.Method, r.URL.Path, output.status, time.Since(startedAt))
	})
}

// Shutdown 停止接纳新工作，等待包括已返回超时响应在内的内部 Handler 结束。
// ctx 到期只结束本次等待，不取消业务；装配层须等 HTTP 和内部工作都结束后再关数据库。
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

// lateResponseWriter 不缓存数据，只把标准库拒绝的晚到输出视为正常丢弃。
// 避免 response 把预期的超时/断连记成系统写入失败；返回成功不代表客户端收到正文。
type lateResponseWriter struct{ http.ResponseWriter }

func (w lateResponseWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	if errors.Is(err, http.ErrHandlerTimeout) || errors.Is(err, context.Canceled) {
		return len(body), nil
	}
	return n, err
}

// responseWriter 仅观察真实输出，不实现计时或缓冲，也不供内部 Handler 使用。
type responseWriter struct {
	http.ResponseWriter
	request *http.Request
	status  int
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.Header().Del("Content-Length")
	if status == http.StatusServiceUnavailable && w.request.ProtoMajor == 1 && w.request.ContentLength != 0 {
		// HTTP/1 提前响应时不再等待消费剩余请求体，避免慢请求体阻塞 503。
		w.Header().Set("Connection", "close")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		log.Printf("Write HTTP response failed: method=%s path=%s status=%d written=%d expected=%d error=%v", w.request.Method, w.request.URL.Path, w.status, n, len(body), err)
	}
	return n, err
}
