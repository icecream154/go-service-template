// Package routers 注册用户领域的 HTTP 路由。
package routers

import (
	userv1controller "go-service-template/app/domains/user/controller/v1"

	"github.com/gin-gonic/gin"
)

// UserRouter 持有用户领域的控制器，并按接口版本注册路由。
type UserRouter struct {
	// userProfiles 处理 V1 用户资料请求，不保存单次请求的数据。
	userProfiles *userv1controller.UserProfileController
}

// NewRouter 创建并保存用户资料控制器，不执行数据库查询。
func NewRouter() *UserRouter {
	return &UserRouter{userProfiles: userv1controller.NewUserProfileController()}
}

// RegisterV1Routes 在传入的 V1 分组下注册用户资源路由。
// user_id 是必填路径参数，交给控制器解析并输出响应。
func (r *UserRouter) RegisterV1Routes(v1 *gin.RouterGroup) {
	users := v1.Group("/users")
	users.GET("/:user_id", r.userProfiles.QueryUserProfileByUserID)
}
