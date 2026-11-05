// Package v1 实现用户 V1 接口的 HTTP 请求处理。
package v1

import (
	"log"
	"strconv"

	userapierror "go-service-template/app/domains/user/apierror"
	userv1dto "go-service-template/app/domains/user/dto/v1"
	"go-service-template/app/domains/user/dto/v1/shared"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/internal/composition"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

// UserProfileController 处理用户资料请求，并将领域模型转换为响应 DTO。
// 结构体不保存 Service 引用或单次请求状态。
type UserProfileController struct{}

// NewUserProfileController 创建无状态的用户资料控制器，不执行查询。
func NewUserProfileController() *UserProfileController {
	return &UserProfileController{}
}

// QueryUserProfileByUserID 解析用户 ID、查询领域模型并构造用户资料响应。
// Query 参数 user_id 必填，按十进制解析为正 int64，缺失、格式错误、越界或非正数返回 400。
// 查询成功返回 200 和用户资料；未找到返回 404，其他查询错误记录日志并返回 500。
func (c *UserProfileController) QueryUserProfileByUserID(ctx *gin.Context) {
	// Query 参数是字符串，显式解析后填入请求 DTO；解析错误时不会继续查询。
	var request userv1dto.QueryUserProfileByUserIDRequest
	userID, err := strconv.ParseInt(ctx.Query("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		response.Fail(ctx, response.InvalidArgument("", response.FieldError{
			Field: "user_id", Reason: "must be a positive 64-bit integer",
		}))
		return
	}
	request.UserID = userID

	// 使用默认用户资料服务，查询得到的领域模型仅保存在本次请求的局部变量中。
	profile, err := composition.Current().UserProfile.QueryUserProfileByUserID(request.UserID)
	if err != nil {
		failUserProfileOperation(ctx, "Query user profile", err)
		return
	}

	result := userv1dto.QueryUserProfileByUserIDResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// CreateUser 使用首次自注册的外部身份创建用户，内部 ID、初始状态和版本由 Service 决定。
// Request DTO 由领域 Validator 绑定后传入，本方法不再读取或解析 HTTP 请求体。
// 当前接口不接入外部身份认证，不要求已有 operator_user_id。
func (c *UserProfileController) CreateUser(ctx *gin.Context, request *userv1dto.CreateUserRequest) {
	profile, err := composition.Current().UserProfile.CreateUser(userservice.CreateUserParams{
		AccountSource: request.AccountSource, ExternalAccountID: request.ExternalAccountID,
		UserName: request.UserName, AvatarURL: request.AvatarURL,
	})
	if err != nil {
		failUserProfileOperation(ctx, "Create user", err)
		return
	}
	result := userv1dto.CreateUserResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// UpdateUserProfile 完整替换昵称和头像，版本必须显式提交，头像必须显式给出值或 null。
// 发起方与目标用户使用不同字段；具体业务限制由 Service 校验。
func (c *UserProfileController) UpdateUserProfile(ctx *gin.Context) {
	var request userv1dto.UpdateUserProfileRequest
	if !decodeUserProfileJSON(ctx, &request,
		[]string{"operator_user_id", "user_id", "expected_version", "user_name", "avatar_url"}, "avatar_url") {
		return
	}
	if !requirePositiveUserID(ctx, "operator_user_id", request.OperatorUserID) ||
		!requirePositiveUserID(ctx, "user_id", request.UserID) {
		return
	}
	if request.ExpectedVersion < 0 {
		failRequestField(ctx, "expected_version", "must be a non-negative 64-bit integer")
		return
	}
	profile, err := composition.Current().UserProfile.UpdateUserProfile(userservice.UpdateUserProfileParams{
		OperatorUserID: request.OperatorUserID, UserID: request.UserID, ExpectedVersion: request.ExpectedVersion,
		UserName: request.UserName, AvatarURL: request.AvatarURL,
	})
	if err != nil {
		failUserProfileOperation(ctx, "Update user profile", err)
		return
	}
	result := userv1dto.UpdateUserProfileResponse(userProfileDTO(profile))
	response.Success(ctx, result)
}

// BatchQueryUserProfiles 批量读取明确的一组用户资料，返回已找到的资料和缺失 ID。
// 单次最多接收 100 项；Service 对重复 ID 去重，并保留首次出现的顺序。
func (c *UserProfileController) BatchQueryUserProfiles(ctx *gin.Context) {
	var request userv1dto.BatchQueryUserProfilesRequest
	if !decodeUserProfileJSON(ctx, &request, []string{"user_id_list"}) {
		return
	}
	if len(request.UserIDList) < 1 || len(request.UserIDList) > userservice.MaxUserProfileBatchSize {
		failRequestField(ctx, "user_id_list", "must contain between 1 and 100 user IDs")
		return
	}
	userIDs := make([]int64, 0, len(request.UserIDList))
	for _, value := range request.UserIDList {
		userID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || userID <= 0 {
			failRequestField(ctx, "user_id_list", "each item must be a positive 64-bit integer string")
			return
		}
		userIDs = append(userIDs, userID)
	}
	profiles, missingIDs, err := composition.Current().UserProfile.BatchQueryUserProfiles(userIDs)
	if err != nil {
		failUserProfileOperation(ctx, "Batch query user profiles", err)
		return
	}
	missing := make([]string, 0, len(missingIDs))
	for _, userID := range missingIDs {
		missing = append(missing, strconv.FormatInt(userID, 10))
	}
	result := userv1dto.BatchQueryUserProfilesResponse{
		UserProfileList: userProfileDTOList(profiles), MissingUserIDList: missing,
	}
	response.Success(ctx, result)
}

// QueryUserProfilePage 使用页码分页读取资料，支持昵称精确匹配和启用状态筛选。
// 默认第 1 页、每页 20 条，按用户 ID 降序；每页最多 100 条。
func (c *UserProfileController) QueryUserProfilePage(ctx *gin.Context) {
	request := userv1dto.QueryUserProfilePageRequest{PageNumber: 1, PageSize: 20}
	if !parsePageQuery(ctx, &request) {
		return
	}
	profiles, totalCount, err := composition.Current().UserProfile.QueryUserProfilePage(userservice.QueryUserProfilePageParams{
		PageNumber: request.PageNumber, PageSize: request.PageSize,
		UserName: request.UserName, IsEnabled: request.IsEnabled,
	})
	if err != nil {
		failUserProfileOperation(ctx, "Query user profile page", err)
		return
	}
	result := userv1dto.QueryUserProfilePageResponse{
		UserProfileList: userProfileDTOList(profiles), PageNumber: request.PageNumber,
		PageSize: request.PageSize, TotalCount: totalCount,
	}
	response.Success(ctx, result)
}

// userProfileDTO 只选择接口约定的业务字段，保留头像的 nil 与空字符串语义。
func userProfileDTO(profile *models.UserProfile) shared.UserProfile {
	return shared.UserProfile{
		UserID: profile.ID, UserName: profile.UserName, AvatarURL: profile.AvatarURL,
		IsEnabled: profile.IsEnabled, Version: profile.Version,
	}
}

// userProfileDTOList 始终创建非 nil 切片，确保空结果编码为 []。
func userProfileDTOList(profiles []*models.UserProfile) []shared.UserProfile {
	result := make([]shared.UserProfile, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, userProfileDTO(profile))
	}
	return result
}

// failUserProfileOperation 复用领域接口映射，只将未预期错误的原始详情写入服务端日志。
func failUserProfileOperation(ctx *gin.Context, operation string, err error) {
	failure := userapierror.FromError(err)
	if failure.Code == response.CodeInternalError {
		log.Printf("%s failed: error=%v", operation, err)
	}
	response.Fail(ctx, failure)
}
