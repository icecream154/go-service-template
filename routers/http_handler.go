package routers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go-service-template/app/http/middleware"
	authnjwt "go-service-template/internal/authn/jwt"
)

// defaultResponseTimeout 是未单独配置接口的响应等待时间，不限制内部业务执行时间。
const defaultResponseTimeout = 3 * time.Second

// HTTPHandler 为完整路由选择响应等待时间，并统一管理超时后仍在运行的请求。
// 通过 NewHTTPHandler 创建；停机时除关闭 HTTP Server 外，还必须调用 Shutdown。
type HTTPHandler struct {
	// entryHandler 在响应等待及路由分发之前，为每个请求建立 TraceId 上下文。
	entryHandler http.Handler
	// defaultHandler 是默认 3 秒中间件包装后的完整 Gin 入口。
	// 请求没有命中方法和路径的覆盖配置时，进入此 Handler。
	defaultHandler http.Handler
	// routeHandlers 保存接口覆盖配置对应的入口，键为“HTTP 方法 + 空格 + 完整路径”。
	// 当前包含分页查询的 5 秒、创建用户的 10 秒和头像生成的 65 秒入口；查询参数不属于键。
	// 每个入口包装同一个 Gin Engine，与 defaultHandler 并列，请求只选择其中一个。
	routeHandlers map[string]http.Handler
	// timeouts 保存默认及全部覆盖项的中间件实例，供 Shutdown 逐个等待内部处理结束。
	// Handler 用于接收请求，实例用于管理执行计数和停机；因此两者都需要保留。
	// 这个切片不是请求执行链，单个请求不会依次经过这里的所有中间件。
	timeouts []*middleware.ResponseTimeout
}

// NewHTTPHandler 创建生产 HTTP 入口，在响应等待层之前统一装配 TraceId 中间件。
// 所有接口默认等待 3 秒，指定接口使用独立配置。
// 每个超时 Handler 包装同一个完整 Gin Engine；请求只进入其中一个，不叠加计时。
// 本方法在启动时调用一次，预先准备各个入口；构造和 Wrap 不会开始某次请求的计时。
// 每次请求进入选定入口后，才由中间件中的 http.TimeoutHandler 启动该请求的等待。
// 多个请求可以复用同一个中间件实例，但各自独立计时，不共享一个倒计时。
func NewHTTPHandler() (*HTTPHandler, error) {
	// 覆盖配置按 HTTP 方法和完整路径匹配；新增特殊配置时统一在这里声明。
	// 未列出的请求使用 defaultResponseTimeout；修改配置后重新启动服务生效。
	overrides := []struct {
		method  string
		path    string
		timeout time.Duration
	}{
		{http.MethodGet, "/api/v1/user/userProfile/queryUserProfilePage", 5 * time.Second},
		{http.MethodPost, "/api/v1/user/userProfile/createUser", 10 * time.Second},
		// 图片生成 Client 当前总超时为 60 秒，另留 5 秒用于认证、校验与响应处理。
		{http.MethodPost, "/api/v1/user/userProfile/generateUserAvatar", 65 * time.Second},
	}

	// 启动阶段已初始化共享 JWT 组件；完整 Gin Engine 在 /api 分组挂一次可选认证。
	// 各等待包装器复用这个 Engine，不会重复安装或重复执行认证。
	engine := New(authnjwt.Current())
	// 保存已注册的“方法 + 路径”，用于检查下面的覆盖配置是否指向真实接口。
	registeredRoutes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		registeredRoutes[route.Method+" "+route.Path] = struct{}{}
	}

	// 创建默认中间件，Wrap(engine) 返回带等待控制的 HTTP 入口，随后保存其收尾实例。
	defaultTimeout, err := middleware.NewResponseTimeout(defaultResponseTimeout)
	if err != nil {
		return nil, fmt.Errorf("configure default response timeout: %w", err)
	}
	handler := &HTTPHandler{
		defaultHandler: defaultTimeout.Wrap(engine),
		routeHandlers:  make(map[string]http.Handler, len(overrides)),
		timeouts:       []*middleware.ResponseTimeout{defaultTimeout},
	}
	for _, override := range overrides {
		key := override.method + " " + override.path
		// 路由变更后不能让旧覆盖配置静默失效；启动阶段直接报告未注册的路由。
		if _, exists := registeredRoutes[key]; !exists {
			return nil, fmt.Errorf("response timeout route is not registered: %s", key)
		}
		timeout, err := middleware.NewResponseTimeout(override.timeout)
		if err != nil {
			return nil, fmt.Errorf("configure response timeout for %s: %w", key, err)
		}
		// 覆盖入口直接包装原始 Engine，不包装 defaultHandler。
		// 例如创建接口只受 10 秒等待控制，不会再被默认 3 秒提前结束等待。
		handler.routeHandlers[key] = timeout.Wrap(engine)
		// 每个覆盖项也可能留下已返回超时响应、但仍在执行的业务，必须纳入停机等待。
		handler.timeouts = append(handler.timeouts, timeout)
	}
	// TraceId 在响应等待之前确定，超时、停机拒绝及正常业务均使用同一个请求标识。
	handler.entryHandler = middleware.TraceID(http.HandlerFunc(handler.serveHTTP))
	return handler, nil
}

// ServeHTTP 先建立 TraceId 上下文，再按方法和路径选择响应等待入口并进入 Gin。
// cmd/web 将本对象设置为 http.Server.Handler，Shutdown 继续管理同一对象的内部工作。
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.entryHandler.ServeHTTP(w, r)
}

// serveHTTP 只负责路由分发，查询参数不参与匹配；未配置的路径使用默认等待时间。
func (h *HTTPHandler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// 例如 GET .../queryUserProfilePage?page_number=2 的键不包含 ? 后面的内容，命中 5 秒入口；
	// POST .../createUser 命中 10 秒入口；GET .../queryUserProfileByUserId 未命中，采用默认 3 秒。
	// 方法也是匹配条件：GET .../createUser 不会使用 POST 的覆盖项，最终由 Gin 判断路由。
	handler, exists := h.routeHandlers[r.Method+" "+r.URL.Path]
	if !exists {
		handler = h.defaultHandler
	}
	// 只调用选中的一个入口。中间件先登记内部执行，再开始等待并调用完整 Gin 处理链。
	// 期限内完成就发送业务响应，不必等满配置时间；期限到达仍未完成则选择统一的 503 响应。
	// 中间件隔离内部请求的 Context 取消信号，业务继续执行，晚到响应被丢弃。
	// 返回超时 503 不减少内部执行计数，只有内部处理真正结束后，中间件才解除本次登记。
	handler.ServeHTTP(w, r)
}

// Shutdown 等待所有超时 Handler 的内部工作结束，包括已经向客户端返回超时的请求。
//
// cmd/web 先调用 http.Server.Shutdown 停止接收并等待 HTTP 请求，再调用本方法等待内部处理。
// HTTP 外层可能已返回 503，而内部业务仍在执行；仅等待 HTTP Server 无法覆盖这些工作。
// 本方法不另设计时器：HTTP 收尾及这里所有实例共用调用方传入的同一个停机 Context。
// 当前停机总预算默认 10 秒，由 -shutdown-timeout 配置，与接口各自的响应等待时间无关。
// 例如 HTTP 收尾已用 4 秒，后续实例共用剩余约 6 秒，不是每个实例再等待完整 10 秒。
//
// 每个 ResponseTimeout.Shutdown 先停止自身接纳新工作，再等待自己的 active 计数归零。
// active 仅在内部 Handler 结束时减少；最后一个结束后关闭 drained 通道，唤醒停机等待。
// 等待期限到达只返回错误，不取消业务；但调用方随后退出进程时，剩余业务无法继续执行。
// 返回错误表示未确认内部处理全部结束，不能据此认为业务成功、事务回滚或依赖可安全关闭。
// 进程入口只有确认 HTTP 和内部处理均已结束，才正常关闭数据库等共享资源。
func (h *HTTPHandler) Shutdown(ctx context.Context) error {
	var shutdownErrors []error
	// 依次处理默认和各覆盖实例；轮到某个实例时，才由它停止接纳新工作并等待。
	// 即使前一个等待失败，也继续调用后面的 Shutdown，使其同样进入停止状态。
	for _, timeout := range h.timeouts {
		if err := timeout.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	// 全部成功时返回 nil；否则保留各实例的收尾错误，交给进程入口处理。
	return errors.Join(shutdownErrors...)
}
