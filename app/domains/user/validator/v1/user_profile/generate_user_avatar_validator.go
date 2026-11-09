package userprofile

import (
	"errors"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/internal/check"
	checktext "go-service-template/internal/check/text"
	"go-service-template/internal/logger"
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
	// 1. 为当前请求创建独立 DTO，并沿用公共绑定方法读取输入；不把请求正文写入日志或共享字段。
	var request userprofiledto.GenerateUserAvatarRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		// 1.1. 绑定失败继续按输入错误响应，输出后立即返回，不进入校验或业务处理。
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}

	// 2. 执行现有提示词校验，将已知字段问题和规则故障分别交给原有响应路径。
	details, err := validateGenerateUserAvatarRequest(&request)
	if err != nil {
		// 2.1. 未知或规则校验故障使用共享 Logger，沿用入站 HTTP 作用域及 TraceId。
		// Validator 不建立新的组件身份；日志只记录固定阶段说明，原始错误仍用于原有响应。
		logger.Current().Error(ctx.Request.Context(), "Validate user avatar request failed", logger.Fields{
			Error: "User avatar request validation failed",
		})
		// 2.2. 日志写入故障由 Logger 内部独立兜底，不改变校验错误分类、响应文案或返回顺序。
		response.Fail(ctx, response.ErrorInfo{
			Code:    response.CodeInternalError,
			Message: err.Error(),
		})
		return
	}
	// 3. 已知字段问题继续返回已有明细，不把非法用户输入当作内部规则故障记录。
	if len(details) > 0 {
		// 3.1. 使用首条字段说明作为消息并保留全部明细，响应后结束当前入口。
		response.Fail(ctx, response.InvalidArgument(details[0].Error(), details...))
		return
	}
	// 4. 绑定和校验均通过后，将同一请求及 DTO 交给 Controller，不额外创建生命周期日志。
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
