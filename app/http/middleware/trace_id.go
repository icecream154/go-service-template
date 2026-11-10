package middleware

import (
	"encoding/json"
	"log"
	"net/http"

	"go-service-template/internal/response"
	"go-service-template/internal/tracing"
)

// TraceID 优先沿用 Context 中已确定的 TraceId，没有值时再校验请求头，缺失或非法时重新生成。
// 标识必须是 32 个小写十六进制字符且不全为零；格式非法不拒绝业务请求。
// 应包在响应等待中间件外层；响应头返回相同标识，不修改请求头或业务 JSON。
// 本阶段只处理自定义 TraceId 头，尚不提取 traceparent 或创建 Span。
func TraceID(next http.Handler) http.Handler {
	// 1. 装配时检查下游 Handler，避免把启动配置错误留到实际请求进入时才暴露。
	if next == nil {
		panic("trace ID handler must not be nil")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2. 优先通过统一入口沿用已有上下文标识，确保标准 SpanContext 不被旧头中的不同值覆盖。
		// 普通入站尚无上下文标识时，再读取请求头；不修剪或转换输入。
		id := tracing.TraceID(r.Context())
		if id == "" {
			// 2.1. 只有上层尚未确定链路标识时才以自定义请求头作为候选值。
			id = r.Header.Get(tracing.TraceIDHeaderName)
		}
		if !tracing.IsValidTraceID(id) {
			// 2.2. 缺失、空值、全零及其他非法格式统一生成新标识，不因此拒绝正常业务。
			var err error
			id, err = tracing.NewTraceID()
			if err != nil {
				// 2.3. 随机源失败时无法建立合法链路标识，保留原有统一 HTTP 500 返回路径。
				// 此时未进入业务流程，不回显非法输入，也不向下游交付不完整的 Context。
				log.Printf("Generate HTTP trace ID failed: error=%v", err)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				failure := response.InternalError()
				if err := json.NewEncoder(w).Encode(response.Response[any]{Error: &failure}); err != nil {
					// 2.4. 错误响应写入失败仅记录基础故障，不重试响应或继续调用业务 Handler。
					log.Printf("Write HTTP trace ID error response failed: error=%v", err)
				}
				return
			}
		}

		// 3. 响应头和 Request Context 使用同一个已确定的合法值，覆盖后续成功、拒绝和超时响应。
		request := r.WithContext(tracing.WithTraceID(r.Context(), id))
		w.Header().Set(tracing.TraceIDHeaderName, tracing.TraceID(request.Context()))
		// 4. 仅分发一次请求，后续 Logger 和公司 HTTP Client 从该 Context 读取同一标识。
		next.ServeHTTP(w, request)
	})
}
