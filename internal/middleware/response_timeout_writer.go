package middleware

import (
	"bytes"
	"net/http"
	"sync"
)

// responseBuffer 只暂存内部 Handler 的响应，不持有真实的 HTTP Writer。
// Header 归内部 Handler 独占；外层仅在 Handler 完全结束后读取它。
// 超时或断连只在锁内标记丢弃并释放正文，不与 Handler 争用 Header。
type responseBuffer struct {
	header http.Header

	mu              sync.Mutex
	status          int
	committedHeader http.Header
	body            bytes.Buffer
	discarded       bool
}

func newResponseBuffer() *responseBuffer {
	return &responseBuffer{header: make(http.Header)}
}

func (w *responseBuffer) Header() http.Header {
	return w.header
}

func (w *responseBuffer) WriteHeader(status int) {
	if status < 100 || status > 999 {
		panic("invalid HTTP response status code")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.discarded || w.status != 0 {
		return
	}
	// 普通完整响应不转发临时的 1xx 响应，也不支持 101 协议升级。
	if status < 200 {
		return
	}
	w.status = status
	// 与普通 Writer 一致，首次提交状态时冻结响应头，之后的 Header 修改不再生效。
	w.committedHeader = w.header.Clone()
}

func (w *responseBuffer) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.discarded {
		// 晚到响应是按契约丢弃，并非网络写入失败；不让 response 误记系统故障。
		// 返回成功仅表示处理了这次写入，不表示字节已发送给客户端。
		return len(body), nil
	}
	if w.status == 0 {
		w.status = http.StatusOK
		w.committedHeader = w.header.Clone()
	}
	return w.body.Write(body)
}

func (w *responseBuffer) discard() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.discarded = true
	w.body = bytes.Buffer{}
}

func (w *responseBuffer) isDiscarded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.discarded
}

// result 只能在内部 Handler 结束后调用，不与 Header 的写入并发。
func (w *responseBuffer) result() (http.Header, int, []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	status := w.status
	header := w.committedHeader
	if status == 0 {
		status = http.StatusOK
		header = w.header.Clone()
	}
	return header, status, w.body.Bytes()
}
