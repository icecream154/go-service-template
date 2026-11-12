package service

import (
	"context"
	"errors"
	"time"

	"go-service-template/app/domains/user/models"
	"go-service-template/app/domains/user/tables"
	"go-service-template/internal/check/scalar"
	"go-service-template/internal/database/sqlvalue"
	"go-service-template/internal/logger"
	appruntime "go-service-template/internal/runtime"
	"go-service-template/internal/tracing"

	"gorm.io/gorm"
)

// errors: shared

// ErrUserMembershipPermissionDenied 表示已确认的发起方无权读取目标用户的会员数据。
var ErrUserMembershipPermissionDenied = errors.New("user membership operation is not permitted")

// errors: QueryMembershipChangeRecordPage

// errInvalidMembershipChangeRecordState 表示持久化历史不能形成合法的当前页及翻页边界。
// 与调用方传入非法令牌不同，该错误归属内部数据异常。
var errInvalidMembershipChangeRecordState = errors.New("stored membership change record state is invalid")

// UserMembershipService 查询当前会员权益及订单触发的权益历史。
// 实例无状态，不保存请求 Context、仓储或事务；写入仍由订单履约流程负责。
type UserMembershipService struct{}

// NewUserMembershipService 创建可共享的会员查询服务，不执行查询。
func NewUserMembershipService() *UserMembershipService {
	return &UserMembershipService{}
}

// use case: QueryUserMembershipByUserID

// QueryUserMembershipByUserIDParams 表示已确认身份的本人会员查询输入。
type QueryUserMembershipByUserIDParams struct {
	// OperatorUserID 来自可信调用入口，必须为正数且与 UserID 相同。
	OperatorUserID int64
	// UserID 是目标用户的内部标识。
	UserID int64
}

// QueryUserMembershipByUserIDResult 表达同一查询时刻的会员状态和可空权益。
type QueryUserMembershipByUserIDResult struct {
	// UserID 是实际查询的用户标识。
	UserID int64
	// MembershipStatus 是 QueriedAt 时刻的派生状态，不写入数据库。
	MembershipStatus string
	// QueriedAt 是读取权益后取用的同一个 UTC 毫秒查询时刻。
	QueriedAt time.Time
	// Membership 为 nil 表示用户存在但从未开通；过期记录仍原样保留。
	Membership *models.UserMembership
}

// validateQueryUserMembershipByUserIDParams 独立保障有效标识和本人访问关系。
func validateQueryUserMembershipByUserIDParams(params QueryUserMembershipByUserIDParams) error {
	return validateMembershipQueryUserIDs(params.OperatorUserID, params.UserID)
}

// QueryUserMembershipByUserID 查询本人权益，不以用户启用状态或是否有效会员限制读取。
// 用户不存在时返回 ErrUserProfileNotFound；会员行不存在时返回正常的 NOT_OPENED 结果。
func (s *UserMembershipService) QueryUserMembershipByUserID(ctx context.Context, params QueryUserMembershipByUserIDParams) (*QueryUserMembershipByUserIDResult, error) {
	// 1. 建立本次业务操作作用域和 Service Span，保留调用入口的执行控制。
	if ctx != nil {
		ctx = logger.WithCurrentOperation(ctx)
	}
	ctx, span := tracing.Current().StartService(ctx, logger.Operation(ctx))
	defer span.End()
	lg := logger.Current()
	// 2. 在访问仓储前保证输入和本人关系，不将请求声明当作可信身份。
	if err := validateQueryUserMembershipByUserIDParams(params); err != nil {
		span.Fail("User membership query parameters or access are invalid")
		lg.Warn(ctx, "User membership query rejected", logger.Fields{Error: "User membership query parameters or access are invalid"})
		return nil, err
	}
	// 3. 区分用户不存在与用户尚未开通，停用资料仍可查询。
	runtime := appruntime.Current()
	if _, err := runtime.Repositories.UserProfile.GetProfileByID(params.UserID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			span.Fail("User profile was not found")
			lg.Warn(ctx, "User membership query rejected", logger.Fields{Error: "User profile was not found"})
			return nil, ErrUserProfileNotFound
		}
		span.Fail("User profile query execution failed")
		lg.Error(ctx, "User membership query failed", logger.Fields{Error: "User profile query execution failed"})
		return nil, err
	}
	// 4. 仓储不按到期时间过滤，明确未命中才映射为可空权益，技术故障原样返回。
	row, err := runtime.Repositories.UserMembership.GetMembershipByUserID(params.UserID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		span.Fail("User membership query execution failed")
		lg.Error(ctx, "User membership query failed", logger.Fields{Error: "User membership query execution failed"})
		return nil, err
	}
	var membership *models.UserMembership
	if err == nil {
		membership = membershipModel(row)
	}
	// 5. 只取一次 UTC 毫秒时刻，状态计算及响应共用该值，不回写或清理过期数据。
	queriedAt := runtime.DB.User.NowFunc().UTC().Truncate(time.Millisecond)
	result, err := userMembershipQueryResult(params.UserID, membership, queriedAt)
	if err != nil {
		span.Fail("Stored user membership state is invalid")
		lg.Error(ctx, "User membership query failed", logger.Fields{Error: "Stored user membership state is invalid"})
		return nil, err
	}
	lg.Info(ctx, "User membership queried")
	return result, nil
}

// use case: QueryMembershipChangeRecordPage

// QueryMembershipChangeRecordPageParams 表达本人历史列表的数量及不透明翻页边界。
type QueryMembershipChangeRecordPageParams struct {
	// OperatorUserID 来自可信调用入口，必须与 UserID 相同。
	OperatorUserID int64
	// UserID 是需要读取历史的用户内部标识。
	UserID int64
	// PageSize 由入口准备默认值，显式非法值不在 Service 内修复。
	PageSize int
	// PageToken 为空表示第一页，每次查询仍独立检查本人关系。
	PageToken string
}

// QueryMembershipChangeRecordPageResult 保存裁剪后的当前页和下一页边界。
// 不承诺跨页固定快照，不查询总数；Records 为空时仍是非 nil 切片。
type QueryMembershipChangeRecordPageResult struct {
	// Records 保留数据库给定的创建时间及 ID 降序，不包含额外探测行。
	Records []*models.MembershipChangeRecord
	// HasMore 表示本次读取发现了当前页之后的至少一条记录。
	HasMore bool
	// NextPageToken 有下一页时包含当前页末条记录的位置，否则为 nil。
	NextPageToken *string
}

const (
	// MinMembershipChangeRecordPageSize 表示历史分页数量的最小值。
	MinMembershipChangeRecordPageSize = 1
	// DefaultMembershipChangeRecordPageSize 供入口准备未显式提供的分页数量。
	DefaultMembershipChangeRecordPageSize = 20
	// MaxMembershipChangeRecordPageSize 限制单次读取数量，不包含额外探测行。
	MaxMembershipChangeRecordPageSize = 100
)

// validateQueryMembershipChangeRecordPageParams 校验身份关系、数量和当前用户的翻页边界。
// 返回解析后的边界以供查询复用，不重复解码或依赖 HTTP Validator 已经执行。
func validateQueryMembershipChangeRecordPageParams(params QueryMembershipChangeRecordPageParams) (*models.MembershipChangeRecordCursor, error) {
	if err := validateMembershipQueryUserIDs(params.OperatorUserID, params.UserID); err != nil {
		return nil, err
	}
	if err := scalar.IntInRange(params.PageSize, MinMembershipChangeRecordPageSize, MaxMembershipChangeRecordPageSize); err != nil {
		return nil, err
	}
	return models.ParseMembershipChangeRecordPageToken(params.PageToken, params.UserID)
}

// QueryMembershipChangeRecordPage 按 created_at、id 降序读取本人的权益变更历史。
// 用户存在但没有历史时返回空页；只使用用户库仓储，不跨库关联订单信息。
func (s *UserMembershipService) QueryMembershipChangeRecordPage(ctx context.Context, params QueryMembershipChangeRecordPageParams) (*QueryMembershipChangeRecordPageResult, error) {
	// 1. 建立独立业务作用域和 Service Span，私有流程复用当前 Context。
	if ctx != nil {
		ctx = logger.WithCurrentOperation(ctx)
	}
	ctx, span := tracing.Current().StartService(ctx, logger.Operation(ctx))
	defer span.End()
	lg := logger.Current()
	// 2. 在数据库查询前验证本人关系，令牌只描述位置，不授予访问其他用户的权限。
	cursor, err := validateQueryMembershipChangeRecordPageParams(params)
	if err != nil {
		span.Fail("Membership change record query parameters or access are invalid")
		lg.Warn(ctx, "Membership change record query rejected", logger.Fields{Error: "Membership change record query parameters or access are invalid"})
		return nil, err
	}
	// 3. 用户必须真实存在；停用不影响读取已保存的权益历史。
	repositories := appruntime.Current().Repositories
	if _, err := repositories.UserProfile.GetProfileByID(params.UserID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			span.Fail("User profile was not found")
			lg.Warn(ctx, "Membership change record query rejected", logger.Fields{Error: "User profile was not found"})
			return nil, ErrUserProfileNotFound
		}
		span.Fail("User profile query execution failed")
		lg.Error(ctx, "Membership change record query failed", logger.Fields{Error: "User profile query execution failed"})
		return nil, err
	}
	// 4. 将可空游标拆为仓储查询参数，多取一行判断下一页，保持数量边界由业务层掌握。
	var afterCreatedAt *time.Time
	var afterID int64
	if cursor != nil {
		afterCreatedAt, afterID = &cursor.CreatedAt, cursor.ID
	}
	rows, err := repositories.MembershipChangeRecord.QueryRecordPage(params.UserID, afterCreatedAt, afterID, params.PageSize+1)
	if err != nil {
		span.Fail("Membership change record query execution failed")
		lg.Error(ctx, "Membership change record query failed", logger.Fields{Error: "Membership change record query execution failed"})
		return nil, err
	}
	// 5. 下一页边界来自本页实际返回的末条记录，额外探测行保留给下一页读取。
	result, err := membershipChangeRecordPageResult(rows, params.UserID, params.PageSize)
	if err != nil {
		span.Fail("Stored membership change record state is invalid")
		lg.Error(ctx, "Membership change record query failed", logger.Fields{Error: "Stored membership change record state is invalid"})
		return nil, err
	}
	lg.Info(ctx, "Membership change records queried")
	return result, nil
}

// shared

// validateMembershipQueryUserIDs 保障两个读取用例共有的正数标识及本人关系。
func validateMembershipQueryUserIDs(operatorUserID, userID int64) error {
	if err := scalar.PositiveInt64(operatorUserID); err != nil {
		return err
	}
	if err := scalar.PositiveInt64(userID); err != nil {
		return err
	}
	if operatorUserID != userID {
		return ErrUserMembershipPermissionDenied
	}
	return nil
}

// userMembershipQueryResult 组合可空权益和同一时刻的派生状态，不改写原模型。
func userMembershipQueryResult(userID int64, membership *models.UserMembership, queriedAt time.Time) (*QueryUserMembershipByUserIDResult, error) {
	if membership != nil && membership.UserID != userID {
		return nil, models.ErrInvalidUserMembershipState
	}
	status, err := membership.StatusAt(queriedAt)
	if err != nil {
		return nil, err
	}
	return &QueryUserMembershipByUserIDResult{
		UserID: userID, MembershipStatus: status, QueriedAt: queriedAt, Membership: membership,
	}, nil
}

// membershipChangeRecordPageResult 裁剪探测行，并把实际返回末条记录编码为下一页位置。
func membershipChangeRecordPageResult(rows []tables.MembershipChangeRecord, userID int64, pageSize int) (*QueryMembershipChangeRecordPageResult, error) {
	result := &QueryMembershipChangeRecordPageResult{
		Records: make([]*models.MembershipChangeRecord, 0, min(len(rows), pageSize)),
		HasMore: len(rows) > pageSize,
	}
	if result.HasMore {
		rows = rows[:pageSize]
	}
	for i := range rows {
		result.Records = append(result.Records, membershipChangeRecordModel(&rows[i]))
	}
	if result.HasMore {
		last := rows[len(rows)-1]
		if last.UserID != userID {
			return nil, errInvalidMembershipChangeRecordState
		}
		// 只有内部数据合法时才编码；数据库内容异常不冒充客户端令牌错误。
		cursor := models.MembershipChangeRecordCursor{UserID: last.UserID, CreatedAt: last.CreatedAt, ID: last.ID}
		token, err := models.EncodeMembershipChangeRecordPageToken(cursor)
		if err != nil {
			return nil, errInvalidMembershipChangeRecordState
		}
		result.NextPageToken = &token
	}
	return result, nil
}

// membershipModel 还原当前权益领域模型，保留过期记录和数据库时间精度。
func membershipModel(row *tables.UserMembership) *models.UserMembership {
	return &models.UserMembership{
		ID: row.ID, UserID: row.UserID, MembershipLevel: row.MembershipLevel,
		EffectiveAt: row.EffectiveAt.UTC(), ExpiredAt: row.ExpiredAt.UTC(),
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

// membershipChangeRecordModel 保留历史的可空前值，时间以 UTC 输出，不改写历史快照。
func membershipChangeRecordModel(row *tables.MembershipChangeRecord) *models.MembershipChangeRecord {
	beforeEffectiveAt := sqlvalue.FromNullTime(row.BeforeEffectiveAt)
	if beforeEffectiveAt != nil {
		*beforeEffectiveAt = beforeEffectiveAt.UTC()
	}
	beforeExpiredAt := sqlvalue.FromNullTime(row.BeforeExpiredAt)
	if beforeExpiredAt != nil {
		*beforeExpiredAt = beforeExpiredAt.UTC()
	}
	return &models.MembershipChangeRecord{
		ID: row.ID, UserID: row.UserID, MembershipID: row.MembershipID, OrderID: row.OrderID,
		ChangeType: row.ChangeType, BeforeMembershipLevel: sqlvalue.FromNullInt32(row.BeforeMembershipLevel),
		BeforeEffectiveAt: beforeEffectiveAt, BeforeExpiredAt: beforeExpiredAt,
		AfterMembershipLevel: row.AfterMembershipLevel, AfterEffectiveAt: row.AfterEffectiveAt.UTC(),
		AfterExpiredAt: row.AfterExpiredAt.UTC(), CreatedAt: row.CreatedAt.UTC(),
	}
}
