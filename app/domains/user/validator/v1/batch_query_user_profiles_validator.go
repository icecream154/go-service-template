package v1

import (
	"strconv"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// BatchQueryUserProfilesValidator 持有资料控制器，不保存单次请求的 DTO 或绑定结果。
type BatchQueryUserProfilesValidator struct {
	// controller 在绑定和参数校验通过后接收本次批量查询请求。
	controller *userv1controller.UserProfileController
}

// NewBatchQueryUserProfilesValidator 复用路由创建的控制器，不执行请求绑定或业务操作。
func NewBatchQueryUserProfilesValidator(controller *userv1controller.UserProfileController) *BatchQueryUserProfilesValidator {
	return &BatchQueryUserProfilesValidator{controller: controller}
}

// BatchQueryUserProfiles 为本次请求创建 DTO，绑定和字段校验通过后才调用 Controller。
// 重复 ID 的去重、资料排序和缺失目标的解释继续由 Service 处理。
func (v *BatchQueryUserProfilesValidator) BatchQueryUserProfiles(ctx *gin.Context) {
	var request userv1dto.BatchQueryUserProfilesRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(""))
		return
	}
	if details := validateBatchQueryUserProfilesRequest(&request); len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument("", details...))
		return
	}
	v.controller.BatchQueryUserProfiles(ctx, &request)
}

// validateBatchQueryUserProfilesRequest 在函数内完整校验列表数量和每项的正 int64 编码。
// 数量按去重前计算，不改写输入顺序；列表字段最多返回一条问题。
func validateBatchQueryUserProfilesRequest(request *userv1dto.BatchQueryUserProfilesRequest) []response.FieldError {
	details := make([]response.FieldError, 0, 1)
	if len(request.UserIDList) == 0 || len(request.UserIDList) > 100 {
		details = append(details, response.FieldError{Field: "user_id_list", Reason: "must contain between 1 and 100 user IDs"})
		return details
	}
	for _, value := range request.UserIDList {
		userID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || userID <= 0 {
			details = append(details, response.FieldError{Field: "user_id_list", Reason: "each item must be a positive 64-bit integer string"})
			break
		}
	}
	return details
}
