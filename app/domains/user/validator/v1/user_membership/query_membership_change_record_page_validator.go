package usermembership

import (
	"errors"
	"fmt"
	"log"

	userv1controller "go-service-template/app/domains/user/controller/v1"
	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
	"go-service-template/internal/response"
	commonvalidator "go-service-template/internal/validator"

	"github.com/gin-gonic/gin"
)

// QueryMembershipChangeRecordPageValidator 绑定并校验本人的会员权益历史分页请求。
type QueryMembershipChangeRecordPageValidator struct {
	// controller 复用路由创建的控制器，不保存请求 DTO 或分页位置。
	controller *userv1controller.UserMembershipController
}

// NewQueryMembershipChangeRecordPageValidator 保存控制器引用，不执行查询。
func NewQueryMembershipChangeRecordPageValidator(controller *userv1controller.UserMembershipController) *QueryMembershipChangeRecordPageValidator {
	return &QueryMembershipChangeRecordPageValidator{controller: controller}
}

// QueryMembershipChangeRecordPage 先准备默认每页数量，再绑定、校验并调用 Controller。
// 显式零值覆盖默认值后参与校验；不在校验后补默认值或改写翻页令牌。
func (v *QueryMembershipChangeRecordPageValidator) QueryMembershipChangeRecordPage(ctx *gin.Context) {
	request := usermembershipdto.QueryMembershipChangeRecordPageRequest{
		PageSize: userservice.DefaultMembershipChangeRecordPageSize,
	}
	if err := commonvalidator.Bind(ctx, &request); err != nil {
		response.Fail(ctx, response.InvalidArgument(err.Error()))
		return
	}

	details, err := validateQueryMembershipChangeRecordPageRequest(&request)
	if err != nil {
		log.Printf("Validate QueryMembershipChangeRecordPage request failed: error=%v", err)
		response.Fail(ctx, response.ErrorInfo{Code: response.CodeInternalError, Message: err.Error()})
		return
	}
	if len(details) > 0 {
		response.Fail(ctx, response.InvalidArgument(details[0].Error(), details...))
		return
	}
	v.controller.QueryMembershipChangeRecordPage(ctx, &request)
}

// validateQueryMembershipChangeRecordPageRequest 按用户、每页数量、令牌的顺序汇总输入问题。
// 令牌中的用户绑定依赖 user_id 已合法，不把令牌视为身份或权限证明。
// 已知输入问题返回字段明细；无效规则或未知错误原样返回并丢弃已有明细。
func validateQueryMembershipChangeRecordPageRequest(request *usermembershipdto.QueryMembershipChangeRecordPageRequest) ([]response.FieldError, error) {
	var details []response.FieldError
	userID, userIDErr := scalar.ParsePositiveInt64(request.UserID)
	if userIDErr != nil {
		if errors.Is(userIDErr, check.ErrInvalidRule) || !errors.Is(userIDErr, check.ErrInvalidValue) {
			return nil, userIDErr
		}
		details = append(details, response.FieldError{Field: "user_id", Reason: "must be a positive 64-bit integer"})
	}
	if err := scalar.IntInRange(request.PageSize, userservice.MinMembershipChangeRecordPageSize, userservice.MaxMembershipChangeRecordPageSize); err != nil {
		if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
			return nil, err
		}
		details = append(details, response.FieldError{
			Field:  "page_size",
			Reason: fmt.Sprintf("must be between %d and %d", userservice.MinMembershipChangeRecordPageSize, userservice.MaxMembershipChangeRecordPageSize),
		})
	}
	if userIDErr == nil {
		if _, err := models.ParseMembershipChangeRecordPageToken(request.PageToken, userID); err != nil {
			if errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
				return nil, err
			}
			details = append(details, response.FieldError{Field: "page_token", Reason: err.Error()})
		}
	}
	return details, nil
}
