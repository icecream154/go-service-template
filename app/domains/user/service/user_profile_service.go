// Package service 实现用户相关的业务用例。
package service

import (
	"database/sql"
	"errors"
	"fmt"

	usererrors "go-service-template/app/domains/user/errors"
	"go-service-template/app/domains/user/models"
	"go-service-template/app/domains/user/repository"
	"go-service-template/app/domains/user/tables"
	appruntime "go-service-template/internal/runtime"
	"go-service-template/internal/snowflake"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// UserProfileService 提供用户资料操作，使用领域模型表达查询结果。
// 结构体不保存仓储或本次查询的状态。
type UserProfileService struct{}

// NewUserProfileService 创建无状态的用户资料服务，不执行查询。
func NewUserProfileService() *UserProfileService {
	return &UserProfileService{}
}

// QueryUserProfileByUserID 按内部用户 ID 查询用户资料领域模型。
// userID 必须大于 0，非法参数直接返回 ErrInvalidUserID，不调用仓储。
// 查询成功时返回完整资料，保留账户身份、启用状态、版本和时间，不额外过滤停用用户。
// 查询未命中转换为 ErrUserProfileNotFound，其他仓储错误原样返回供上层记录。
// 本层不构造 HTTP 响应，也不决定对外业务错误码。
func (s *UserProfileService) QueryUserProfileByUserID(userID int64) (*models.UserProfile, error) {
	if userID <= 0 {
		return nil, usererrors.ErrInvalidUserID
	}

	// 从已初始化的 Runtime 取得普通连接仓储，本方法不创建或加入事务。
	profile, err := appruntime.Current().Repositories.UserProfile.GetProfileByID(userID)
	if err != nil {
		// 将持久化层的未命中语义转换为领域错误，Controller 无需识别 GORM 错误。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, usererrors.ErrUserProfileNotFound
		}
		return nil, err
	}

	return profileModel(profile), nil
}

// CreateUser 按首次注册语义创建用户，初始启用、版本为 0。
// 本层只校验注册身份的格式，不核验外部身份凭证；账户唯一性最终由数据库保障。
func (s *UserProfileService) CreateUser(params CreateUserParams) (*models.UserProfile, error) {
	if err := validateCreateUser(params); err != nil {
		return nil, err
	}
	return createUser(params, appruntime.Current().Repositories.UserProfile, snowflake.NextID)
}

// createUser 按次接收仓储和发号函数，不将依赖或单次注册状态保存在 Service 上。
func createUser(params CreateUserParams, profiles *repository.UserProfileRepository, nextID func() (int64, error)) (*models.UserProfile, error) {
	id, err := nextID()
	if err != nil {
		return nil, fmt.Errorf("generate user ID: %w", err)
	}
	if id <= 0 {
		return nil, errors.New("generated user ID must be positive")
	}
	profile := &tables.UserProfile{
		ID: id, AccountSource: params.AccountSource, ExternalAccountID: params.ExternalAccountID,
		UserName: params.UserName, AvatarURL: avatarValue(params.AvatarURL), IsEnabled: true, Version: 0,
	}
	if err := profiles.CreateProfile(profile); err != nil {
		var driverError *mysqldriver.MySQLError
		if errors.Is(err, gorm.ErrDuplicatedKey) || (errors.As(err, &driverError) && driverError.Number == 1062) {
			// 唯一键冲突也可能来自主键；按注册账户复查，避免把发号冲突误报为账号已存在。
			if _, accountErr := profiles.GetProfileByAccount(params.AccountSource, params.ExternalAccountID); accountErr == nil {
				return nil, usererrors.ErrAccountAlreadyExists
			} else if !errors.Is(accountErr, gorm.ErrRecordNotFound) {
				return nil, errors.Join(err, fmt.Errorf("check account after duplicate key: %w", accountErr))
			}
		}
		return nil, err
	}
	return profileModel(profile), nil
}

// UpdateUserProfile 完整替换本人的昵称、头像，校验用户状态和调用方读取到的版本。
// 成功返回本次修改后的资料；事务内使用行锁，防止状态判断和返回结果受到并发修改影响。
func (s *UserProfileService) UpdateUserProfile(params UpdateUserProfileParams) (*models.UserProfile, error) {
	if err := validateUpdateUserProfile(params); err != nil {
		return nil, err
	}
	return updateUserProfile(params, appruntime.Current().DB.User)
}

func updateUserProfile(params UpdateUserProfileParams, db *gorm.DB) (*models.UserProfile, error) {
	var result *models.UserProfile
	err := db.Transaction(func(tx *gorm.DB) error {
		profiles := repository.NewUserProfileRepository(tx)
		profile, err := profiles.GetProfileByIDForUpdate(params.UserID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return usererrors.ErrUserProfileNotFound
		}
		if err != nil {
			return err
		}
		if !profile.IsEnabled {
			return usererrors.ErrUserDisabled
		}
		if profile.Version != params.ExpectedVersion {
			return usererrors.ErrUserProfileVersionConflict
		}
		affected, err := profiles.UpdateProfile(params.UserID, params.ExpectedVersion, tables.UserProfile{
			UserName: params.UserName, AvatarURL: avatarValue(params.AvatarURL),
		})
		if err != nil {
			return err
		}
		if affected != 1 {
			return usererrors.ErrUserProfileVersionConflict
		}
		updated, err := profiles.GetProfileByID(params.UserID)
		if err != nil {
			return err
		}
		result = profileModel(updated)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// BatchQueryUserProfiles 按输入顺序返回资料与未找到的 ID，重复 ID 只处理首次出现。
// 部分或全部缺失仍属于正常查询结果，空集合始终返回非 nil 切片。
func (s *UserProfileService) BatchQueryUserProfiles(userIDs []int64) ([]*models.UserProfile, []int64, error) {
	if err := validateBatchUserIDs(userIDs); err != nil {
		return nil, nil, err
	}
	return batchQueryUserProfiles(userIDs, appruntime.Current().Repositories.UserProfile)
}

func batchQueryUserProfiles(userIDs []int64, profiles *repository.UserProfileRepository) ([]*models.UserProfile, []int64, error) {
	ids := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	rows, err := profiles.GetProfilesByIDs(ids)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int64]*models.UserProfile, len(rows))
	for i := range rows {
		byID[rows[i].ID] = profileModel(&rows[i])
	}
	result := make([]*models.UserProfile, 0, len(rows))
	missing := make([]int64, 0)
	for _, id := range ids {
		if profile, exists := byID[id]; exists {
			result = append(result, profile)
		} else {
			missing = append(missing, id)
		}
	}
	return result, missing, nil
}

// QueryUserProfilePage 查询精确昵称、启用状态筛选下的资料页和总数。
// 资料固定按用户 ID 降序排列，页码超出结果范围时返回空页及实际总数。
func (s *UserProfileService) QueryUserProfilePage(params QueryUserProfilePageParams) ([]*models.UserProfile, int64, error) {
	if err := validateUserProfilePage(params); err != nil {
		return nil, 0, err
	}
	return queryUserProfilePage(params, appruntime.Current().Repositories.UserProfile)
}

func queryUserProfilePage(params QueryUserProfilePageParams, profiles *repository.UserProfileRepository) ([]*models.UserProfile, int64, error) {
	rows, total, err := profiles.QueryProfilePage(params.UserName, params.IsEnabled, params.PageNumber, params.PageSize)
	if err != nil {
		return nil, 0, err
	}
	result := make([]*models.UserProfile, 0, len(rows))
	for i := range rows {
		result = append(result, profileModel(&rows[i]))
	}
	return result, total, nil
}

// profileModel 还原完整领域模型，不与表结构共享可变的头像指针。
func profileModel(profile *tables.UserProfile) *models.UserProfile {
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
	return userProfile
}

// avatarValue 保留未提供、空字符串以及具体 URL 三种状态。
func avatarValue(avatar *string) sql.NullString {
	if avatar == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *avatar, Valid: true}
}
