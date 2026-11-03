// Package routers 创建 HTTP 路由并绑定请求处理方法。
package routers

import (
	"net/http"

	userrouter "go-service-template/app/domains/user/routers"

	"github.com/gin-gonic/gin"
)

// New 创建带请求日志和异常恢复中间件的 Gin 路由，并注册各版本的领域接口。
// 用户领域的控制器在注册前创建，后续请求复用已绑定的处理方法。
func New() *gin.Engine {
	router := gin.Default()

	// 存活检查固定返回进程状态，不执行数据库查询。
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	// 顶层统一管理 API 前缀和版本，具体资源路径由各领域注册。
	api := router.Group("/api")
	v1 := api.Group("/v1")
	userrouter.NewRouter().RegisterV1Routes(v1)

	return router
}
