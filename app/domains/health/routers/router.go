package routers

import (
	v1 "go-service-template/app/domains/health/controllers/v1"
	requests "go-service-template/app/domains/health/requests/v1"
	"go-service-template/app/domains/health/services"
	binding "go-service-template/app/http/requests"
	"go-service-template/app/shared/contracts"

	"github.com/gin-gonic/gin"
)

type HealthRouter struct {
	controller *v1.HealthController
}

func NewRouter(service services.UseCases) *HealthRouter {
	return &HealthRouter{controller: v1.NewHealthControllerWithService(service)}
}

func (r *HealthRouter) RegisterV1Routes(group *gin.RouterGroup) {
	r.registerRoutes(group)
}

// RegisterPublicRoutes 提供不带版本前缀的存活检查入口。
func (r *HealthRouter) RegisterPublicRoutes(group *gin.RouterGroup) {
	r.registerRoutes(group)
}

func (r *HealthRouter) registerRoutes(group *gin.RouterGroup) {
	group.GET("/health", binding.Bind[requests.CheckReq](), r.controller.Check)
	group.HEAD("/health", binding.Bind[requests.CheckReq](), r.controller.Check)
}

var _ contracts.VersionedRouter = (*HealthRouter)(nil)
