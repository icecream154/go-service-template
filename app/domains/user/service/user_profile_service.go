// Package service 实现用户相关的业务用例。
package service

import (
	"errors"

	"go-service-template/app/domains/user/models"
	"go-service-template/app/domains/user/repository"
)

// UserProfileService 提供用户资料操作，使用领域模型表达查询结果。
type UserProfileService struct {
	// userProfiles 用于读取用户资料，查询条件由各业务方法传入。
	userProfiles *repository.UserProfileRepository
}

// NewUserProfileService 使用已有的用户资料仓储创建服务。
// userProfiles 应为已初始化的有效仓储；构造函数只保存依赖，不执行查询。
func NewUserProfileService(userProfiles *repository.UserProfileRepository) *UserProfileService {
	return &UserProfileService{userProfiles: userProfiles}
}

// QueryUserProfileByUserID 按内部用户 ID 查询用户资料领域模型。
// userID 必须大于 0，非法参数直接返回错误，不调用仓储。
// 查询成功时返回完整资料，保留账户身份、启用状态、版本和时间，不额外过滤停用用户。
// 查询未命中或失败时返回 nil 和仓储的原始错误，包括 gorm.ErrRecordNotFound。
func (s *UserProfileService) QueryUserProfileByUserID(userID int64) (*models.UserProfile, error) {
	if userID <= 0 {
		return nil, errors.New("user ID must be positive")
	}

	profile, err := s.userProfiles.GetProfileByID(userID)
	if err != nil {
		return nil, err
	}

	// 将持久化数据逐项还原为领域模型，保留原始字段值。
	// 返回独立对象，领域模型与本次查询得到的表结构分别持有各自的数据。
	userProfile := &models.UserProfile{
		ID:                profile.ID,
		AccountSource:     profile.AccountSource,
		ExternalAccountID: profile.ExternalAccountID,
		UserName:          profile.UserName,
		IsEnabled:         profile.IsEnabled,
		Version:           profile.Version,
		CreatedAt:         profile.CreatedAt,
		UpdatedAt:         profile.UpdatedAt,
	}
	// SQL NULL 保持为 nil；有效值复制为独立字符串指针，空字符串同样保留。
	if profile.AvatarURL.Valid {
		avatarURL := profile.AvatarURL.String
		userProfile.AvatarURL = &avatarURL
	}
	return userProfile, nil
}
