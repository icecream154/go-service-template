package v1

import (
	"errors"
	"fmt"
	"log"
	"math"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
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
	request := userv1dto.QueryUserProfilePageRequest{
		PageNumber: userservice.DefaultUserProfilePageNumber,
		PageSize:   userservice.DefaultUserProfilePageSize,
	}
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}
	details, err := validateQueryUserProfilePageRequest(&request)
	if err != nil {
		log.Printf("Validate QueryUserProfilePage request failed: error=%v", err)
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
	v.controller.QueryUserProfilePage(ctx, &request)
}

// validateQueryUserProfilePageRequest 组合用例分页边界、领域昵称规则和接口布尔文本检查。
// 按字段顺序汇总问题，每个字段最多一条；不改写输入，也不查询业务状态。
// 已知输入问题返回字段明细；无效规则或未知错误立即返回，丢弃已有明细。
func validateQueryUserProfilePageRequest(request *userv1dto.QueryUserProfilePageRequest) ([]response.FieldError, error) {
	var details []response.FieldError
	pageSizeErr := scalar.IntInRange(request.PageSize, userservice.MinUserProfilePageSize, userservice.MaxUserProfilePageSize)
	if pageSizeErr != nil && (errors.Is(pageSizeErr, check.ErrInvalidRule) || !errors.Is(pageSizeErr, check.ErrInvalidValue)) {
		return nil, pageSizeErr
	}
	if err := scalar.IntAtLeast(request.PageNumber, userservice.MinUserProfilePageNumber); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{Field: "page_number", Reason: "must be a positive integer"})
	} else if pageSizeErr == nil && request.PageNumber-1 > math.MaxInt/request.PageSize {
		// 只在每页数量有效时计算偏移，避免非法零值触发除零。
		details = append(details, response.FieldError{Field: "page_number", Reason: "page offset exceeds the supported range"})
	}
	if pageSizeErr != nil {
		details = append(details, response.FieldError{
			Field:  "page_size",
			Reason: fmt.Sprintf("must be between %d and %d", userservice.MinUserProfilePageSize, userservice.MaxUserProfilePageSize),
		})
	}
	if request.UserName != "" {
		if err := models.ValidateUserName(request.UserName); err != nil {
			if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
				return nil, err
			}
			details = append(details, response.FieldError{
				Field:  "user_name",
				Reason: err.Error(),
			})
		}
	}
	if request.IsEnabled != nil {
		if _, err := scalar.ParseBoolText(*request.IsEnabled); err != nil {
			if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
				return nil, err
			}
			details = append(details, response.FieldError{
				Field:  "is_enabled",
				Reason: "must be true or false",
			})
		}
	}
	return details, nil
}
