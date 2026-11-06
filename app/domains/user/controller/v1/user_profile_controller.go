// Package v1 实现用户 V1 接口的 HTTP 请求处理。
package v1

import (
	"errors"
	"strconv"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
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
	// codeUserProfilePermissionDenied 表示无权修改目标资料，固定归属 PERMISSION_DENIED。
	codeUserProfilePermissionDenied response.BusinessErrorCode = "USER_PROFILE_PERMISSION_DENIED"
)

// UserProfileController 处理用户资料请求，并将领域模型转换为响应 DTO。
// 结构体不保存 Service 引用或单次请求状态。
type UserProfileController struct{}

// NewUserProfileController 创建无状态的用户资料控制器，不执行查询。
func NewUserProfileController() *UserProfileController {
	return &UserProfileController{}
}

// QueryUserProfileByUserID 接收 Validator 已绑定和校验的请求，查询并构造用户资料响应。
// user_id 的格式及范围已校验，本方法将其十进制文本转换为 Service 使用的内部标识。
// 查询成功返回 200 和用户资料；未找到返回 404，其他查询错误记录日志并返回 500。
func (c *UserProfileController) QueryUserProfileByUserID(ctx *gin.Context, request *userprofiledto.QueryUserProfileByUserIDRequest) {
	// Validator 已按相同十进制规则确认格式、int64 范围和正数要求，此处允许忽略转换错误。
	userID, _ := strconv.ParseInt(request.UserID, 10, 64)
	// 使用默认用户资料服务，查询得到的领域模型仅保存在本次请求的局部变量中。
	profile, err := composition.Current().UserProfile.QueryUserProfileByUserID(userservice.QueryUserProfileByUserIDParams{
		UserID: userID,
	})
	if err != nil {
		failUserProfileOperation(ctx, err)
		return
	}

	result := userprofiledto.QueryUserProfileByUserIDResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// CreateUser 使用首次自注册的外部身份创建用户，内部 ID、初始状态和版本由 Service 决定。
// Request DTO 由领域 Validator 绑定后传入，本方法不再读取或解析 HTTP 请求体。
// 当前接口不接入外部身份认证，不要求已有 operator_user_id。
func (c *UserProfileController) CreateUser(ctx *gin.Context, request *userprofiledto.CreateUserRequest) {
	profile, err := composition.Current().UserProfile.CreateUser(userservice.CreateUserParams{
		AccountSource: request.AccountSource, ExternalAccountID: request.ExternalAccountID,
		UserName: request.UserName, AvatarURL: request.AvatarURL,
	})
	if err != nil {
		failUserProfileOperation(ctx, err)
		return
	}
	result := userprofiledto.CreateUserResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// UpdateUserProfile 使用 Validator 已校验的完整资料及版本调用 Service。
// 更新头像必须是有效的非空地址，以非 nil 指针传入 Service，不表达清空操作。
// 发起方权限、用户状态和实际版本冲突继续由 Service 判断。
func (c *UserProfileController) UpdateUserProfile(ctx *gin.Context, request *userprofiledto.UpdateUserProfileRequest) {
	profile, err := composition.Current().UserProfile.UpdateUserProfile(userservice.UpdateUserProfileParams{
		OperatorUserID: request.OperatorUserID, UserID: request.UserID, ExpectedVersion: *request.ExpectedVersion,
		UserName: request.UserName, AvatarURL: &request.AvatarURL,
	})
	if err != nil {
		failUserProfileOperation(ctx, err)
		return
	}
	result := userprofiledto.UpdateUserProfileResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// BatchQueryUserProfiles 批量读取明确的一组用户资料，返回已找到的资料和缺失 ID。
// 数量和 ID 格式由 Validator 校验；Service 对重复 ID 去重并保留首次出现的顺序。
func (c *UserProfileController) BatchQueryUserProfiles(ctx *gin.Context, request *userprofiledto.BatchQueryUserProfilesRequest) {
	userIDs := make([]int64, 0, len(request.UserIDList))
	for _, value := range request.UserIDList {
		// Validator 已按相同规则确认正 int64 文本，这里构造 Service 参数，允许忽略转换错误。
		userID, _ := strconv.ParseInt(value, 10, 64)
		userIDs = append(userIDs, userID)
	}
	serviceResult, err := composition.Current().UserProfile.BatchQueryUserProfiles(userservice.BatchQueryUserProfilesParams{
		UserIDs: userIDs,
	})
	if err != nil {
		failUserProfileOperation(ctx, err)
		return
	}
	missing := make([]string, 0, len(serviceResult.MissingUserIDs))
	for _, userID := range serviceResult.MissingUserIDs {
		missing = append(missing, strconv.FormatInt(userID, 10))
	}
	result := userprofiledto.BatchQueryUserProfilesResponse{
		UserProfileList: userProfileDTOList(serviceResult.UserProfiles), MissingUserIDList: missing,
	}
	response.Success(ctx, result)
}

// QueryUserProfilePage 使用页码分页读取资料，支持昵称精确匹配和启用状态筛选。
// 默认值和输入校验由 Validator 完成，本方法只映射 Service 参数。
func (c *UserProfileController) QueryUserProfilePage(ctx *gin.Context, request *userprofiledto.QueryUserProfilePageRequest) {
	var isEnabled *bool
	if request.IsEnabled != nil {
		value := *request.IsEnabled == "true"
		isEnabled = &value
	}
	serviceResult, err := composition.Current().UserProfile.QueryUserProfilePage(userservice.QueryUserProfilePageParams{
		PageNumber: request.PageNumber, PageSize: request.PageSize,
		UserName: request.UserName, IsEnabled: isEnabled,
	})
	if err != nil {
		failUserProfileOperation(ctx, err)
		return
	}
	result := userprofiledto.QueryUserProfilePageResponse{
		UserProfileList: userProfileDTOList(serviceResult.UserProfiles), PageNumber: request.PageNumber,
		PageSize: request.PageSize, TotalCount: serviceResult.TotalCount,
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
// Service 解释可确定的业务失败；其原样返回的未知错误和技术故障由默认分支归为内部错误。
func mapUserProfileErrorCodes(err error) (response.ErrorCode, response.BusinessErrorCode) {
	switch {
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
	case errors.Is(err, userservice.ErrUserProfilePermissionDenied):
		return response.CodePermissionDenied, codeUserProfilePermissionDenied
	case errors.Is(err, check.ErrInvalidValue):
		return response.CodeInvalidArgument, ""
	default:
		return response.CodeInternalError, ""
	}
}

// failUserProfileOperation 原样采用 Service 错误的 Error()，不补造字段明细。
// Service 已在故障发生处记录原始原因，这里只分类并输出响应。
func failUserProfileOperation(ctx *gin.Context, err error) {
	code, businessCode := mapUserProfileErrorCodes(err)
	response.Fail(ctx, response.ErrorInfo{
		Code: code, BusinessCode: businessCode, Message: err.Error(),
	})
}
