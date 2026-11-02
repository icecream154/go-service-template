package recovery

import (
	"runtime/debug"

	"go-service-template/app/http/middleware/request_context"
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Recovery 记录异常及调用栈，对外只返回固定系统错误。
func Recovery(log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}
	return func(c *gin.Context) {
		defer func() {
			if value := recover(); value != nil {
				log.Error("HTTP panic",
					zap.Any("panic", value),
					zap.ByteString("stack", debug.Stack()),
					zap.String("trace_id", request_context.GetRequestContext(c).TraceId),
				)
				if c.Writer.Written() {
					c.Abort()
					return
				}
				response.ErrorSystem(c)
			}
		}()
		c.Next()
	}
}
