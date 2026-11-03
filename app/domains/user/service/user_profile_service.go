// Package service 实现用户相关的业务用例。
package service

import (
	"errors"

	"go-service-template/app/domains/user/models"
	appruntime "go-service-template/internal/runtime"
)

// UserProfileService 提供用户资料操作，使用领域模型表达查询结果。
// 结构体不保存仓储或本次查询的状态。
type UserProfileService struct{}

// NewUserProfileService 创建无状态的用户资料服务，不执行查询。
func NewUserProfileService() *UserProfileService {
	return &UserProfileService{}
}

// QueryUserProfileByUserID 按内部用户 ID 查询用户资料领域模型。
// userID 必须大于 0，非法参数直接返回错误，不调用仓储。
// 查询成功时返回完整资料，保留账户身份、启用状态、版本和时间，不额外过滤停用用户。
// 查询未命中或失败时返回 nil 和仓储的原始错误，包括 gorm.ErrRecordNotFound。
func (s *UserProfileService) QueryUserProfileByUserID(userID int64) (*models.UserProfile, error) {
	if userID <= 0 {
		return nil, errors.New("user ID must be positive")
	}

	// 从已初始化的 Runtime 取得普通连接仓储，本方法不创建或加入事务。
	profile, err := appruntime.Current().Repositories.UserProfile.GetProfileByID(userID)
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
