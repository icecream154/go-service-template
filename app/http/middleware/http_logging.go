package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"go-service-template/internal/logger"
	"go-service-template/internal/traceid"

	"github.com/gin-gonic/gin"
)

const (
	// unmatchedHTTPRoute 表示 Gin 已完成匹配，但没有找到对应的方法及路由。
	unmatchedHTTPRoute = "<unmatched>"
	// unresolvedHTTPRoute 表示响应结束前未取得路由模板，不能据此断定请求没有匹配路由。
	unresolvedHTTPRoute = "<unresolved>"
)

// httpLogContextKey 关联外层 HTTP 观察器与内层路由匹配，不保存或共享 Gin Context。
type httpLogContextKey struct{}

// httpRequestLog 只属于一次请求，模板首次确认后保持不变。
// Gin 工作协程与外层响应协程通过 mu 交接模板及日志顺序，不并发读取各自的 Writer 或 Gin Context。
type httpRequestLog struct {
	mu            sync.Mutex
	log           *logger.Logger
	baseContext   context.Context
	method        string
	startedAt     time.Time
	routeTemplate string
	logContext    context.Context
}

// HTTPLogging 记录 HTTP 请求开始和对外响应结束，必须位于 TraceID 之后、ResponseTimeout 之前。
// 构造时取得已初始化的共享 Logger；单次请求仅共享受锁元信息，不把响应日志交给内部业务计时。
func HTTPLogging(next http.Handler) http.Handler {
	// 1. 显式检查装配参数，再捕获进程共享 Logger；缺少初始化属于启动顺序错误。
	if next == nil {
		panic("HTTP logging handler must not be nil")
	}
	log := logger.Current()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2. 在进入响应等待层前记下开始时间，后续耗时包含路由、认证及全部响应等待。
		// baseContext 已含 TraceId；它只供外层日志使用，不替换业务协程隔离取消后的 Context。
		state := &httpRequestLog{
			log:         log,
			baseContext: r.Context(),
			method:      r.Method,
			startedAt:   time.Now(),
		}
		// 3. 通过标准 Context 把请求独有的状态传给 Gin；创建请求副本，不改写其他请求或共享实例。
		request := r.WithContext(context.WithValue(r.Context(), httpLogContextKey{}, state))
		output := &httpLogResponseWriter{ResponseWriter: w}
		returnedNormally := false
		// 4. 外层结束时统一写结束记录；即使 next 传播 panic，也先记录已知响应信息并继续原有异常流程。
		// 不在这里 recover 或伪造 HTTP 500，避免改变 net/http 及既有 Recovery 的处理语义。
		defer func() {
			state.finish(output, returnedNormally)
		}()
		// 5. 业务仅调用一次；真实 HTTP 状态由 output 观察，内部晚到的缓冲响应不会覆盖该状态。
		next.ServeHTTP(output, request)
		// 6. 只有正常返回才允许在没有显式响应且未取消时采用 net/http 的默认 200 口径。
		returnedNormally = true
	})
}

// HTTPRouteLogging 在 Gin 完成路由匹配后提供模板，覆盖匹配成功及 NoRoute 路径。
// 独立使用 Gin 而未装配外层 HTTPLogging 时直接透传，不隐式取得全局 Logger。
func HTTPRouteLogging() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// 1. 从本次请求中查找外层观察状态；没有状态说明调用方只使用底层 Gin Router。
		state, _ := ctx.Request.Context().Value(httpLogContextKey{}).(*httpRequestLog)
		if state == nil {
			// 1.1. 独立 Router 不补造日志生命周期，也不新增共享 Logger 初始化依赖。
			ctx.Next()
			return
		}
		// 2. FullPath 是 Gin 选中的注册模板，路径参数仍为占位符；空值在此阶段代表真正的 NoRoute。
		// 外层若已先行冻结 unresolved，则继续使用冻结值，避免同一请求前后标识改变。
		routeTemplate := state.bindRoute(ctx.FullPath())
		// 3. 基于当前业务 Context 建立 HTTP 作用域，不使用 state.baseContext 替换它。
		// ResponseTimeout 的 WithoutCancel 隔离因此保留，TraceId 与本次日志状态也继续沿用。
		operationContext := logger.WithHTTPOperation(ctx.Request.Context(), state.method, routeTemplate)
		ctx.Request = ctx.Request.WithContext(operationContext)
		// 4. 继续原有 Gin 链；本中间件不记录 HTTP 响应结束，后者必须由外层实际 Writer 决定。
		ctx.Next()
	}
}

// bindRoute 首次取得模板时输出开始记录，并返回本次 HTTP 生命周期固定采用的模板。
func (state *httpRequestLog) bindRoute(routeTemplate string) string {
	// 1. 序列化路由确认与外层结束，开始记录总在结束记录之前，且整个生命周期只选择一次模板。
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.routeTemplate == "" {
		// 2. Gin 已执行匹配但 FullPath 为空时，用固定标识表示未匹配，避免记录动态 URL 或资源 ID。
		if routeTemplate == "" {
			routeTemplate = unmatchedHTTPRoute
		}
		// 3. 开始日志只在首次绑定时写出；已有模板或外层已结束时直接复用，不重复记录。
		state.startLocked(routeTemplate)
	}
	// 4. 返回不可再修改的模板值，内层工作即使晚于外层返回也不会改写已记录的 HTTP operation。
	return state.routeTemplate
}

// startLocked 在持有 mu 时固定作用域并记录 started，供路由绑定和外层兜底共同使用。
func (state *httpRequestLog) startLocked(routeTemplate string) {
	// 1. 保存模板和专属于 HTTP 观察器的 Context；业务后续建立自己的操作作用域不会影响它。
	state.routeTemplate = routeTemplate
	state.logContext = logger.WithHTTPOperation(state.baseContext, state.method, routeTemplate)
	// 2. 开始记录不携带响应状态或耗时，表示 HTTP 处理已开始，而不是业务已经成功。
	// 同步输出期间继续持有状态锁，确保并发的外层结束不会先输出 finished。
	if err := state.log.Info(state.logContext, "HTTP request started", logger.Fields{Event: logger.EventStarted}); err != nil {
		// 2.1. 日志故障仅交给独立错误输出，不改变响应状态或停止业务链。
		reportHTTPLogError(state.logContext, err)
	}
}

// finish 在外层响应路径返回时记录选定状态及耗时，不等待超时后仍执行的内部业务。
func (state *httpRequestLog) finish(output *httpLogResponseWriter, returnedNormally bool) {
	// 1. 在外层刚结束时截取耗时，避免把后续等候元信息锁或补写日志的时间计入响应等待。
	durationMS := float64(time.Since(state.startedAt)) / float64(time.Millisecond)
	// 2. 与 Gin 的首次模板绑定串行，确保读取的是已经固定的作用域，而不是可变的 Gin Context。
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.routeTemplate == "" {
		// 2.1. 停机拒绝、提前超时或 Gin 自动重定向可能绕过模板绑定，不能伪称路由未匹配。
		// 此时固定 unresolved，并按开始、结束顺序补齐这一次 HTTP 生命周期。
		state.startLocked(unresolvedHTTPRoute)
	}
	// 3. 只有正常返回且请求未取消时，才将未显式提交响应视为 net/http 默认选择的 200。
	// panic、提前取消等路径只记录确实观察到的状态，缺少最终状态时省略 status_code。
	status := output.status
	if status == 0 && returnedNormally && state.baseContext.Err() == nil {
		status = http.StatusOK
	}
	fields := logger.Fields{Event: logger.EventFinished, DurationMS: &durationMS}
	if status != 0 {
		// 3.1. 非零状态来自实际最终 WriteHeader、隐式 Write 或上述正常默认响应口径。
		fields.StatusCode = &status
	}
	// 4. 仅记录受控的故障阶段，不把 panic 原值、请求正文或底层任意错误文本直接写入日志。
	switch {
	case output.writeError != nil:
		// 4.1. 实际写入失败优先于其他结束原因；只报告阶段，原始任意错误对象不进入公共日志字段。
		fields.Error = "HTTP response write failed"
	case !returnedNormally:
		// 4.2. 外层未正常返回时保留异常终止事实，交给原有异常处理继续决定连接如何结束。
		fields.Error = "HTTP handler terminated before response completion"
	case state.baseContext.Err() != nil:
		// 4.3. 请求 Context 已结束时记录取消事实，不能把无状态返回误写成成功响应。
		fields.Error = "HTTP request context ended"
	}
	// 5. 外层 finished 表示响应路径结束，状态码不证明客户端已完整收到正文，也不证明内部工作结束。
	if err := state.log.Info(state.logContext, "HTTP response finished", fields); err != nil {
		// 5.1. 输出失败仍不能修改已经选定的 HTTP 响应，使用独立错误输出进行兜底。
		reportHTTPLogError(state.logContext, err)
	}
}

// reportHTTPLogError 处理日志器自身的输出故障，避免递归调用同一个故障 Logger。
func reportHTTPLogError(ctx context.Context, err error) {
	// 1. TraceId 用引用格式转义后写入 stderr，只报告日志输出错误，不输出请求参数或正文。
	// stderr 本身失败时不继续重试或启动后台任务，HTTP 业务响应保持原有流程。
	_, _ = fmt.Fprintf(os.Stderr, "Write HTTP log failed: trace_id=%q error=%v\n", traceid.FromContext(ctx), err)
}

// httpLogResponseWriter 只由外层响应协程访问，记录最终状态和实际写入故障。
// 它不供超时后继续工作的 Gin 协程直接使用，也不修改或缓存业务响应正文。
type httpLogResponseWriter struct {
	http.ResponseWriter
	status     int
	writeError error
}

// WriteHeader 记录第一个最终状态，保留 1xx 临时响应的标准行为。
func (w *httpLogResponseWriter) WriteHeader(status int) {
	// 1. 100–199 中除协议切换 101 外的状态只是临时响应，直接转交，不占用最终状态位置。
	if status >= 100 && status <= 199 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	// 2. 最终状态已选定时忽略重复提交，保持第一次响应的记录，不被后续 WriteHeader 覆盖。
	if w.status != 0 {
		return
	}
	// 3. 先交给实际 Writer 处理，再记录其接受的状态；无效状态触发 panic 时不伪造已提交结果。
	w.ResponseWriter.WriteHeader(status)
	w.status = status
}

// Write 观察正文写入，保留隐式 200、短写和错误返回语义。
func (w *httpLogResponseWriter) Write(body []byte) (int, error) {
	// 1. 原样转交正文，让底层 Writer 自行处理隐式状态和 Content-Type 等头部。
	// 不为采集状态提前调用 WriteHeader，避免改变底层对正文的探测或延迟提交行为。
	written, err := w.ResponseWriter.Write(body)
	// 2. 底层 Write 已正常返回后，才记录该正文写入隐式选择的 200；已有最终状态继续保留。
	// 若底层直接 panic，本分支不会执行，外层不会凭空推断已经提交了 200。
	if w.status == 0 {
		w.status = http.StatusOK
	}
	// 2.1. 底层未写满却未返回错误时归一为 io.ErrShortWrite，不重试或追加正文。
	if err == nil && written != len(body) {
		err = io.ErrShortWrite
	}
	// 3. 保留第一个写入故障供外层结束日志使用，同时原样向现有响应组件返回写入数量与错误。
	if err != nil && w.writeError == nil {
		w.writeError = err
	}
	return written, err
}

// Unwrap 允许标准 ResponseController 继续找到底层 Writer 的能力，不自行模拟 Flush 或 Hijack。
func (w *httpLogResponseWriter) Unwrap() http.ResponseWriter {
	// 1. 返回真实被包装的 Writer；具体可用能力仍由底层实现决定。
	return w.ResponseWriter
}
