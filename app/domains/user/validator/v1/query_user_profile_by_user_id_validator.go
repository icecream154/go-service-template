package v1

import (
	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	"go-service-template/internal/check/scalar"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// QueryUserProfileByUserIDValidator 绑定并校验按用户 ID 查询资料的请求。
type QueryUserProfileByUserIDValidator struct {
	// controller 复用路由创建的控制器，不保存单次请求的 DTO。
	controller *userv1controller.UserProfileController
}

// NewQueryUserProfileByUserIDValidator 保存控制器引用，不执行绑定或业务查询。
func NewQueryUserProfileByUserIDValidator(controller *userv1controller.UserProfileController) *QueryUserProfileByUserIDValidator {
	return &QueryUserProfileByUserIDValidator{controller: controller}
}

// QueryUserProfileByUserID 依次绑定、校验请求，全部通过后才调用 Controller。
func (v *QueryUserProfileByUserIDValidator) QueryUserProfileByUserID(ctx *gin.Context) {
	var request userv1dto.QueryUserProfileByUserIDRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(""))
		return
	}
	if details := validateQueryUserProfileByUserIDRequest(&request); len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument("", details...))
		return
	}
	v.controller.QueryUserProfileByUserID(ctx, &request)
}

// validateQueryUserProfileByUserIDRequest 复用公共检查验证 ID 文本的格式、范围和正数要求。
// 请求文本保持原值，用户是否存在由 Service 查询时判断。
func validateQueryUserProfileByUserIDRequest(request *userv1dto.QueryUserProfileByUserIDRequest) []response.FieldError {
	details := make([]response.FieldError, 0, 1)
	if _, err := scalar.ParsePositiveInt64(request.UserID); err != nil {
		details = append(details, response.FieldError{
			Field: "user_id", Reason: "must be a positive 64-bit integer",
		})
	}
	return details
}
