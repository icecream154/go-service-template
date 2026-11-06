// Package v1 绑定并校验用户 V1 接口的请求参数，再将 Request DTO 交给 Controller。
package v1

import (
	"errors"
	"log"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	"go-service-template/app/domains/user/models"
	"go-service-template/internal/check"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// CreateUserValidator 持有用户资料控制器，不保存单次请求的 DTO 或绑定结果。
type CreateUserValidator struct {
	// controller 复用路由创建的控制器，绑定和参数校验通过后接收本次请求的 DTO。
	controller *userv1controller.UserProfileController
}

// NewCreateUserValidator 复用路由已创建的控制器，不执行请求绑定或业务操作。
func NewCreateUserValidator(controller *userv1controller.UserProfileController) *CreateUserValidator {
	return &CreateUserValidator{controller: controller}
}

// CreateUser 为本次请求创建 DTO，绑定和字段校验通过后才调用 Controller。
// 账户唯一性、ID 生成及创建操作继续由现有 Service 处理。
func (v *CreateUserValidator) CreateUser(ctx *gin.Context) {
	var request userv1dto.CreateUserRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}
	details, err := validateCreateUserRequest(&request)
	if err != nil {
		log.Printf("Validate CreateUser request failed: error=%v", err)
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
	v.controller.CreateUser(ctx, &request)
}

// validateCreateUserRequest 校验绑定后的创建用户字段，不改写 DTO 或访问业务状态。
// 按请求字段顺序组合领域规则，每个字段最多返回一条问题。
// 已知输入问题返回字段明细；无效规则或未知错误立即返回，丢弃已有明细。
func validateCreateUserRequest(request *userv1dto.CreateUserRequest) ([]response.FieldError, error) {
	var details []response.FieldError
	if err := models.ValidateAccountSource(request.AccountSource); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "account_source",
			Reason: err.Error(),
		})
	}
	if err := models.ValidateExternalAccountID(request.ExternalAccountID); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "external_account_id",
			Reason: err.Error(),
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

	// 头像可省略或为 null，显式空字符串也保留原有语义。
	if request.AvatarURL == nil || *request.AvatarURL == "" {
		return details, nil
	}
	if err := models.ValidateAvatarURL(*request.AvatarURL); err != nil {
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
