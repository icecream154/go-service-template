package v1

import (
	"errors"
	"strconv"

	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	httpauthz "go-service-template/app/http/authz"
	"go-service-template/internal/check"
	"go-service-template/internal/composition"
	"go-service-template/internal/logger"
	"go-service-template/internal/response"
	"go-service-template/internal/tracing"

	"github.com/gin-gonic/gin"
)

const (
	// codeUserMembershipUserNotFound 表示目标用户不存在，固定归属 NOT_FOUND。
	codeUserMembershipUserNotFound response.BusinessErrorCode = "USER_PROFILE_NOT_FOUND"
	// codeUserMembershipPermissionDenied 表示无权查询目标会员权益，固定归属 PERMISSION_DENIED。
	codeUserMembershipPermissionDenied response.BusinessErrorCode = "USER_MEMBERSHIP_PERMISSION_DENIED"
)

// errUserMembershipAccessDenied 表示可信的用户身份不满足本人访问条件。
var errUserMembershipAccessDenied = errors.New("user membership operation is not permitted")

// UserMembershipController 查询本人当前会员权益和变更历史，不执行开通或续费。
// 结构体不保存 Service、请求 Context 或单次查询状态。
type UserMembershipController struct{}

// NewUserMembershipController 创建无状态的会员控制器，不执行查询。
func NewUserMembershipController() *UserMembershipController {
	return &UserMembershipController{}
}

// QueryUserMembershipByUserID 读取本人会员状态，未开通和已过期均返回成功结果。
// 请求字段由 Validator 校验；可信发起方只能来自认证后的 user/member 身份。
func (c *UserMembershipController) QueryUserMembershipByUserID(ctx *gin.Context, request *usermembershipdto.QueryUserMembershipByUserIDRequest) {
	// 1. 为当前 Controller 操作建立子 Span，日志不包含用户标识或权益正文。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "User membership query request received")
	// 2. 请求 ID 已完成正 int64 文本校验；发起方仍须独立从可信身份取得。
	userID, _ := strconv.ParseInt(request.UserID, 10, 64)
	operatorUserID, err := httpauthz.RequireMemberUserID(ctx)
	if err != nil {
		span.Fail("User membership query access denied")
		lg.Warn(requestCtx, "User membership query request rejected", logger.Fields{Error: "User membership query access denied"})
		failUserMembershipOperation(ctx, err)
		return
	}
	if operatorUserID != userID {
		span.Fail("Querying another user's membership is not permitted")
		lg.Warn(requestCtx, "User membership query request rejected", logger.Fields{Error: "Querying another user's membership is not permitted"})
		failUserMembershipOperation(ctx, errUserMembershipAccessDenied)
		return
	}
	// 3. Service 确认用户存在并判断权益状态，Controller 不自行读取数据库或计算到期。
	result, err := composition.Current().UserMembership.QueryUserMembershipByUserID(requestCtx, userservice.QueryUserMembershipByUserIDParams{
		OperatorUserID: operatorUserID, UserID: userID,
	})
	if err != nil {
		span.Fail("User membership query service call failed")
		failUserMembershipOperation(ctx, err)
		return
	}
	// 4. 保留未开通时的 nil 权益；状态和查询时间直接采用同一次 Service 结果。
	response.Success(ctx, usermembershipdto.QueryUserMembershipByUserIDResponse{
		UserID: result.UserID, MembershipStatus: result.MembershipStatus,
		QueriedAt: result.QueriedAt, Membership: userMembershipDTO(result.Membership),
	})
	lg.Info(requestCtx, "User membership query request handled")
}

// QueryMembershipChangeRecordPage 按游标读取本人的权益变更历史。
// 每次请求都检查可信身份及目标用户，分页令牌不能代替访问授权。
func (c *UserMembershipController) QueryMembershipChangeRecordPage(ctx *gin.Context, request *usermembershipdto.QueryMembershipChangeRecordPageRequest) {
	// 1. 保存派生的请求 Context，覆盖正常返回、错误及 panic 的 Span 收尾。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "Membership change record page request received")
	// 2. 只接受普通用户查询本人；不记录令牌、用户标识或历史正文。
	userID, _ := strconv.ParseInt(request.UserID, 10, 64)
	operatorUserID, err := httpauthz.RequireMemberUserID(ctx)
	if err != nil {
		span.Fail("Membership change record page access denied")
		lg.Warn(requestCtx, "Membership change record page request rejected", logger.Fields{Error: "Membership change record page access denied"})
		failUserMembershipOperation(ctx, err)
		return
	}
	if operatorUserID != userID {
		span.Fail("Querying another user's membership history is not permitted")
		lg.Warn(requestCtx, "Membership change record page request rejected", logger.Fields{Error: "Querying another user's membership history is not permitted"})
		failUserMembershipOperation(ctx, errUserMembershipAccessDenied)
		return
	}
	// 3. 传递显式分页参数，由 Service 再次保障用例约束并组织历史与下一页令牌。
	result, err := composition.Current().UserMembership.QueryMembershipChangeRecordPage(requestCtx, userservice.QueryMembershipChangeRecordPageParams{
		OperatorUserID: operatorUserID, UserID: userID, PageSize: request.PageSize, PageToken: request.PageToken,
	})
	if err != nil {
		span.Fail("Membership change record page service call failed")
		failUserMembershipOperation(ctx, err)
		return
	}
	// 4. 显式转换快照字段，空页保持 []，最后一页的令牌保持 null。
	response.Success(ctx, usermembershipdto.QueryMembershipChangeRecordPageResponse{
		MembershipChangeRecordList: membershipChangeRecordDTOList(result.Records),
		PageSize:                   request.PageSize, HasMore: result.HasMore, NextPageToken: result.NextPageToken,
	})
	lg.Info(requestCtx, "Membership change record page request handled")
}

// userMembershipDTO 仅输出当前权益的业务字段，未开通时保持 null。
func userMembershipDTO(membership *models.UserMembership) *usermembershipdto.UserMembership {
	if membership == nil {
		return nil
	}
	return &usermembershipdto.UserMembership{
		MembershipID: membership.ID, MembershipLevel: membership.MembershipLevel,
		EffectiveAt: membership.EffectiveAt, ExpiredAt: membership.ExpiredAt,
	}
}

// membershipChangeRecordDTOList 保留历史的前后快照及首次开通的 null，不关联订单信息。
func membershipChangeRecordDTOList(records []*models.MembershipChangeRecord) []usermembershipdto.MembershipChangeRecord {
	result := make([]usermembershipdto.MembershipChangeRecord, 0, len(records))
	for _, record := range records {
		result = append(result, usermembershipdto.MembershipChangeRecord{
			RecordID: record.ID, MembershipID: record.MembershipID, OrderID: record.OrderID,
			ChangeType: record.ChangeType, BeforeMembershipLevel: record.BeforeMembershipLevel,
			BeforeEffectiveAt: record.BeforeEffectiveAt, BeforeExpiredAt: record.BeforeExpiredAt,
			AfterMembershipLevel: record.AfterMembershipLevel, AfterEffectiveAt: record.AfterEffectiveAt,
			AfterExpiredAt: record.AfterExpiredAt, CreatedAt: record.CreatedAt,
		})
	}
	return result
}

// mapUserMembershipErrorCodes 区分认证、权限、用户缺失和技术故障，未开通不属于错误。
func mapUserMembershipErrorCodes(err error) (response.ErrorCode, response.BusinessErrorCode) {
	switch {
	case errors.Is(err, httpauthz.ErrAuthenticationRequired), errors.Is(err, httpauthz.ErrInvalidMemberIdentity):
		return response.CodeUnauthenticated, ""
	case errors.Is(err, check.ErrInvalidRule):
		return response.CodeInternalError, ""
	case errors.Is(err, userservice.ErrUserProfileNotFound):
		return response.CodeNotFound, codeUserMembershipUserNotFound
	case errors.Is(err, httpauthz.ErrPrincipalNotAllowed), errors.Is(err, errUserMembershipAccessDenied),
		errors.Is(err, userservice.ErrUserMembershipPermissionDenied):
		return response.CodePermissionDenied, codeUserMembershipPermissionDenied
	case errors.Is(err, check.ErrInvalidValue):
		return response.CodeInvalidArgument, ""
	default:
		return response.CodeInternalError, ""
	}
}

// failUserMembershipOperation 原样传递错误消息，按认证原因设置必要的 Bearer 挑战头。
func failUserMembershipOperation(ctx *gin.Context, err error) {
	code, businessCode := mapUserMembershipErrorCodes(err)
	if code == response.CodeUnauthenticated {
		challenge := `Bearer realm="api"`
		if errors.Is(err, httpauthz.ErrInvalidMemberIdentity) {
			challenge += `, error="invalid_token"`
		}
		ctx.Header("WWW-Authenticate", challenge)
	}
	response.Fail(ctx, response.ErrorInfo{Code: code, BusinessCode: businessCode, Message: err.Error()})
}
