package transport

import (
	"go-service-template/app/domains/health/services"
	"go-service-template/app/http/middleware/request_context"

	"github.com/gin-gonic/gin"
)

func Call(c *gin.Context) services.Call {
	return services.Call{Context: c.Request.Context(), Trace: request_context.GetRequestContext(c)}
}
