package usermembership

import (
	"errors"
	"log"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// QueryUserMembershipByUserIDValidator 绑定并校验按用户 ID 查询本人会员权益的请求。
type QueryUserMembershipByUserIDValidator struct {
	// controller 复用路由创建的控制器，不保存单次请求状态。
	controller *userv1controller.UserMembershipController
}

// NewQueryUserMembershipByUserIDValidator 保存控制器引用，不执行绑定或查询。
func NewQueryUserMembershipByUserIDValidator(controller *userv1controller.UserMembershipController) *QueryUserMembershipByUserIDValidator {
	return &QueryUserMembershipByUserIDValidator{controller: controller}
}

// QueryUserMembershipByUserID 依次绑定、校验请求，全部通过后才调用 Controller。
func (v *QueryUserMembershipByUserIDValidator) QueryUserMembershipByUserID(ctx *gin.Context) {
	var request usermembershipdto.QueryUserMembershipByUserIDRequest
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}

	details, err := validateQueryUserMembershipByUserIDRequest(&request)
	if err != nil {
		log.Printf("Validate QueryUserMembershipByUserID request failed: error=%v", err)
		response.Fail(ctx, response.ErrorInfo{Code: response.CodeInternalError, Message: err.Error()})
		return
	}
	if len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument(details[0].Error(), details...))
		return
	}
	v.controller.QueryUserMembershipByUserID(ctx, &request)
}

// validateQueryUserMembershipByUserIDRequest 校验正 int64 文本，保留原值。
// 身份与目标的关系由 Controller、Service 保证；这里不查询用户或会员是否存在。
func validateQueryUserMembershipByUserIDRequest(request *usermembershipdto.QueryUserMembershipByUserIDRequest) ([]response.FieldError, error) {
	var details []response.FieldError
	if _, err := scalar.ParsePositiveInt64(request.UserID); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{Field: "user_id", Reason: "must be a positive 64-bit integer"})
	}
	return details, nil
}
