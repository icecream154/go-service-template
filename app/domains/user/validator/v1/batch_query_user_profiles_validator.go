package v1

import (
	"errors"
	"fmt"
	"log"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/internal/check"
	"go-service-template/internal/check/collection"
	"go-service-template/internal/check/scalar"
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
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}
	details, err := validateBatchQueryUserProfilesRequest(&request)
	if err != nil {
		log.Printf("Validate BatchQueryUserProfiles request failed: error=%v", err)
		response.Fail(ctx, response.ErrorInfo{
			Code:    response.CodeInternalError,
			Message: err.Error(),
		})
		return
	}
	if len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument(details[0].Error(), details...))
		return
	}
	v.controller.BatchQueryUserProfiles(ctx, &request)
}

// validateBatchQueryUserProfilesRequest 先按用例契约检查数量，再复用公共检查验证元素。
// 数量按去重前计算，不改写输入顺序；列表字段最多返回一条问题。
// 已知输入问题返回字段明细；无效规则或未知错误立即返回，丢弃已有明细。
func validateBatchQueryUserProfilesRequest(request *userv1dto.BatchQueryUserProfilesRequest) ([]response.FieldError, error) {
	var details []response.FieldError
	if err := collection.SliceLengthInRange(request.UserIDList, userservice.MinUserProfileBatchSize, userservice.MaxUserProfileBatchSize); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "user_id_list",
			Reason: fmt.Sprintf("must contain between %d and %d user IDs", userservice.MinUserProfileBatchSize, userservice.MaxUserProfileBatchSize),
		})
		return details, nil
	}
	if err := collection.Each(request.UserIDList, func(value string) error {
		_, err := scalar.ParsePositiveInt64(value)
		return err
	}); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "user_id_list",
			Reason: "each item must be a positive 64-bit integer string",
		})
	}
	return details, nil
}
