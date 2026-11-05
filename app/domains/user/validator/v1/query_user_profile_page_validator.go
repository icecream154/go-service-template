package v1

import (
	"math"
	"strings"
	"unicode/utf8"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// QueryUserProfilePageValidator 绑定并校验用户资料分页查询请求。
type QueryUserProfilePageValidator struct {
	// controller 复用路由创建的控制器，不保存单次请求的 DTO。
	controller *userv1controller.UserProfileController
}

// NewQueryUserProfilePageValidator 保存控制器引用，不执行绑定或业务查询。
func NewQueryUserProfilePageValidator(controller *userv1controller.UserProfileController) *QueryUserProfilePageValidator {
	return &QueryUserProfilePageValidator{controller: controller}
}

// QueryUserProfilePage 依次绑定、校验请求，全部通过后才调用 Controller。
// 绑定前提供省略参数时的默认页码与每页数量，显式零值仍交给校验函数拒绝。
func (v *QueryUserProfilePageValidator) QueryUserProfilePage(ctx *gin.Context) {
	request := userv1dto.QueryUserProfilePageRequest{PageNumber: 1, PageSize: 20}
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(""))
		return
	}
	if details := validateQueryUserProfilePageRequest(&request); len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument("", details...))
		return
	}
	v.controller.QueryUserProfilePage(ctx, &request)
}

// validateQueryUserProfilePageRequest 在函数内部完成分页、昵称和启用筛选值的校验。
// 按字段顺序汇总问题，每个字段最多一条；不改写输入，也不查询业务状态。
func validateQueryUserProfilePageRequest(request *userv1dto.QueryUserProfilePageRequest) []response.FieldError {
	details := make([]response.FieldError, 0, 4)
	if request.PageNumber <= 0 {
		details = append(details, response.FieldError{Field: "page_number", Reason: "must be a positive integer"})
	} else if request.PageSize > 0 && request.PageSize <= 100 && request.PageNumber-1 > math.MaxInt/request.PageSize {
		// 只在每页数量有效时计算偏移，避免非法零值触发除零。
		details = append(details, response.FieldError{Field: "page_number", Reason: "page offset exceeds the supported range"})
	}
	if request.PageSize <= 0 || request.PageSize > 100 {
		details = append(details, response.FieldError{Field: "page_size", Reason: "must be between 1 and 100"})
	}
	if request.UserName != "" {
		var reason string
		switch {
		case !utf8.ValidString(request.UserName):
			reason = "must be valid UTF-8 text"
		case strings.TrimSpace(request.UserName) == "":
			reason = "must not be blank"
		case utf8.RuneCountInString(request.UserName) > 64:
			reason = "must contain at most 64 Unicode code points"
		}
		if reason != "" {
			details = append(details, response.FieldError{Field: "user_name", Reason: reason})
		}
	}
	if request.IsEnabled != nil && *request.IsEnabled != "true" && *request.IsEnabled != "false" {
		details = append(details, response.FieldError{Field: "is_enabled", Reason: "must be true or false"})
	}
	return details
}
