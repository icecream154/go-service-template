package middleware

import (
	"encoding/json"
	"log"
	"net/http"

	"go-service-template/internal/response"
	"go-service-template/internal/traceid"
)

// TraceID 仅沿用请求头中格式合法的 TraceId，缺失、为空或非法时重新生成。
// 标识必须是 32 个小写十六进制字符且不全为零；格式非法不拒绝业务请求。
// 应包在响应等待中间件外层；响应头返回相同标识，不修改请求头或业务 JSON。
// 本阶段只处理自定义 TraceId 头，尚不提取 traceparent 或创建 Span。
func TraceID(next http.Handler) http.Handler {
	// 1. 装配时检查下游 Handler，避免把启动配置错误留到实际请求进入时才暴露。
	if next == nil {
		panic("trace ID handler must not be nil")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2. 每次请求读取自己的入站标识；只有严格符合标准格式时才原样沿用。
		// 不修剪或转换旧值，避免改变调用方发送的标识。
		id := r.Header.Get(traceid.HeaderName)
		if !traceid.IsValid(id) {
			// 2.1. 缺失、空值、全零及其他非法格式统一生成新标识，不因此拒绝正常业务。
			var err error
			id, err = traceid.New()
			if err != nil {
				// 2.2. 随机源失败时无法建立合法链路标识，保留原有统一 HTTP 500 返回路径。
				// 此时未进入业务流程，不回显非法输入，也不向下游交付不完整的 Context。
				log.Printf("Generate HTTP trace ID failed: error=%v", err)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				failure := response.InternalError()
				if err := json.NewEncoder(w).Encode(response.Response[any]{Error: &failure}); err != nil {
					// 2.3. 错误响应写入失败仅记录基础故障，不重试响应或继续调用业务 Handler。
					log.Printf("Write HTTP trace ID error response failed: error=%v", err)
				}
				return
			}
		}

		// 3. 响应头和 Request Context 使用同一个已确定的合法值，覆盖后续成功、拒绝和超时响应。
		w.Header().Set(traceid.HeaderName, id)
		request := r.WithContext(traceid.WithContext(r.Context(), id))
		// 4. 仅分发一次请求，后续 Logger 和公司 HTTP Client 从该 Context 读取同一标识。
		next.ServeHTTP(w, request)
	})
}
