// Package routers 注册用户领域的 HTTP 路由。
package routers

import (
	userv1controller "go-service-template/app/domains/user/controller/v1"
	userprofilevalidator "go-service-template/app/domains/user/validator/v1/user_profile"

	"github.com/gin-gonic/gin"
)

// UserRouter 持有用户领域的控制器，并按接口版本注册路由。
type UserRouter struct {
	// userProfiles 处理 V1 用户资料请求，不保存单次请求的数据。
	userProfiles *userv1controller.UserProfileController
	// createUserValidator 绑定创建用户请求，再交给同一个用户资料控制器处理。
	createUserValidator *userprofilevalidator.CreateUserValidator
	// generateUserAvatarValidator 绑定并校验头像生成请求。
	generateUserAvatarValidator *userprofilevalidator.GenerateUserAvatarValidator
	// queryUserProfileByUserIDValidator 绑定并校验按 ID 查询请求。
	queryUserProfileByUserIDValidator *userprofilevalidator.QueryUserProfileByUserIDValidator
	// queryUserProfilePageValidator 绑定并校验分页查询请求。
	queryUserProfilePageValidator *userprofilevalidator.QueryUserProfilePageValidator
	// updateUserProfileValidator 绑定并校验完整替换资料的请求。
	updateUserProfileValidator *userprofilevalidator.UpdateUserProfileValidator
	// batchQueryUserProfilesValidator 绑定并校验批量查询请求。
	batchQueryUserProfilesValidator *userprofilevalidator.BatchQueryUserProfilesValidator
}

// NewRouter 创建用户资料控制器及各用例的 Validator，不执行数据库查询。
func NewRouter() *UserRouter {
	controller := userv1controller.NewUserProfileController()
	return &UserRouter{
		userProfiles:                      controller,
		createUserValidator:               userprofilevalidator.NewCreateUserValidator(controller),
		generateUserAvatarValidator:       userprofilevalidator.NewGenerateUserAvatarValidator(controller),
		queryUserProfileByUserIDValidator: userprofilevalidator.NewQueryUserProfileByUserIDValidator(controller),
		queryUserProfilePageValidator:     userprofilevalidator.NewQueryUserProfilePageValidator(controller),
		updateUserProfileValidator:        userprofilevalidator.NewUpdateUserProfileValidator(controller),
		batchQueryUserProfilesValidator:   userprofilevalidator.NewBatchQueryUserProfilesValidator(controller),
	}
}

// RegisterV1Routes 在传入的 V1 分组下按领域、Controller 和业务用例注册路由。
// 各请求先由对应 Validator 绑定和校验，再传给同一用户资料控制器。
func (r *UserRouter) RegisterV1Routes(v1 *gin.RouterGroup) {
	user := v1.Group("/user")
	userProfile := user.Group("/userProfile")
	userProfile.GET("/queryUserProfileByUserId", r.queryUserProfileByUserIDValidator.QueryUserProfileByUserID)
	userProfile.GET("/queryUserProfilePage", r.queryUserProfilePageValidator.QueryUserProfilePage)
	userProfile.POST("/createUser", r.createUserValidator.CreateUser)
	userProfile.POST("/generateUserAvatar", r.generateUserAvatarValidator.GenerateUserAvatar)
	userProfile.POST("/updateUserProfile", r.updateUserProfileValidator.UpdateUserProfile)
	userProfile.POST("/batchQueryUserProfiles", r.batchQueryUserProfilesValidator.BatchQueryUserProfiles)
}
