// Package routers 注册用户领域的 HTTP 路由。
package routers

import (
	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1validator "go-service-template/app/domains/user/validator/v1"

	"github.com/gin-gonic/gin"
)

// UserRouter 持有用户领域的控制器，并按接口版本注册路由。
type UserRouter struct {
	// userProfiles 处理 V1 用户资料请求，不保存单次请求的数据。
	userProfiles *userv1controller.UserProfileController
	// userProfileValidator 绑定创建用户请求，再交给同一个用户资料控制器处理。
	userProfileValidator *userv1validator.UserProfileValidator
}

// NewRouter 创建并保存用户资料控制器及创建请求的 Validator，不执行数据库查询。
func NewRouter() *UserRouter {
	controller := userv1controller.NewUserProfileController()
	return &UserRouter{
		userProfiles:         controller,
		userProfileValidator: userv1validator.NewUserProfileValidator(controller),
	}
}

// RegisterV1Routes 在传入的 V1 分组下按领域、Controller 和业务用例注册路由。
// 简单查询使用 Query 参数，创建、更新和批量查询使用 JSON 请求体。
func (r *UserRouter) RegisterV1Routes(v1 *gin.RouterGroup) {
	user := v1.Group("/user")
	userProfile := user.Group("/userProfile")
	userProfile.GET("/queryUserProfileByUserId", r.userProfiles.QueryUserProfileByUserID)
	userProfile.GET("/queryUserProfilePage", r.userProfiles.QueryUserProfilePage)
	userProfile.POST("/createUser", r.userProfileValidator.CreateUser)
	userProfile.POST("/updateUserProfile", r.userProfiles.UpdateUserProfile)
	userProfile.POST("/batchQueryUserProfiles", r.userProfiles.BatchQueryUserProfiles)
}
