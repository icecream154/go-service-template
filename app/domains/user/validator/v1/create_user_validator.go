// Package v1 绑定并校验用户 V1 接口的请求参数，再将 Request DTO 交给 Controller。
package v1

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
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
		response.Fail(ctx, response.InvalidArgument(""))
		return
	}
	if details := validateCreateUserRequest(&request); len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument("", details...))
		return
	}
	v.controller.CreateUser(ctx, &request)
}

// validateCreateUserRequest 校验绑定后的创建用户字段，不改写 DTO 或访问业务状态。
// 按请求字段顺序汇总问题，每个字段最多一条；长度以 Unicode 码点计数。
func validateCreateUserRequest(request *userv1dto.CreateUserRequest) []response.FieldError {
	details := make([]response.FieldError, 0, 4)
	textFields := []struct {
		name    string
		value   string
		maximum int
	}{
		{name: "account_source", value: request.AccountSource, maximum: 32},
		{name: "external_account_id", value: request.ExternalAccountID, maximum: 128},
		{name: "user_name", value: request.UserName, maximum: 64},
	}
	for _, field := range textFields {
		var reason string
		switch {
		case !utf8.ValidString(field.value):
			reason = "must be valid UTF-8 text"
		case strings.TrimSpace(field.value) == "":
			reason = "must not be blank"
		case utf8.RuneCountInString(field.value) > field.maximum:
			reason = fmt.Sprintf("must contain at most %d Unicode code points", field.maximum)
		}
		if reason != "" {
			details = append(details, response.FieldError{Field: field.name, Reason: reason})
		}
	}

	// 头像可省略或为 null，显式空字符串也保留原有语义。
	if request.AvatarURL == nil || *request.AvatarURL == "" {
		return details
	}
	avatarURL := *request.AvatarURL
	var reason string
	switch {
	case !utf8.ValidString(avatarURL):
		reason = "must be valid UTF-8 text"
	case utf8.RuneCountInString(avatarURL) > 512:
		reason = "must contain at most 512 Unicode code points"
	default:
		parsed, err := url.Parse(avatarURL)
		if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			reason = "must be an absolute HTTP(S) URL with a hostname"
		}
	}
	if reason != "" {
		details = append(details, response.FieldError{Field: "avatar_url", Reason: reason})
	}
	return details
}
