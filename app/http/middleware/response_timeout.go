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
	// 1. 只接受正的响应等待时间；该时间控制 HTTP 等待，不作为内部业务的执行截止时间。
	if timeout <= 0 {
		return nil, errors.New("response timeout must be positive")
	}
	// 2. 准备统一的不可用响应编码方法，构造阶段预先生成固定正文，避免每次超时重复编码。
	encode := func(message string) ([]byte, error) {
		// 2.1. 使用项目已有的错误结构，保留 data/error 响应契约，不输出内部实现或异常正文。
		failure := response.ServiceUnavailable(message)
		// 2.2. 返回完整 JSON 字节及编码错误，由外层构造流程决定是否继续创建中间件。
		return json.Marshal(response.Response[any]{Error: &failure})
	}
	// 3. 分别编码等待超时和停机拒绝的正文，二者都返回 503，但表达的处理阶段不同。
	timeoutBody, err := encode("response wait timeout; operation outcome is unknown")
	if err != nil {
		// 3.1. 固定超时正文编码失败时不交付部分可用的中间件。
		return nil, err
	}
	shutdownBody, err := encode("server is shutting down")
	if err != nil {
		// 3.2. 停机正文同样必须准备成功，避免运行后才发现拒绝路径无法输出。
		return nil, err
	}
	// 4. 保存配置及固定正文，创建排空通知通道；此时没有请求、计时器或后台工作开始执行。
	return &ResponseTimeout{
		timeout: timeout, timeoutBody: string(timeoutBody), shutdownBody: string(shutdownBody),
		drained: make(chan struct{}),
	}, nil
}

// Wrap 包装整个 Gin Engine，不通过 router.Use 注册；next 应包含既有的异常恢复中间件。
// 标准库超时后返回 503，内部处理继续，后续响应被拒绝；停机时须另行调用 Shutdown。
func (m *ResponseTimeout) Wrap(next http.Handler) http.Handler {
	// 1. 包装目标必须存在，空 Handler 属于装配错误，不能留到请求进入时才触发空指针调用。
	if next == nil {
		panic("response timeout handler must not be nil")
	}
	// 2. 定义真正的内部工作入口；它由标准库超时 Handler 在独立执行流程中调用。
	work := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2.1. 外层接纳请求时已增加在途计数，只有内部 Handler 真正返回或异常退出才减少。
		// 对外返回超时响应不等于业务结束，不能在外层响应完成时提前解除这次登记。
		defer m.finish()
		// 2.2. 保留 Gin Recovery 之外的异常记录，并重新抛出给标准库处理，避免吞掉 panic。
		defer func() {
			// 2.2.1. 只处理本 goroutine 捕获到的异常；正常退出时不生成额外异常日志。
			if value := recover(); value != nil {
				// 2.2.2. 沿用既有异常诊断输出，尤其保留超时后才发生的 panic 及当前堆栈。
				// 本阶段外部请求/响应摘要交给 HTTPLogging，异常日志迁移仍与其生命周期区分。
				log.Printf("HTTP handler panicked: method=%s path=%s panic=%v stack=%s", r.Method, r.URL.Path, value, debug.Stack())
				// 2.2.3. 重新抛出原值，保留现有标准库超时包装器及服务器的异常传播语义。
				panic(value)
			}
		}()
		// 2.3. 标准库的等待 Context 会超时；交给业务前移除取消和截止时间，继续保留 TraceId 等值。
		// Clone 不复制 Body，参数尚未读取完整时仍可能因连接关闭而绑定失败，不能保证请求体独立存活。
		request := r.Clone(context.WithoutCancel(r.Context()))
		// 2.4. 完整执行下层 Handler；晚到 Writer 只适配预期的超时/取消写入结果，不缓存或补发响应。
		next.ServeHTTP(lateResponseWriter{w}, request)
	})
	// 3. 使用标准库管理响应等待计时、内部工作和响应缓冲，不重复实现超时选择逻辑。
	timed := http.TimeoutHandler(work, m.timeout, m.timeoutBody)
	// 4. 返回接纳请求的外层入口。真实 HTTP 请求进入此入口时才开始登记和等待。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 4.1. 进入前已取消的请求不再启动业务；外层 HTTPLogging 负责记录实际响应路径。
		if r.Context().Err() != nil {
			return
		}
		// 4.2. 仅在外部响应路径适配 Header 和短写行为；内部 Handler 写的是标准库的响应缓冲。
		output := &responseWriter{ResponseWriter: w, request: r}
		output.Header().Set("Content-Type", "application/json; charset=utf-8")
		// 4.3. 接纳检查与在途登记在同一把锁内完成；停机后直接拒绝，不产生新的内部工作。
		if !m.start() {
			// 4.3.1. 选定统一停机响应，写入结果由最外层 HTTPLogging 观察，不在此重复输出访问摘要。
			output.WriteHeader(http.StatusServiceUnavailable)
			_, _ = output.Write([]byte(m.shutdownBody))
			return
		}
		// 4.4. 只调用一个标准库等待入口；期限内完成则使用业务响应，期限到达则选定超时响应。
		// HTTPLogging 在本入口之外记录对外状态和耗时，避免将内部结束误当作响应结束或重复记账。
		timed.ServeHTTP(output, r)
	})
}

// Shutdown 停止接纳新工作，等待包括已返回超时响应在内的内部 Handler 结束。
// ctx 到期只结束本次等待，不取消业务；装配层须等 HTTP 和内部工作都结束后再关数据库。
func (m *ResponseTimeout) Shutdown(ctx context.Context) error {
	// 1. 在状态锁内把组件切换为停止接纳，确保此后 start 不会继续增加在途工作。
	m.mu.Lock()
	if !m.stopping {
		// 1.1. 只由首次停机调用完成状态切换，后续调用复用同一个排空通道，避免重复关闭。
		m.stopping = true
		if m.active == 0 {
			// 1.2. 当前没有内部工作时立即通知排空；否则由最后一个 finish 发出通知。
			close(m.drained)
		}
	}
	// 2. 等待期间不持有状态锁，使执行中的请求仍能进入 finish 完成计数和唤醒。
	m.mu.Unlock()
	// 3. 先检查是否已经排空，使已完成的停机即使收到过期 Context 也能直接报告成功。
	select {
	case <-m.drained:
		// 3.1. 所有已登记内部工作均已结束，无需继续等待取消信号。
		return nil
	default:
		// 3.2. 尚未排空时进入下一个带取消的等待，不在这里忙轮询。
	}
	// 4. 排空与停机预算共用调用方提供的 Context；预算结束只结束等待，不取消内部业务。
	select {
	case <-m.drained:
		// 4.1. 最后一个内部工作结束，允许资源持有方继续后续共享资源清理。
		return nil
	case <-ctx.Done():
		// 4.2. 返回真实取消或超时原因，不能据此宣布内部工作、事务或远端调用已完成。
		return ctx.Err()
	}
}

// start 原子地判断是否接纳请求，并为即将执行的内部工作增加计数。
func (m *ResponseTimeout) start() bool {
	// 1. 接纳判断和计数递增共用状态锁，避免 Shutdown 已关闭 drained 后又出现新工作。
	m.mu.Lock()
	defer m.mu.Unlock()
	// 2. 停机后拒绝新工作，不修改 active；调用方随后直接选择停机响应。
	if m.stopping {
		return false
	}
	// 3. 成功登记一个内部 Handler；对应 work 的 defer 将负责且仅负责一次 finish。
	m.active++
	return true
}

// finish 在内部 Handler 结束时解除登记，并在停机阶段通知最后一次排空。
func (m *ResponseTimeout) finish() {
	// 1. 持锁更新计数，使多个并发完成的内部 Handler 不会遗漏最后一次排空通知。
	m.mu.Lock()
	defer m.mu.Unlock()
	// 2. 每个已接纳的内部工作只在其 defer 中减计数，与外层是否已返回响应无关。
	m.active--
	// 3. 仅当已进入停机且最后一个工作结束时关闭通道，唤醒所有 Shutdown 等待者。
	if m.stopping && m.active == 0 {
		close(m.drained)
	}
}

// lateResponseWriter 不缓存数据，只把标准库拒绝的晚到输出视为正常丢弃。
// 避免 response 把预期的超时/断连记成系统写入失败；返回成功不代表客户端收到正文。
type lateResponseWriter struct{ http.ResponseWriter }

func (w lateResponseWriter) Write(body []byte) (int, error) {
	// 1. 把正文交给标准库的缓冲 Writer，保留其实际写入数和原始错误用于分类。
	n, err := w.ResponseWriter.Write(body)
	// 2. 超时或取消后的晚到正文不再发送；将预期拒绝转换为已消费，避免业务响应组件重复报写入故障。
	// 该返回值只表示内部正文被正常丢弃，不能证明客户端收到响应或业务已回滚。
	if errors.Is(err, http.ErrHandlerTimeout) || errors.Is(err, context.Canceled) {
		return len(body), nil
	}
	// 3. 对正常写入及其他真实错误保持原有返回，不吞掉未知故障或重试发送。
	return n, err
}

// responseWriter 仅观察真实输出，不实现计时或缓冲，也不供内部 Handler 使用。
type responseWriter struct {
	http.ResponseWriter
	request *http.Request
	status  int
}

func (w *responseWriter) WriteHeader(status int) {
	// 1. 保存本次选定的状态，并清除旧长度，由后续实际正文决定 Content-Length。
	w.status = status
	w.Header().Del("Content-Length")
	// 2. HTTP/1 在请求体尚未完全消费时提前返回 503，需要明确关闭连接，避免等待剩余请求体阻塞响应。
	if status == http.StatusServiceUnavailable && w.request.ProtoMajor == 1 && w.request.ContentLength != 0 {
		w.Header().Set("Connection", "close")
	}
	// 3. 原样提交状态至外层 Writer，由 HTTPLogging 观察实际对外结果，本组件不再重复写访问日志。
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	// 1. 把选中的完整或超时正文交给外层 Writer；这条路径负责对外输出，内部 Handler 不直接使用它。
	n, err := w.ResponseWriter.Write(body)
	// 2. 补齐底层短写但未报告错误的情况，保留错误供外层 HTTPLogging 和调用方观察。
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	// 3. 返回真实字节数与最终错误；外层统一记录响应结束，不在此重复打印同一写入故障。
	return n, err
}
