package request_context

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"go-service-template/app/shared/requestmeta"
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
)

const requestContextKey = "requestContextKey"

type RequestContext = requestmeta.RequestContext

func GetRequestContext(c *gin.Context) *RequestContext {
	if value, exists := c.Get(requestContextKey); exists {
		if meta, ok := value.(*RequestContext); ok && meta != nil {
			return meta
		}
	}
	return &RequestContext{}
}

func CheckRequestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		meta := &RequestContext{StartUnixNano: time.Now().UnixNano()}
		err := c.ShouldBindHeader(meta)
		valid := err == nil && validTraceID(meta.TraceId)
		if meta.TraceId == "" || !valid {
			meta.TraceId = newTraceID()
		}
		c.Set(requestContextKey, meta)
		// 提前设置，绑定失败、未知路由和恢复响应也能关联本次请求。
		c.Header("TraceId", meta.TraceId)
		if !valid {
			response.ErrorParam(c, nil)
			return
		}
		c.Next()
	}
}

func validTraceID(value string) bool {
	if len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == ':' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func newTraceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("request context: random source unavailable")
	}
	return hex.EncodeToString(value[:])
}
