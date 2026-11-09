package middleware

import (
	"encoding/json"
	"log"
	"net/http"

	"go-service-template/internal/response"
	"go-service-template/internal/traceid"
)

// TraceID 沿用请求头中的非空 TraceId，缺失或为空时生成，并保存到标准 Context。
// 应包在响应等待中间件外层；响应头返回相同标识，不修改请求头或业务 JSON。
func TraceID(next http.Handler) http.Handler {
	if next == nil {
		panic("trace ID handler must not be nil")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(traceid.HeaderName)
		if id == "" {
			var err error
			id, err = traceid.New()
			if err != nil {
				log.Printf("Generate HTTP trace ID failed: error=%v", err)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				failure := response.InternalError()
				if err := json.NewEncoder(w).Encode(response.Response[any]{Error: &failure}); err != nil {
					log.Printf("Write HTTP trace ID error response failed: error=%v", err)
				}
				return
			}
		}

		w.Header().Set(traceid.HeaderName, id)
		request := r.WithContext(traceid.WithContext(r.Context(), id))
		next.ServeHTTP(w, request)
	})
}
