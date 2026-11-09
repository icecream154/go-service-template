// Package v1 实现用户 V1 接口的 HTTP 请求处理。
package v1

import (
	"errors"
	"log"
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
	"go-service-template/internal/response"

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
	// 1. 将 Validator 已确认格式、int64 范围和正数要求的文本转换为内部标识，允许忽略转换错误。
	userID, _ := strconv.ParseInt(request.UserID, 10, 64)
	// 2. 从可信 Principal 取得成员标识；身份不满足时输出原有错误响应，不进入 Service。
	operatorUserID, err := httpauthz.RequireMemberUserID(ctx)
	if err != nil {
		// 2.1. 身份故障由 Controller 统一映射，响应输出后立即结束本次业务方法。
		failUserProfileOperation(ctx, err)
		return
	}
	// 3. 本用例只允许查询本人；公共身份转换不决定业务目标的访问范围。
	if operatorUserID != userID {
		// 3.1. 拒绝查询他人资料，保持现有权限错误和响应契约。
		failUserProfileOperation(ctx, errUserProfileAccessDenied)
		return
	}
	// 4. 将内部 Request Context 作为 Service 首参，保留 TraceId、操作作用域及已有执行控制。
	// 查询得到的领域模型仅保存在本次请求中，不将 Context 或结果存入共享 Controller。
	profile, err := composition.Current().UserProfile.QueryUserProfileByUserID(ctx.Request.Context(), userservice.QueryUserProfileByUserIDParams{
		OperatorUserID: operatorUserID, UserID: userID,
	})
	if err != nil {
		// 4.1. Service 返回错误时沿用现有分类和响应，不在此替换错误文案。
		failUserProfileOperation(ctx, err)
		return
	}

	// 5. 将领域模型转换为当前版本 DTO，继续使用公共成功响应组件。
	result := userprofiledto.QueryUserProfileByUserIDResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// QueryUserProfilePage 使用页码分页读取资料，支持昵称精确匹配和启用状态筛选。
// 仅允许已认证的 service/admin 调用；默认值和输入校验由 Validator 完成。
func (c *UserProfileController) QueryUserProfilePage(ctx *gin.Context, request *userprofiledto.QueryUserProfilePageRequest) {
	// 1. 要求可信的 service/admin 身份，未满足接口调用资格时不发起分页查询。
	if _, err := httpauthz.RequirePrincipal(ctx, authn.PrincipalKindService, identity.ServiceNamespaceAdmin); err != nil {
		// 1.1. 身份失败沿用统一错误响应，输出后立即返回。
		failUserProfileOperation(ctx, err)
		return
	}
	// 2. 将已校验的可选布尔文本转换为 Service 参数；未提供时保留 nil 的筛选语义。
	var isEnabled *bool
	if request.IsEnabled != nil {
		// 2.1. Validator 已确认合法布尔文本，这里只执行无歧义映射，不重新校验或补默认值。
		value := *request.IsEnabled == "true"
		isEnabled = &value
	}
	// 3. 传递当前内部 Request Context，分页参数继续通过显式 Params 提供。
	serviceResult, err := composition.Current().UserProfile.QueryUserProfilePage(ctx.Request.Context(), userservice.QueryUserProfilePageParams{
		PageNumber: request.PageNumber, PageSize: request.PageSize,
		UserName: request.UserName, IsEnabled: isEnabled,
	})
	if err != nil {
		// 3.1. 查询失败保持 Service 原有错误分类，交给公共响应路径处理。
		failUserProfileOperation(ctx, err)
		return
	}
	// 4. 将领域模型集合与分页统计映射为响应 DTO，不改变列表、页码或总数语义。
	result := userprofiledto.QueryUserProfilePageResponse{
		UserProfileList: userProfileDTOList(serviceResult.UserProfiles), PageNumber: request.PageNumber,
		PageSize: request.PageSize, TotalCount: serviceResult.TotalCount,
	}
	response.Success(ctx, result)
}

// CreateUser 允许匿名或已认证调用，创建成功后为本次生成的内部用户 ID 签发 user/member 凭证。
// 无效凭证由公共认证入口拒绝；已有身份不改变首次创建语义，也不决定新用户的 ID。
// Request DTO 由领域 Validator 绑定后传入，本方法不再读取或解析 HTTP 请求体。
// 外部账户字段仅为调用方声明，不核验外部身份，也不据此为已有用户补发凭证。
// 签发失败返回 500，但不会回滚已经完成的创建；仅在创建和签发均成功时输出成功响应。
func (c *UserProfileController) CreateUser(ctx *gin.Context, request *userprofiledto.CreateUserRequest) {
	// 1. 本接口的成功响应包含敏感凭证，要求 HTTP 缓存不保存响应。
	ctx.Header("Cache-Control", "no-store")
	// 2. 将当前内部 Request Context 作为 Service 首参，业务字段仍由 Request DTO 显式映射。
	profile, err := composition.Current().UserProfile.CreateUser(ctx.Request.Context(), userservice.CreateUserParams{
		AccountSource: request.AccountSource, ExternalAccountID: request.ExternalAccountID,
		UserName: request.UserName, AvatarURL: request.AvatarURL,
	})
	if err != nil {
		// 2.1. 创建失败立即返回原有错误响应，重复注册等情况不会继续签发凭证。
		failUserProfileOperation(ctx, err)
		return
	}
	// 3. 身份类别和命名空间由服务端固定，主体只能取本次成功创建的内部 ID。
	principal := authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: identity.UserNamespaceMember,
		SubjectID: strconv.FormatInt(profile.ID, 10),
	}
	// 4. 沿用本次请求的 Context。外层响应等待中间件已隔离取消，超过 10 秒等待时间
	// 仍会继续内部处理；晚到的凭证响应由外层丢弃，不表示创建已回滚。
	issued, err := authnjwt.Current().Issue(ctx.Request.Context(), principal)
	if err != nil {
		// 4.1. 签发发生在 Controller，故障也在此处记录；不打印凭证或完整响应 DTO。
		// 创建已经完成，保留原有返回错误的语义，不在这里声称回滚。
		log.Printf("Issue user credential failed: user_id=%d error=%v", profile.ID, err)
		failUserProfileOperation(ctx, err)
		return
	}
	// 5. 组合已创建的资料和签发结果，仅在这两个阶段均成功后输出成功响应。
	result := userprofiledto.CreateUserResponse{
		UserProfile: userProfileDTO(profile),
		AccessToken: issued.Credential.Value,
		TokenType:   issued.Credential.Scheme,
		ExpiresAt:   issued.ExpiresAt,
	}
	response.Success(ctx, result)
}

// GenerateUserAvatar 仅允许已认证的 user/member 生成头像，不修改用户资料。
// Prompt 由 Validator 校验，Service 先经 Qwen 优化再生成图片；调用沿用内部 Request Context。
// 成功只返回头像地址。
func (c *UserProfileController) GenerateUserAvatar(ctx *gin.Context, request *userprofiledto.GenerateUserAvatarRequest) {
	// 1. 要求可信的 user/member 身份，未满足接口调用资格时不执行提示词优化或图片生成。
	if _, err := httpauthz.RequireMemberUserID(ctx); err != nil {
		// 1.1. 通过原有错误映射输出响应，随后立即结束本次 Controller 方法。
		failUserProfileOperation(ctx, err)
		return
	}
	// 2. 继续将内部 Request Context 传给 Service，保留 TraceId 与响应等待层已隔离取消的契约。
	// 提示词仍通过显式 Params 传递，不将 DTO 或业务参数放入 Context。
	result, err := composition.Current().UserProfile.GenerateUserAvatar(ctx.Request.Context(), userservice.GenerateUserAvatarParams{
		Prompt: request.Prompt,
	})
	if err != nil {
		// 2.1. 任一远端步骤失败时沿用 Service 返回错误，不在 Controller 重新解释流程结果。
		failUserProfileOperation(ctx, err)
		return
	}
	// 3. 成功只返回本次生成的头像地址，不修改或持久化用户资料。
	response.Success(ctx, userprofiledto.GenerateUserAvatarResponse{AvatarURL: result.AvatarURL})
}

// UpdateUserProfile 使用 Validator 已校验的完整资料及版本调用 Service。
// 更新头像必须是有效的非空地址，以非 nil 指针传入 Service，不表达清空操作。
// 仅允许已认证的 user/member 修改本人，发起方 ID 来自 Principal，不接收请求自行声明。
// Service 继续独立检查本人关系、用户状态和实际版本冲突。
func (c *UserProfileController) UpdateUserProfile(ctx *gin.Context, request *userprofiledto.UpdateUserProfileRequest) {
	// 1. 从可信 Principal 取得当前成员标识，不采用请求自行声明的操作人。
	operatorUserID, err := httpauthz.RequireMemberUserID(ctx)
	if err != nil {
		// 1.1. 身份不满足时直接输出原有错误响应，不进入资料修改流程。
		failUserProfileOperation(ctx, err)
		return
	}
	// 2. 本用例只允许修改本人；发起方已由公共身份转换确定，目标仍由请求表达。
	if operatorUserID != request.UserID {
		// 2.1. 越权目标在调用 Service 前拒绝，继续沿用原有权限错误。
		failUserProfileOperation(ctx, errUserProfileAccessDenied)
		return
	}
	// 3. 以内部 Request Context 为首参传入 Service，资料、版本和可信操作人仍使用显式 Params。
	profile, err := composition.Current().UserProfile.UpdateUserProfile(ctx.Request.Context(), userservice.UpdateUserProfileParams{
		OperatorUserID: operatorUserID, UserID: request.UserID, ExpectedVersion: *request.ExpectedVersion,
		UserName: request.UserName, AvatarURL: &request.AvatarURL,
	})
	if err != nil {
		// 3.1. Service 继续独立检查业务状态和并发版本，失败时使用既有错误响应。
		failUserProfileOperation(ctx, err)
		return
	}
	// 4. 成功后将更新后的领域模型映射为当前版本 DTO，不改变公共响应格式。
	result := userprofiledto.UpdateUserProfileResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// BatchQueryUserProfiles 批量读取明确的一组用户资料，返回已找到的资料和缺失 ID。
// 仅允许已认证的 service/admin 调用，不将服务身份解释为某个用户或要求其主体为数字。
// 数量和 ID 格式由 Validator 校验；Service 对重复 ID 去重并保留首次出现的顺序。
func (c *UserProfileController) BatchQueryUserProfiles(ctx *gin.Context, request *userprofiledto.BatchQueryUserProfilesRequest) {
	// 1. 要求可信的 service/admin 身份，不将服务主体解释为用户数字标识。
	if _, err := httpauthz.RequirePrincipal(ctx, authn.PrincipalKindService, identity.ServiceNamespaceAdmin); err != nil {
		// 1.1. 身份失败时保持现有错误分类，立即输出响应并返回。
		failUserProfileOperation(ctx, err)
		return
	}
	// 2. 将已校验的 ID 文本列表逐个转换为 Service 使用的整数，保留提交顺序和重复项。
	userIDs := make([]int64, 0, len(request.UserIDList))
	for _, value := range request.UserIDList {
		// 2.1. Validator 已确认正 int64 文本，这里只做参数映射，允许忽略转换错误。
		userID, _ := strconv.ParseInt(value, 10, 64)
		userIDs = append(userIDs, userID)
	}
	// 3. 将内部 Request Context 与显式 ID 参数一同传给 Service，由业务层负责去重及缺失项整理。
	serviceResult, err := composition.Current().UserProfile.BatchQueryUserProfiles(ctx.Request.Context(), userservice.BatchQueryUserProfilesParams{
		UserIDs: userIDs,
	})
	if err != nil {
		// 3.1. 查询失败时交给原有响应路径，不把故障当作全部用户缺失。
		failUserProfileOperation(ctx, err)
		return
	}
	// 4. 将缺失的内部数字 ID 转回接口使用的十进制文本，维持 Service 给出的顺序。
	missing := make([]string, 0, len(serviceResult.MissingUserIDs))
	for _, userID := range serviceResult.MissingUserIDs {
		// 4.1. 每个缺失项只做表示形式转换，不重新查询或补造资料。
		missing = append(missing, strconv.FormatInt(userID, 10))
	}
	// 5. 映射已找到的资料与缺失列表，继续使用公共成功响应组件。
	result := userprofiledto.BatchQueryUserProfilesResponse{
		UserProfileList: userProfileDTOList(serviceResult.UserProfiles), MissingUserIDList: missing,
	}
	response.Success(ctx, result)
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
