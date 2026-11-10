// Package v1 实现用户 V1 接口的 HTTP 请求处理。
package v1

import (
	"errors"
	"strconv"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	httpauthz "go-service-template/app/http/authz"
	"go-service-template/app/identity"
	"go-service-template/internal/authn"
	authnjwt "go-service-template/internal/authn/jwt"
	"go-service-template/internal/check"
	"go-service-template/internal/composition"
	"go-service-template/internal/logger"
	"go-service-template/internal/response"
	"go-service-template/internal/tracing"

	"github.com/gin-gonic/gin"
)

const (
	// codeUserProfileNotFound 表示目标资料不存在，固定归属 NOT_FOUND。
	codeUserProfileNotFound response.BusinessErrorCode = "USER_PROFILE_NOT_FOUND"
	// codeUserProfileAccountAlreadyExists 表示注册账户已存在，固定归属 CONFLICT。
	codeUserProfileAccountAlreadyExists response.BusinessErrorCode = "USER_ACCOUNT_ALREADY_EXISTS"
	// codeUserProfileDisabled 表示停用用户不能修改资料，固定归属 CONFLICT。
	codeUserProfileDisabled response.BusinessErrorCode = "USER_DISABLED"
	// codeUserProfileVersionConflict 表示资料版本已过期，固定归属 CONFLICT。
	codeUserProfileVersionConflict response.BusinessErrorCode = "USER_PROFILE_VERSION_CONFLICT"
	// codeUserProfilePermissionDenied 表示无权执行用户资料操作，固定归属 PERMISSION_DENIED。
	codeUserProfilePermissionDenied response.BusinessErrorCode = "USER_PROFILE_PERMISSION_DENIED"
)

// errUserProfileAccessDenied 表示已确认的用户身份不满足本接口的本人访问条件。
// 通用身份缺失、范围不符及 member 标识错误使用公共 authz 包的错误值。
var errUserProfileAccessDenied = errors.New("user profile operation is not permitted")

// UserProfileController 处理用户资料请求，并将领域模型转换为响应 DTO。
// 结构体不保存 Service 引用或单次请求状态。
type UserProfileController struct{}

// NewUserProfileController 创建无状态的用户资料控制器，不执行查询。
func NewUserProfileController() *UserProfileController {
	return &UserProfileController{}
}

// QueryUserProfileByUserID 接收 Validator 已绑定和校验的请求，查询并构造用户资料响应。
// user_id 的格式及范围已校验，本方法将其十进制文本转换为 Service 使用的内部标识。
// 仅允许已认证的 user/member 查询本人，身份或权限不满足时不调用 Service。
// 查询成功返回 200 和用户资料；未找到返回 404，其他查询错误记录日志并返回 500。
func (c *UserProfileController) QueryUserProfileByUserID(ctx *gin.Context, request *userprofiledto.QueryUserProfileByUserIDRequest) {
	// 1. 为当前 Controller 方法建立操作作用域和子 Span，保存回请求并直接记录请求到达。
	// 派生 Context 保留 TraceId 和内部执行控制，不记录用户标识或请求正文。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	// 1.1. 复用入口自动识别的 operation，直接登记 Span 收尾，覆盖正常返回、提前失败和 panic。
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "User profile query request received")
	// 2. 将 Validator 已确认格式、int64 范围和正数要求的文本转换为内部标识，允许忽略转换错误。
	userID, _ := strconv.ParseInt(request.UserID, 10, 64)
	// 3. 从可信 Principal 取得成员标识；身份不满足时记录固定拒绝原因，不进入 Service。
	operatorUserID, err := httpauthz.RequireMemberUserID(ctx)
	if err != nil {
		// 3.1. 不记录凭证、主体文本或原始错误，响应继续沿用现有分类和文案。
		span.Fail("User profile query access denied")
		lg.Warn(requestCtx, "User profile query request rejected", logger.Fields{
			Error: "User profile query access denied",
		})
		failUserProfileOperation(ctx, err)
		return
	}
	// 4. 本用例只允许查询本人；公共身份转换不决定业务目标的访问范围。
	if operatorUserID != userID {
		// 4.1. 在越权分支直接记录受控说明，保持现有权限错误和响应契约。
		span.Fail("Querying another user's profile is not permitted")
		lg.Warn(requestCtx, "User profile query request rejected", logger.Fields{
			Error: "Querying another user's profile is not permitted",
		})
		failUserProfileOperation(ctx, errUserProfileAccessDenied)
		return
	}
	// 5. 将当前 Controller Context 传给 Service，Service 在自己的派生节点建立操作作用域。
	// 查询得到的领域模型仅保存在本次请求中，不将 Context 或结果存入共享 Controller。
	profile, err := composition.Current().UserProfile.QueryUserProfileByUserID(requestCtx, userservice.QueryUserProfileByUserIDParams{
		OperatorUserID: operatorUserID, UserID: userID,
	})
	if err != nil {
		// 5.1. Service 负责对应失败日志，此处仅沿用现有分类和响应，不重复输出故障摘要。
		span.Fail("User profile query service call failed")
		failUserProfileOperation(ctx, err)
		return
	}

	// 6. 将领域模型转换为当前版本 DTO 并处理响应，随后记录本方法已完成处理。
	// HTTP 对外状态由公共入口记录，不把方法返回等同于客户端已收到正文。
	result := userProfileDTO(profile)
	response.Success(ctx, result)
	lg.Info(requestCtx, "User profile query request handled")
}

// QueryUserProfilePage 使用页码分页读取资料，支持昵称精确匹配和启用状态筛选。
// 仅允许已认证的 service/admin 调用；默认值和输入校验由 Validator 完成。
func (c *UserProfileController) QueryUserProfilePage(ctx *gin.Context, request *userprofiledto.QueryUserProfilePageRequest) {
	// 1. 建立当前 Controller 操作作用域和子 Span，保留已有 TraceId 和执行控制。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	// 1.1. Span 名称复用自动识别结果，直接 defer 结束本次方法执行，不改变原有日志顺序。
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "User profile page request received")
	// 2. 要求可信的 service/admin 身份，未满足接口调用资格时不发起分页查询。
	if _, err := httpauthz.RequirePrincipal(ctx, authn.PrincipalKindService, identity.ServiceNamespaceAdmin); err != nil {
		// 2.1. 记录固定拒绝原因，不打印凭证或主体文本，响应仍采用原有错误。
		span.Fail("User profile page access denied")
		lg.Warn(requestCtx, "User profile page request rejected", logger.Fields{
			Error: "User profile page access denied",
		})
		failUserProfileOperation(ctx, err)
		return
	}
	// 3. 将已校验的可选布尔文本转换为 Service 参数；未提供时保留 nil 的筛选语义。
	var isEnabled *bool
	if request.IsEnabled != nil {
		// 3.1. Validator 已确认合法布尔文本，这里只执行无歧义映射，不重新校验或补默认值。
		value := *request.IsEnabled == "true"
		isEnabled = &value
	}
	// 4. 传递当前 Controller Context，分页参数继续通过显式 Params 提供，不进入日志字段。
	serviceResult, err := composition.Current().UserProfile.QueryUserProfilePage(requestCtx, userservice.QueryUserProfilePageParams{
		PageNumber: request.PageNumber, PageSize: request.PageSize,
		UserName: request.UserName, IsEnabled: isEnabled,
	})
	if err != nil {
		// 4.1. Service 已记录对应失败，此处保持原有错误分类和响应，避免重复故障摘要。
		span.Fail("User profile page service call failed")
		failUserProfileOperation(ctx, err)
		return
	}
	// 5. 将领域模型集合与分页统计映射为响应 DTO，完成处理后记录步骤，不输出资料正文。
	result := userprofiledto.QueryUserProfilePageResponse{
		UserProfileList: userProfileDTOList(serviceResult.UserProfiles), PageNumber: request.PageNumber,
		PageSize: request.PageSize, TotalCount: serviceResult.TotalCount,
	}
	response.Success(ctx, result)
	lg.Info(requestCtx, "User profile page request handled")
}

// CreateUser 允许匿名或已认证调用，创建成功后为本次生成的内部用户 ID 签发 user/member 凭证。
// 无效凭证由公共认证入口拒绝；已有身份不改变首次创建语义，也不决定新用户的 ID。
// Request DTO 由领域 Validator 绑定后传入，本方法不再读取或解析 HTTP 请求体。
// 外部账户字段仅为调用方声明，不核验外部身份，也不据此为已有用户补发凭证。
// 签发失败返回 500，但不会回滚已经完成的创建；仅在创建和签发均成功时输出成功响应。
func (c *UserProfileController) CreateUser(ctx *gin.Context, request *userprofiledto.CreateUserRequest) {
	// 1. 建立当前 Controller 操作作用域和子 Span，不输出账户输入或用户资料。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	// 1.1. 以当前具名方法的自动识别结果创建 Span，正常、提前失败及 panic 均在方法退出时结束。
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "User creation request received")
	// 2. 本接口的成功响应包含敏感凭证，要求 HTTP 缓存不保存响应。
	ctx.Header("Cache-Control", "no-store")
	// 3. 将当前 Controller Context 作为 Service 首参，业务字段仍由 Request DTO 显式映射。
	profile, err := composition.Current().UserProfile.CreateUser(requestCtx, userservice.CreateUserParams{
		AccountSource: request.AccountSource, ExternalAccountID: request.ExternalAccountID,
		UserName: request.UserName, AvatarURL: request.AvatarURL,
	})
	if err != nil {
		// 3.1. 创建失败由 Service 记录，此处输出原有错误响应，重复注册等情况不继续签发凭证。
		span.Fail("User creation service call failed")
		failUserProfileOperation(ctx, err)
		return
	}
	// 4. 身份类别和命名空间由服务端固定，主体只能取本次成功创建的内部 ID。
	principal := authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: identity.UserNamespaceMember,
		SubjectID: strconv.FormatInt(profile.ID, 10),
	}
	// 5. 将当前 Controller Context 传给签发组件，保留同一 TraceId 和操作作用域。
	// 外层响应等待中间件已隔离取消，超过 10 秒等待时间
	// 仍会继续内部处理；晚到的凭证响应由外层丢弃，不表示创建已回滚。
	issued, err := authnjwt.Current().Issue(requestCtx, principal)
	if err != nil {
		// 5.1. 签发发生在 Controller，直接记录固定失败阶段，不打印用户 ID、凭证或原始错误。
		// 创建已经完成，保留原有返回错误的语义，不在这里声称回滚。
		span.Fail("User credential issuance failed")
		lg.Error(requestCtx, "Issue user credential failed", logger.Fields{
			Error: "User credential issuance failed",
		})
		failUserProfileOperation(ctx, err)
		return
	}
	// 6. 组合已创建的资料和签发结果，处理成功响应后只记录阶段完成，不记录敏感响应内容。
	result := userprofiledto.CreateUserResponse{
		UserProfile: userProfileDTO(profile),
		AccessToken: issued.Credential.Value,
		TokenType:   issued.Credential.Scheme,
		ExpiresAt:   issued.ExpiresAt,
	}
	response.Success(ctx, result)
	lg.Info(requestCtx, "User creation request handled")
}

// GenerateUserAvatar 仅允许已认证的 user/member 生成头像，不修改用户资料。
// Prompt 由 Validator 校验，Service 先经 Qwen 优化再生成图片；调用沿用内部 Request Context。
// 成功只返回头像地址，流程日志在实际执行位置记录，HTTP 状态和耗时由公共入口统计。
func (c *UserProfileController) GenerateUserAvatar(ctx *gin.Context, request *userprofiledto.GenerateUserAvatarRequest) {
	// 1. 从当前内部 Request Context 派生 Controller 操作作用域与子 Span，交回请求供后续处理使用。
	// WithCurrentOperation 自动识别具名方法，保留 TraceId 和响应等待层已经应用的取消隔离。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	// 1.1. 读取已有 operation 创建本次 Controller Span，直接 defer 收尾，不为业务日志维护结束状态。
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	// 2. 取得共享 Logger 并记录请求已到达当前 Controller，不记录提示词、凭证或完整请求。
	lg := logger.Current()
	lg.Info(requestCtx, "User avatar request received")
	// 3. 要求可信的 user/member 身份，未满足接口调用资格时不执行提示词优化或图片生成。
	if _, err := httpauthz.RequireMemberUserID(ctx); err != nil {
		// 3.1. 在拒绝位置直接记录固定原因，日志故障由 Logger 内部处理，响应继续使用原有错误映射。
		span.Fail("User avatar request access denied")
		lg.Warn(requestCtx, "User avatar request rejected", logger.Fields{
			Error: "User avatar request access denied",
		})
		failUserProfileOperation(ctx, err)
		return
	}
	// 4. 将 Controller 派生 Context 传给 Service；Service 在自己的派生节点建立操作作用域。
	// 提示词仍通过显式 Params 传递，不将 DTO 或业务参数放入 Context。
	result, err := composition.Current().UserProfile.GenerateUserAvatar(requestCtx, userservice.GenerateUserAvatarParams{
		Prompt: request.Prompt,
	})
	if err != nil {
		// 4.1. Service 已记录具体失败阶段，此处只沿用原有错误响应，不重复输出故障摘要。
		span.Fail("User avatar service call failed")
		failUserProfileOperation(ctx, err)
		return
	}
	// 5. 返回头像地址并记录 Controller 已完成结果处理，不修改用户资料或记录地址与完整响应。
	// 真实对外响应仍由 HTTP 入口观察，方法处理完成不保证客户端已收到正文。
	response.Success(ctx, userprofiledto.GenerateUserAvatarResponse{AvatarURL: result.AvatarURL})
	lg.Info(requestCtx, "User avatar request handled")
}

// UpdateUserProfile 使用 Validator 已校验的完整资料及版本调用 Service。
// 更新头像必须是有效的非空地址，以非 nil 指针传入 Service，不表达清空操作。
// 仅允许已认证的 user/member 修改本人，发起方 ID 来自 Principal，不接收请求自行声明。
// Service 继续独立检查本人关系、用户状态和实际版本冲突。
func (c *UserProfileController) UpdateUserProfile(ctx *gin.Context, request *userprofiledto.UpdateUserProfileRequest) {
	// 1. 建立当前 Controller 操作作用域和子 Span，沿用 TraceId 和内部执行控制。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	// 1.1. Span 使用自动识别的稳定方法名，直接 defer 结束，派生 Context 继续传给授权及 Service。
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "User profile update request received")
	// 2. 从可信 Principal 取得当前成员标识，不采用请求自行声明的操作人。
	operatorUserID, err := httpauthz.RequireMemberUserID(ctx)
	if err != nil {
		// 2.1. 在拒绝位置记录固定说明，原有身份错误继续用于响应，不输出凭证或主体信息。
		span.Fail("User profile update access denied")
		lg.Warn(requestCtx, "User profile update request rejected", logger.Fields{
			Error: "User profile update access denied",
		})
		failUserProfileOperation(ctx, err)
		return
	}
	// 3. 本用例只允许修改本人；发起方已由公共身份转换确定，目标仍由请求表达。
	if operatorUserID != request.UserID {
		// 3.1. 越权目标在调用 Service 前拒绝，日志不记录实际用户标识，响应沿用原有权限错误。
		span.Fail("Updating another user's profile is not permitted")
		lg.Warn(requestCtx, "User profile update request rejected", logger.Fields{
			Error: "Updating another user's profile is not permitted",
		})
		failUserProfileOperation(ctx, errUserProfileAccessDenied)
		return
	}
	// 4. 将当前 Controller Context 传入 Service，资料、版本和可信操作人仍使用显式 Params。
	profile, err := composition.Current().UserProfile.UpdateUserProfile(requestCtx, userservice.UpdateUserProfileParams{
		OperatorUserID: operatorUserID, UserID: request.UserID, ExpectedVersion: *request.ExpectedVersion,
		UserName: request.UserName, AvatarURL: &request.AvatarURL,
	})
	if err != nil {
		// 4.1. Service 负责业务状态检查及对应失败日志，此处仅输出既有错误响应。
		span.Fail("User profile update service call failed")
		failUserProfileOperation(ctx, err)
		return
	}
	// 5. 将更新后的领域模型映射为当前版本 DTO，响应处理后直接记录阶段完成，不输出资料正文。
	result := userProfileDTO(profile)
	response.Success(ctx, result)
	lg.Info(requestCtx, "User profile update request handled")
}

// BatchQueryUserProfiles 批量读取明确的一组用户资料，返回已找到的资料和缺失 ID。
// 仅允许已认证的 service/admin 调用，不将服务身份解释为某个用户或要求其主体为数字。
// 数量和 ID 格式由 Validator 校验；Service 对重复 ID 去重并保留首次出现的顺序。
func (c *UserProfileController) BatchQueryUserProfiles(ctx *gin.Context, request *userprofiledto.BatchQueryUserProfilesRequest) {
	// 1. 建立当前 Controller 操作作用域和子 Span，沿用已有 TraceId，不输出 ID 列表。
	requestCtx := logger.WithCurrentOperation(ctx.Request.Context())
	// 1.1. 当前方法名由日志入口自动取得，Span 仅覆盖本次 Controller 调用，不为每个用户创建节点。
	requestCtx, span := tracing.Current().StartController(requestCtx, logger.Operation(requestCtx))
	defer span.End()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	lg := logger.Current()
	lg.Info(requestCtx, "User profile batch query request received")
	// 2. 要求可信的 service/admin 身份，不将服务主体解释为用户数字标识。
	if _, err := httpauthz.RequirePrincipal(ctx, authn.PrincipalKindService, identity.ServiceNamespaceAdmin); err != nil {
		// 2.1. 身份失败直接记录固定拒绝原因，保持现有错误分类和响应。
		span.Fail("User profile batch query access denied")
		lg.Warn(requestCtx, "User profile batch query request rejected", logger.Fields{
			Error: "User profile batch query access denied",
		})
		failUserProfileOperation(ctx, err)
		return
	}
	// 3. 将已校验的 ID 文本列表逐个转换为 Service 使用的整数，保留提交顺序和重复项。
	userIDs := make([]int64, 0, len(request.UserIDList))
	for _, value := range request.UserIDList {
		// 3.1. Validator 已确认正 int64 文本，这里只做参数映射，允许忽略转换错误。
		userID, _ := strconv.ParseInt(value, 10, 64)
		userIDs = append(userIDs, userID)
	}
	// 4. 将当前 Controller Context 与显式 ID 参数一同传给 Service，由业务层负责去重及缺失项整理。
	serviceResult, err := composition.Current().UserProfile.BatchQueryUserProfiles(requestCtx, userservice.BatchQueryUserProfilesParams{
		UserIDs: userIDs,
	})
	if err != nil {
		// 4.1. 查询失败由 Service 记录，此处沿用原有响应，不把故障当作全部用户缺失。
		span.Fail("User profile batch query service call failed")
		failUserProfileOperation(ctx, err)
		return
	}
	// 5. 将缺失的内部数字 ID 转回接口使用的十进制文本，维持 Service 给出的顺序。
	missing := make([]string, 0, len(serviceResult.MissingUserIDs))
	for _, userID := range serviceResult.MissingUserIDs {
		// 5.1. 每个缺失项只做表示形式转换，不重新查询或补造资料。
		missing = append(missing, strconv.FormatInt(userID, 10))
	}
	// 6. 映射已找到的资料与缺失列表，处理成功响应后直接记录完成，不输出完整结果。
	result := userprofiledto.BatchQueryUserProfilesResponse{
		UserProfileList: userProfileDTOList(serviceResult.UserProfiles), MissingUserIDList: missing,
	}
	response.Success(ctx, result)
	lg.Info(requestCtx, "User profile batch query request handled")
}

// userProfileDTO 只选择接口约定的业务字段，保留头像的 nil 与空字符串语义。
func userProfileDTO(profile *models.UserProfile) userprofiledto.UserProfile {
	return userprofiledto.UserProfile{
		UserID: profile.ID, UserName: profile.UserName, AvatarURL: profile.AvatarURL,
		IsEnabled: profile.IsEnabled, Version: profile.Version,
	}
}

// userProfileDTOList 始终创建非 nil 切片，确保空结果编码为 []。
func userProfileDTOList(profiles []*models.UserProfile) []userprofiledto.UserProfile {
	result := make([]userprofiledto.UserProfile, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, userProfileDTO(profile))
	}
	return result
}

// mapUserProfileErrorCodes 集中选择本 Controller 的通用码和业务码，不修改错误文案。
// Service 解释可确定的业务失败；签发失败、未知错误和技术故障由默认分支归为内部错误。
func mapUserProfileErrorCodes(err error) (response.ErrorCode, response.BusinessErrorCode) {
	switch {
	case errors.Is(err, httpauthz.ErrAuthenticationRequired), errors.Is(err, httpauthz.ErrInvalidMemberIdentity):
		return response.CodeUnauthenticated, ""
	case errors.Is(err, check.ErrInvalidRule):
		return response.CodeInternalError, ""
	case errors.Is(err, userservice.ErrUserProfileNotFound):
		return response.CodeNotFound, codeUserProfileNotFound
	case errors.Is(err, userservice.ErrAccountAlreadyExists):
		return response.CodeConflict, codeUserProfileAccountAlreadyExists
	case errors.Is(err, userservice.ErrUserDisabled):
		return response.CodeConflict, codeUserProfileDisabled
	case errors.Is(err, userservice.ErrUserProfileVersionConflict):
		return response.CodeConflict, codeUserProfileVersionConflict
	case errors.Is(err, httpauthz.ErrPrincipalNotAllowed), errors.Is(err, errUserProfileAccessDenied),
		errors.Is(err, userservice.ErrUserProfilePermissionDenied):
		return response.CodePermissionDenied, codeUserProfilePermissionDenied
	case errors.Is(err, check.ErrInvalidValue):
		return response.CodeInvalidArgument, ""
	default:
		return response.CodeInternalError, ""
	}
}

// failUserProfileOperation 原样采用接收错误的 Error()，不补造字段明细。
// Service 故障在 Service 中记录，签发故障在 CreateUser 中记录；这里仅分类并输出响应。
func failUserProfileOperation(ctx *gin.Context, err error) {
	code, businessCode := mapUserProfileErrorCodes(err)
	if code == response.CodeUnauthenticated {
		challenge := `Bearer realm="api"`
		if errors.Is(err, httpauthz.ErrInvalidMemberIdentity) {
			challenge += `, error="invalid_token"`
		}
		ctx.Header("WWW-Authenticate", challenge)
	}
	response.Fail(ctx, response.ErrorInfo{
		Code: code, BusinessCode: businessCode, Message: err.Error(),
	})
}
