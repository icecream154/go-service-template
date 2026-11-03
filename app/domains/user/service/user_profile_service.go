// Package service 实现用户相关的业务用例。
package service

import (
	"errors"

	"go-service-template/app/domains/user/dto"
	"go-service-template/app/domains/user/repository"
)

// UserProfileService 提供用户资料查询，并将查询结果转换为响应 DTO。
type UserProfileService struct {
	// userProfiles 用于读取用户资料，查询条件由各业务方法传入。
	userProfiles *repository.UserProfileRepository
}

// NewUserProfileService 使用已有的用户资料仓储创建服务。
// userProfiles 应为已初始化的有效仓储；构造函数只保存依赖，不执行查询。
func NewUserProfileService(userProfiles *repository.UserProfileRepository) *UserProfileService {
	return &UserProfileService{userProfiles: userProfiles}
}

// QueryUserProfileByUserID 按内部用户 ID 查询基本资料。
// UserID 必须大于 0，非法参数直接返回错误，不调用仓储。
// 查询成功时返回用户 ID、昵称和头像；沿用仓储结果，不额外过滤停用用户。
// 查询未命中或失败时返回 nil 和仓储的原始错误，包括 gorm.ErrRecordNotFound。
func (s *UserProfileService) QueryUserProfileByUserID(request dto.QueryUserProfileByUserIDRequest) (*dto.QueryUserProfileByUserIDResponse, error) {
	if request.UserID <= 0 {
		return nil, errors.New("user ID must be positive")
	}

	profile, err := s.userProfiles.GetProfileByID(request.UserID)
	if err != nil {
		return nil, err
	}

	// 显式填充响应字段，ID 保持 int64，昵称保留查询到的原值。
	response := &dto.QueryUserProfileByUserIDResponse{
		UserID:   profile.ID,
		UserName: profile.UserName,
	}
	// SQL NULL 保持为 nil；有效值复制为独立字符串指针，空字符串同样保留。
	if profile.AvatarURL.Valid {
		avatarURL := profile.AvatarURL.String
		response.AvatarURL = &avatarURL
	}
	return response, nil
}
