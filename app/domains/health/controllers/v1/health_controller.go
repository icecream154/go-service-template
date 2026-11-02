package v1

import (
	requests "go-service-template/app/domains/health/requests/v1"
	output "go-service-template/app/domains/health/responses/v1"
	"go-service-template/app/domains/health/services"
	"go-service-template/app/domains/health/transport"
	binding "go-service-template/app/http/requests"
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
)

type HealthController struct {
	service services.UseCases
}

func NewHealthControllerWithService(service services.UseCases) *HealthController {
	if service == nil {
		panic("health: service is required")
	}
	return &HealthController{service: service}
}

// Check 调用进程存活用例，HTTP 输入与输出由各自适配包处理。
func (c *HealthController) Check(ctx *gin.Context) {
	if _, ok := binding.Get[requests.CheckReq](ctx); !ok {
		response.ErrorParam(ctx, nil)
		return
	}
	output.Write(ctx, c.service.Check(transport.Call(ctx)))
}
