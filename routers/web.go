package routers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"go-service-template/app/composition"
	healthRouter "go-service-template/app/domains/health/routers"
	"go-service-template/app/http/middleware/access_log"
	"go-service-template/app/http/middleware/recovery"
	"go-service-template/app/http/middleware/request_context"
	"go-service-template/app/shared/contracts"
	"go-service-template/app/utils/response"
	"go-service-template/config"
)

// InitWebRouter 负责 HTTP 机制和路由装配；资源和业务服务已经由启动阶段初始化。
func InitWebRouter(runtime *composition.Runtime, cfg config.HTTPConfig, logger *zap.Logger) (*gin.Engine, error) {
	if runtime == nil || runtime.Health == nil || logger == nil {
		return nil, fmt.Errorf("router requires initialized health service and logger")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	// 日志与恢复器包住上下文校验，连追踪头校验失败也会记录最终状态。
	router.Use(access_log.Logger(logger), recovery.Recovery(logger), request_context.CheckRequestContext())
	router.NoRoute(func(c *gin.Context) {
		response.ReturnJson(c, http.StatusNotFound, http.StatusNotFound, "Not Found", nil)
	})
	router.NoMethod(func(c *gin.Context) {
		// Gin 根据已注册路由设置 Allow，扩展领域时无需维护第二份方法清单。
		response.ReturnJson(c, http.StatusMethodNotAllowed, http.StatusMethodNotAllowed, "Method Not Allowed", nil)
	})
	mountDomainRoutes(router.Group(cfg.BasePath), runtime)
	return router, nil
}

func mountDomainRoutes(backend *gin.RouterGroup, runtime *composition.Runtime) {
	health := healthRouter.NewRouter(runtime.Health)
	// 运维健康地址与 V1 使用同一控制器、用例和响应规则。
	health.RegisterPublicRoutes(backend)
	versioned := []contracts.VersionedRouter{health}
	v1 := backend.Group("/v1")
	for _, domain := range versioned {
		domain.RegisterV1Routes(v1)
	}
}
