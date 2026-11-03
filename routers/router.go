// Package routers 创建 HTTP 路由并绑定请求处理方法。
package routers

import (
	"net/http"

	usercontroller "go-service-template/app/domains/user/controller"

	"github.com/gin-gonic/gin"
)

// New 创建带请求日志和异常恢复中间件的 Gin 路由。
// userProfiles 用于处理用户资料查询，传入前应已完成依赖初始化。
func New(userProfiles *usercontroller.UserProfileController) *gin.Engine {
	router := gin.Default()

	// 存活检查固定返回进程状态，不执行数据库查询。
	router.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	// user_id 是必填路径参数，由控制器完成解析和响应处理。
	router.GET("/users/:user_id", userProfiles.QueryUserProfileByUserID)

	return router
}
