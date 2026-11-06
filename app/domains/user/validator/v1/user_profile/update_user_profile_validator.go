package userprofile

import (
	"errors"
	"fmt"
	"log"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/internal/check"
	"go-service-template/internal/check/relation"
	"go-service-template/internal/check/scalar"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// UpdateUserProfileValidator 持有资料控制器，不保存单次请求的 DTO 或绑定结果。
type UpdateUserProfileValidator struct {
	// controller 在绑定和参数校验通过后接收本次更新请求。
	controller *userv1controller.UserProfileController
}

// NewUpdateUserProfileValidator 复用路由创建的控制器，不执行请求绑定或业务操作。
func NewUpdateUserProfileValidator(controller *userv1controller.UserProfileController) *UpdateUserProfileValidator {
	return &UpdateUserProfileValidator{controller: controller}
}

// UpdateUserProfile 为本次请求创建 DTO，绑定和字段校验通过后才调用 Controller。
// 本人权限、启用状态和实际资料版本继续由 Service 判断。
func (v *UpdateUserProfileValidator) UpdateUserProfile(ctx *gin.Context) {
	var request userprofiledto.UpdateUserProfileRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}
	details, err := validateUpdateUserProfileRequest(&request)
	if err != nil {
		log.Printf("Validate UpdateUserProfile request failed: error=%v", err)
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
	v.controller.UpdateUserProfile(ctx, &request)
}

// validateUpdateUserProfileRequest 组合数值检查、用例版本边界和完整领域字段规则。
// 按请求字段顺序汇总问题，每个字段最多一条；头像必须是非空的 HTTP/HTTPS 地址。
// 已知输入问题返回字段明细；无效规则或未知错误立即返回，丢弃已有明细。
func validateUpdateUserProfileRequest(request *userprofiledto.UpdateUserProfileRequest) ([]response.FieldError, error) {
	var details []response.FieldError
	if err := scalar.PositiveInt64(request.OperatorUserID); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{Field: "operator_user_id", Reason: "must be a positive 64-bit integer"})
	}
	if err := scalar.PositiveInt64(request.UserID); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{Field: "user_id", Reason: "must be a positive 64-bit integer"})
	}
	if err := relation.NotNil(request.ExpectedVersion); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{Field: "expected_version", Reason: "must be explicitly provided with a non-null integer"})
	} else if err := scalar.Int64InRange(*request.ExpectedVersion, userservice.MinUserProfileExpectedVersion, userservice.MaxUserProfileExpectedVersion); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "expected_version",
			Reason: fmt.Sprintf("must be between %d and %d", userservice.MinUserProfileExpectedVersion, userservice.MaxUserProfileExpectedVersion),
		})
	}

	if err := models.ValidateUserName(request.UserName); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "user_name",
			Reason: err.Error(),
		})
	}

	// 更新接口要求有效头像，缺失、null 和空字符串都由有值规则拒绝。
	if err := models.ValidateAvatarURL(request.AvatarURL); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "avatar_url",
			Reason: err.Error(),
		})
	}
	return details, nil
}
