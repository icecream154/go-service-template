package v1

import (
	"math"
	"net/url"
	"strings"
	"unicode/utf8"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
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
	var request userv1dto.UpdateUserProfileRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(""))
		return
	}
	if details := validateUpdateUserProfileRequest(&request); len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument("", details...))
		return
	}
	v.controller.UpdateUserProfile(ctx, &request)
}

// validateUpdateUserProfileRequest 在函数内完整校验更新输入，不改写 DTO 或读取业务状态。
// 按请求字段顺序汇总问题，每个字段最多一条；头像必须是非空的 HTTP/HTTPS 地址。
func validateUpdateUserProfileRequest(request *userv1dto.UpdateUserProfileRequest) []response.FieldError {
	details := make([]response.FieldError, 0, 5)
	if request.OperatorUserID <= 0 {
		details = append(details, response.FieldError{Field: "operator_user_id", Reason: "must be a positive 64-bit integer"})
	}
	if request.UserID <= 0 {
		details = append(details, response.FieldError{Field: "user_id", Reason: "must be a positive 64-bit integer"})
	}
	if request.ExpectedVersion == nil {
		details = append(details, response.FieldError{Field: "expected_version", Reason: "must be explicitly provided with a non-null integer"})
	} else if *request.ExpectedVersion < 0 || *request.ExpectedVersion == math.MaxInt64 {
		details = append(details, response.FieldError{Field: "expected_version", Reason: "must be between 0 and 9223372036854775806"})
	}

	var nameReason string
	switch {
	case !utf8.ValidString(request.UserName):
		nameReason = "must be valid UTF-8 text"
	case strings.TrimSpace(request.UserName) == "":
		nameReason = "must not be blank"
	case utf8.RuneCountInString(request.UserName) > 64:
		nameReason = "must contain at most 64 Unicode code points"
	}
	if nameReason != "" {
		details = append(details, response.FieldError{Field: "user_name", Reason: nameReason})
	}

	avatarURL := request.AvatarURL
	var avatarReason string
	switch {
	case !utf8.ValidString(avatarURL):
		avatarReason = "must be valid UTF-8 text"
	case strings.TrimSpace(avatarURL) == "":
		// 标准绑定下，缺失、null 和空字符串都按未提供有效头像处理。
		avatarReason = "must not be blank"
	case utf8.RuneCountInString(avatarURL) > 512:
		avatarReason = "must contain at most 512 Unicode code points"
	default:
		parsed, err := url.Parse(avatarURL)
		if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			avatarReason = "must be an absolute HTTP(S) URL with a hostname"
		}
	}
	if avatarReason != "" {
		details = append(details, response.FieldError{Field: "avatar_url", Reason: avatarReason})
	}
	return details
}
