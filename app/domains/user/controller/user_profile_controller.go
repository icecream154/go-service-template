// Package controller 实现用户相关的 HTTP 请求处理。
package controller

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"go-service-template/app/domains/user/dto"
	"go-service-template/app/domains/user/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// UserProfileController 处理用户资料请求，并将领域模型转换为响应 DTO。
type UserProfileController struct {
	// userProfiles 提供按内部 ID 查询用户资料的业务操作。
	userProfiles *service.UserProfileService
}

// NewUserProfileController 使用已有的用户资料服务创建控制器。
// userProfiles 应为已初始化的有效服务，构造函数只保存依赖。
func NewUserProfileController(userProfiles *service.UserProfileService) *UserProfileController {
	return &UserProfileController{userProfiles: userProfiles}
}

// QueryUserProfileByUserID 处理 GET /users/:user_id。
// 路径中的 user_id 按十进制解析为正 int64，格式错误、越界或非正数返回 400。
// 查询成功返回 200 和用户资料；未找到返回 404，其他查询错误记录日志并返回 500。
func (c *UserProfileController) QueryUserProfileByUserID(ctx *gin.Context) {
	// 路径参数是字符串，显式解析后填入请求 DTO；解析错误时不会继续查询。
	var request dto.QueryUserProfileByUserIDRequest
	userID, err := strconv.ParseInt(ctx.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "user_id must be a positive 64-bit integer"})
		return
	}
	request.UserID = userID

	profile, err := c.userProfiles.QueryUserProfileByUserID(request.UserID)
	if err != nil {
		// 未找到使用固定响应文案；其他错误的具体原因只写入服务端日志。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "user profile not found"})
			return
		}
		log.Printf("Query user profile failed: user_id=%d error=%v", request.UserID, err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// 从领域模型选取响应字段，头像指针保留 nil 与空字符串的区别。
	// UserID 的 JSON 字符串格式由响应 DTO 的字段标签确定。
	response := dto.QueryUserProfileByUserIDResponse{
		UserID:    profile.ID,
		UserName:  profile.UserName,
		AvatarURL: profile.AvatarURL,
	}
	ctx.JSON(http.StatusOK, response)
}
