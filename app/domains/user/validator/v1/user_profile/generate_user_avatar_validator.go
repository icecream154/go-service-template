package userprofile

import (
	"errors"
	"log"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/internal/check"
	checktext "go-service-template/internal/check/text"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// GenerateUserAvatarValidator 持有用户资料控制器，不保存单次请求的 DTO 或绑定结果。
type GenerateUserAvatarValidator struct {
	// controller 复用路由创建的控制器，在绑定和参数校验通过后接收本次请求。
	controller *userv1controller.UserProfileController
}

// NewGenerateUserAvatarValidator 复用路由已创建的控制器，不执行请求绑定或业务操作。
func NewGenerateUserAvatarValidator(controller *userv1controller.UserProfileController) *GenerateUserAvatarValidator {
	return &GenerateUserAvatarValidator{controller: controller}
}

// GenerateUserAvatar 为本次请求创建 DTO，绑定和字段校验通过后才调用 Controller。
func (v *GenerateUserAvatarValidator) GenerateUserAvatar(ctx *gin.Context) {
	var request userprofiledto.GenerateUserAvatarRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}

	details, err := validateGenerateUserAvatarRequest(&request)
	if err != nil {
		log.Printf("Validate GenerateUserAvatar request failed: error=%v", err)
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
	v.controller.GenerateUserAvatar(ctx, &request)
}

// validateGenerateUserAvatarRequest 检查提示词是非空白的 UTF-8 文本，不改写输入。
// 已知输入问题返回字段明细，无效规则或未知错误原样返回。
func validateGenerateUserAvatarRequest(request *userprofiledto.GenerateUserAvatarRequest) ([]response.FieldError, error) {
	if err := checktext.NotBlank(request.Prompt); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		return []response.FieldError{{Field: "prompt", Reason: err.Error()}}, nil
	}
	return nil, nil
}
