package access_log

import (
	"time"

	"go-service-template/app/http/middleware/request_context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 只记录请求摘要，不读取正文、Token 或查询参数。
func Logger(log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		log.Info("HTTP request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Int64("latency", time.Since(started).Microseconds()),
			zap.String("trace_id", request_context.GetRequestContext(c).TraceId),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}
