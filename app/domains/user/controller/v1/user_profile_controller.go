// Package v1 实现用户 V1 接口的 HTTP 请求处理。
package v1

import (
	"log"
	"strconv"

	userapierror "go-service-template/app/domains/user/apierror"
	userv1dto "go-service-template/app/domains/user/dto/v1"
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
		// 领域统一映射通用码、可选业务码和对外文案，Controller 不直接识别持久化错误。
		failure := userapierror.FromError(err)
		if failure.Code == response.CodeInternalError {
			// 未预期错误的具体原因只写入服务端日志，不拼入响应。
			log.Printf("Query user profile failed: user_id=%d error=%v", request.UserID, err)
		}
		response.Fail(ctx, failure)
		return
	}

	// 从领域模型选取响应字段，头像指针保留 nil 与空字符串的区别。
	// UserID 的 JSON 字符串格式由响应 DTO 的字段标签确定。
	result := userv1dto.QueryUserProfileByUserIDResponse{
		UserID:    profile.ID,
		UserName:  profile.UserName,
		AvatarURL: profile.AvatarURL,
	}
	response.Success(ctx, result)
}
