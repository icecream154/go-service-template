// Package service 实现用户相关的业务用例。
package service

import (
	"errors"
	"log"
	"math"

	"go-service-template/app/domains/user/models"
	"go-service-template/app/domains/user/repository"
	"go-service-template/app/domains/user/tables"
	"go-service-template/internal/check/collection"
	"go-service-template/internal/check/relation"
	"go-service-template/internal/check/scalar"
	dbmysql "go-service-template/internal/database/mysql"
	"go-service-template/internal/database/sqlvalue"
	appruntime "go-service-template/internal/runtime"
	"go-service-template/internal/snowflake"

	"gorm.io/gorm"
)

// errors: shared

var (
	// ErrUserProfileNotFound 表示查询或更新的目标用户资料不存在。
	ErrUserProfileNotFound = errors.New("user profile not found")
	// ErrUserProfilePermissionDenied 表示发起方不允许查询或修改目标用户的资料。
	ErrUserProfilePermissionDenied = errors.New("user profile operation is not permitted")
)

// errors: CreateUser

var (
	// ErrAccountAlreadyExists 表示同一来源的外部账户已经关联了用户。
	ErrAccountAlreadyExists = errors.New("user account already exists")
	// errInvalidGeneratedUserID 表示发号器未返回错误，但产生了不合法的内部 ID。
	errInvalidGeneratedUserID = errors.New("generated user ID must be positive")
)

// errors: UpdateUserProfile

var (
	// ErrUserDisabled 表示停用用户不能修改资料。
	ErrUserDisabled = errors.New("user is disabled")
	// ErrUserProfileVersionConflict 表示提交的资料版本已经过期，或更新条件不再成立。
	ErrUserProfileVersionConflict = errors.New("user profile version conflict")
)

// UserProfileService 提供用户资料操作，返回领域模型或用例专属 Result。
// 结构体不保存仓储或本次查询的状态。
type UserProfileService struct{}

// NewUserProfileService 创建无状态的用户资料服务，不执行查询。
func NewUserProfileService() *UserProfileService {
	return &UserProfileService{}
}

// use case: QueryUserProfileByUserID

// QueryUserProfileByUserIDParams 表示已确认身份的用户按内部 ID 查询本人资料的输入。
type QueryUserProfileByUserIDParams struct {
	// OperatorUserID 是调用入口根据可信身份确定的发起方，必须为正数且与目标用户相同。
	OperatorUserID int64
	// UserID 是需要查询的用户，必须为正数。
	UserID int64
}

// validateQueryUserProfileByUserIDParams 检查发起方和目标用户 ID，保留公共检查错误的分类。
func validateQueryUserProfileByUserIDParams(params QueryUserProfileByUserIDParams) error {
	if err := scalar.PositiveInt64(params.OperatorUserID); err != nil {
		return err
	}
	if err := scalar.PositiveInt64(params.UserID); err != nil {
		return err
	}
	return nil
}

// QueryUserProfileByUserID 按内部用户 ID 查询本人完整资料，不默认过滤停用用户。
// 输入非法时返回公共检查错误；访问他人返回 ErrUserProfilePermissionDenied；未命中返回 ErrUserProfileNotFound；
// 其他查询故障记录操作上下文后原样返回，不替换为通用查询失败错误。
func (s *UserProfileService) QueryUserProfileByUserID(params QueryUserProfileByUserIDParams) (*models.UserProfile, error) {
	if err := validateQueryUserProfileByUserIDParams(params); err != nil {
		return nil, err
	}
	// 各调用入口负责确认身份真实性，Service 独立保障用例的本人访问约束，再访问仓储。
	if params.OperatorUserID != params.UserID {
		return nil, ErrUserProfilePermissionDenied
	}

	// 从已初始化的 Runtime 取得普通连接仓储，本方法不创建或加入事务。
	profile, err := appruntime.Current().Repositories.UserProfile.GetProfileByID(params.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserProfileNotFound
		}
		log.Printf("Query user profile failed: error=%v", err)
		return nil, err
	}
	return profileModel(profile), nil
}

// use case: CreateUser

// CreateUserParams 表示首次注册所需的外部账户身份与初始资料。
// 注册时尚无内部用户 ID；账户字段是调用方声明，本用例不验证外部身份凭证。
type CreateUserParams struct {
	// AccountSource 是外部账户所属的完整身份命名空间，原文保留。
	AccountSource string
	// ExternalAccountID 是该命名空间中的原始账户标识，原文保留。
	ExternalAccountID string
	// UserName 是非空白的展示昵称，允许与其他用户重复。
	UserName string
	// AvatarURL 为 nil 表示未提供头像，空字符串与 nil 分别保留。
	AvatarURL *string
}

// validateCreateUserParams 组织创建用例的必填与可选输入，字段自身规则统一交给 models。
// 未提供头像和显式空字符串均允许，保留原始值，不补默认值或修改 Params。
func validateCreateUserParams(params CreateUserParams) error {
	if err := models.ValidateAccountSource(params.AccountSource); err != nil {
		return err
	}
	if err := models.ValidateExternalAccountID(params.ExternalAccountID); err != nil {
		return err
	}
	if err := models.ValidateUserName(params.UserName); err != nil {
		return err
	}
	if params.AvatarURL != nil && *params.AvatarURL != "" {
		if err := models.ValidateAvatarURL(*params.AvatarURL); err != nil {
			return err
		}
	}
	return nil
}

// CreateUser 按首次注册语义创建用户，初始启用、版本为 0。
// 输入非法时返回领域字段规则错误；重复账户返回 ErrAccountAlreadyExists；
// 发号或持久化故障记录操作上下文后原样返回。本用例不核验外部身份凭证。
func (s *UserProfileService) CreateUser(params CreateUserParams) (*models.UserProfile, error) {
	if err := validateCreateUserParams(params); err != nil {
		return nil, err
	}
	profile, err := createUser(params, appruntime.Current().Repositories.UserProfile, snowflake.NextID)
	if err != nil {
		if errors.Is(err, ErrAccountAlreadyExists) {
			return nil, err
		}
		log.Printf("Create user failed: error=%v", err)
		return nil, err
	}
	return profile, nil
}

// createUser 按次接收仓储和发号函数，不将依赖或单次注册状态保存在 Service 上。
// 确认账户已存在时返回本层业务错误；技术故障保持原始错误值。
func createUser(params CreateUserParams, profiles *repository.UserProfileRepository, nextID func() (int64, error)) (*models.UserProfile, error) {
	id, err := nextID()
	if err != nil {
		return nil, err
	}
	if id <= 0 {
		return nil, errInvalidGeneratedUserID
	}
	profile := &tables.UserProfile{
		ID: id, AccountSource: params.AccountSource, ExternalAccountID: params.ExternalAccountID,
		UserName: params.UserName, AvatarURL: sqlvalue.ToNullString(params.AvatarURL), IsEnabled: true, Version: 0,
	}
	if err := profiles.CreateProfile(profile); err != nil {
		if dbmysql.IsDuplicateKey(err) {
			// 重复键本身不能确定业务原因；复查只确认请求账户是否已存在，不声称识别了冲突索引。
			if _, accountErr := profiles.GetProfileByAccount(params.AccountSource, params.ExternalAccountID); accountErr == nil {
				return nil, ErrAccountAlreadyExists
			} else if !errors.Is(accountErr, gorm.ErrRecordNotFound) {
				// 复查执行失败时保留该技术错误，两次操作的原因只在日志中关联。
				log.Printf("Check account after duplicate key failed: create_error=%v account_error=%v", err, accountErr)
				return nil, accountErr
			}
		}
		return nil, err
	}
	return profileModel(profile), nil
}

// use case: UpdateUserProfile

// UpdateUserProfileParams 表示本人对昵称、头像的完整替换及并发控制条件。
type UpdateUserProfileParams struct {
	// OperatorUserID 是调用入口根据可信身份确定的发起方，必须与目标用户相同。
	OperatorUserID int64
	// UserID 是本次修改的目标用户。
	UserID int64
	// ExpectedVersion 是读取资料时取得的旧版本，成功后递增，取值位于公开版本边界内。
	ExpectedVersion int64
	// UserName 是替换后的昵称，不能只包含空白。
	UserName string
	// AvatarURL 是替换后的非空头像地址，必须非 nil；本用例不支持清空头像。
	AvatarURL *string
}

const (
	// MinUserProfileExpectedVersion 表示更新资料时允许提交的版本下限，允许等于该值。
	MinUserProfileExpectedVersion int64 = 0
	// MaxUserProfileExpectedVersion 表示允许提交的版本上限，包含边界，并为成功后的版本递增保留空间。
	MaxUserProfileExpectedVersion int64 = math.MaxInt64 - 1
)

// validateUpdateUserProfileParams 检查更新用例的纯输入约束，不读取权限、状态或实际版本。
// 本用例完整替换昵称与头像，头像必须提供且不能清空。
func validateUpdateUserProfileParams(params UpdateUserProfileParams) error {
	if err := scalar.PositiveInt64(params.OperatorUserID); err != nil {
		return err
	}
	if err := scalar.PositiveInt64(params.UserID); err != nil {
		return err
	}
	if err := scalar.Int64InRange(params.ExpectedVersion, MinUserProfileExpectedVersion, MaxUserProfileExpectedVersion); err != nil {
		return err
	}
	if err := models.ValidateUserName(params.UserName); err != nil {
		return err
	}
	if err := relation.NotNil(params.AvatarURL); err != nil {
		return err
	}
	if err := models.ValidateAvatarURL(*params.AvatarURL); err != nil {
		return err
	}
	return nil
}

// UpdateUserProfile 完整替换本人的昵称、头像，成功后递增版本。
// 输入非法时返回公共检查或领域字段规则错误；权限、缺失、停用、版本冲突分别返回对应业务错误。
// 事务执行故障记录操作上下文后原样返回；事务内使用行锁保障状态检查和更新。
func (s *UserProfileService) UpdateUserProfile(params UpdateUserProfileParams) (*models.UserProfile, error) {
	if err := validateUpdateUserProfileParams(params); err != nil {
		return nil, err
	}
	// 纯输入规则通过后，再判断当前用例的本人操作约束；身份真实性仍由认证入口负责。
	if params.OperatorUserID != params.UserID {
		return nil, ErrUserProfilePermissionDenied
	}
	profile, err := updateUserProfile(params, appruntime.Current().DB.User)
	if err != nil {
		if errors.Is(err, ErrUserProfileNotFound) || errors.Is(err, ErrUserDisabled) || errors.Is(err, ErrUserProfileVersionConflict) {
			return nil, err
		}
		log.Printf("Update user profile failed: error=%v", err)
		return nil, err
	}
	return profile, nil
}

// updateUserProfile 在同一事务中锁定、检查和修改资料，失败时不返回部分结果。
func updateUserProfile(params UpdateUserProfileParams, db *gorm.DB) (*models.UserProfile, error) {
	var result *models.UserProfile
	err := db.Transaction(func(tx *gorm.DB) error {
		profiles := repository.NewUserProfileRepository(tx)
		profile, err := profiles.GetProfileByIDForUpdate(params.UserID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserProfileNotFound
		}
		if err != nil {
			return err
		}
		if !profile.IsEnabled {
			return ErrUserDisabled
		}
		if profile.Version != params.ExpectedVersion {
			return ErrUserProfileVersionConflict
		}
		affected, err := profiles.UpdateProfile(params.UserID, params.ExpectedVersion, tables.UserProfile{
			UserName: params.UserName, AvatarURL: sqlvalue.ToNullString(params.AvatarURL),
		})
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrUserProfileVersionConflict
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

// use case: BatchQueryUserProfiles

// BatchQueryUserProfilesParams 表示按提交顺序批量查询用户资料的输入。
type BatchQueryUserProfilesParams struct {
	// UserIDs 是待查询的正整数 ID 列表；数量在去重前检查，重复项由用例去重。
	UserIDs []int64
}

// BatchQueryUserProfilesResult 组合本次批量查询找到的资料和未找到的用户 ID。
// 部分或全部 ID 未找到仍是成功结果；执行失败时不返回此结构。
type BatchQueryUserProfilesResult struct {
	// UserProfiles 按输入中首次出现的顺序保存已找到的资料，无匹配时为空切片。
	UserProfiles []*models.UserProfile
	// MissingUserIDs 按输入中首次出现的顺序保存未找到的 ID，无缺失时为空切片。
	MissingUserIDs []int64
}

const (
	// MinUserProfileBatchSize 表示单次批量查询提交的 ID 数量下限，包含重复项，允许等于该值。
	MinUserProfileBatchSize = 1
	// MaxUserProfileBatchSize 表示单次批量查询提交的 ID 数量上限，包含重复项，允许等于该值。
	MaxUserProfileBatchSize = 100
)

// validateBatchQueryUserProfilesParams 在去重前检查提交数量，再逐项检查整数 ID。
// 集合检查不重排、不去重，也不把整数转换回 HTTP 文本重新解析。
func validateBatchQueryUserProfilesParams(params BatchQueryUserProfilesParams) error {
	if err := collection.SliceLengthInRange(params.UserIDs, MinUserProfileBatchSize, MaxUserProfileBatchSize); err != nil {
		return err
	}
	if err := collection.Each(params.UserIDs, scalar.PositiveInt64); err != nil {
		return err
	}
	return nil
}

// BatchQueryUserProfiles 按输入顺序返回资料与未找到的 ID，重复 ID 只处理首次出现。
// 输入非法时原样返回公共检查错误，查询故障记录操作上下文后原样返回。
// 部分或全部缺失仍属于正常查询结果，空集合始终返回非 nil 切片。
func (s *UserProfileService) BatchQueryUserProfiles(params BatchQueryUserProfilesParams) (*BatchQueryUserProfilesResult, error) {
	if err := validateBatchQueryUserProfilesParams(params); err != nil {
		return nil, err
	}
	result, err := batchQueryUserProfiles(params.UserIDs, appruntime.Current().Repositories.UserProfile)
	if err != nil {
		log.Printf("Batch query user profiles failed: error=%v", err)
		return nil, err
	}
	return result, nil
}

// batchQueryUserProfiles 在查询前去重，并按首次出现的顺序组织找到和缺失的 ID。
func batchQueryUserProfiles(userIDs []int64, profiles *repository.UserProfileRepository) (*BatchQueryUserProfilesResult, error) {
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
		return nil, err
	}
	byID := make(map[int64]*models.UserProfile, len(rows))
	for i := range rows {
		byID[rows[i].ID] = profileModel(&rows[i])
	}
	result := &BatchQueryUserProfilesResult{
		UserProfiles:   make([]*models.UserProfile, 0, len(rows)),
		MissingUserIDs: make([]int64, 0),
	}
	for _, id := range ids {
		if profile, exists := byID[id]; exists {
			result.UserProfiles = append(result.UserProfiles, profile)
		} else {
			result.MissingUserIDs = append(result.MissingUserIDs, id)
		}
	}
	return result, nil
}

// use case: QueryUserProfilePage

// QueryUserProfilePageParams 表示页码分页与可选的精确筛选条件。
type QueryUserProfilePageParams struct {
	// PageNumber 不小于 MinUserProfilePageNumber，调用方在未提供时显式采用默认值。
	PageNumber int
	// PageSize 取值位于 MinUserProfilePageSize～MaxUserProfilePageSize，包含边界。
	PageSize int
	// UserName 非空时按完整昵称精确匹配。
	UserName string
	// IsEnabled 为 nil 时同时查询启用、停用用户。
	IsEnabled *bool
}

// QueryUserProfilePageResult 组合当前页的资料和全部匹配记录的数量。
// 当前页无记录时仍返回成功结果；总数与当前页不承诺来自同一个数据库快照。
type QueryUserProfilePageResult struct {
	// UserProfiles 保存按用户 ID 降序排列的当前页资料，无匹配时为空切片。
	UserProfiles []*models.UserProfile
	// TotalCount 是符合筛选条件的总记录数，不是当前页的记录数。
	TotalCount int64
}

const (
	// MinUserProfilePageNumber 表示有效页码的下限，允许等于该值。
	MinUserProfilePageNumber = 1
	// DefaultUserProfilePageNumber 供入口在未提供页码时准备输入，不覆盖显式非法值。
	DefaultUserProfilePageNumber = 1
	// MinUserProfilePageSize 表示每页返回条数的下限，允许等于该值。
	MinUserProfilePageSize = 1
	// DefaultUserProfilePageSize 供入口在未提供每页条数时准备输入，不覆盖显式非法值。
	DefaultUserProfilePageSize = 20
	// MaxUserProfilePageSize 表示每页返回条数的上限，允许等于该值。
	MaxUserProfilePageSize = 100
)

// validateQueryUserProfilePageParams 先保证页码及每页条数有效，再检查偏移范围与可选筛选值。
// 默认值由调用入口准备，显式零值不在 Service 内自动改写。
func validateQueryUserProfilePageParams(params QueryUserProfilePageParams) error {
	if err := scalar.IntInRange(params.PageSize, MinUserProfilePageSize, MaxUserProfilePageSize); err != nil {
		return err
	}
	if err := scalar.IntAtLeast(params.PageNumber, MinUserProfilePageNumber); err != nil {
		return err
	}
	if err := scalar.IntAtMost(params.PageNumber-1, math.MaxInt/params.PageSize); err != nil {
		return err
	}
	if params.UserName != "" {
		if err := models.ValidateUserName(params.UserName); err != nil {
			return err
		}
	}
	return nil
}

// QueryUserProfilePage 查询精确昵称、启用状态筛选下的资料页和总数。
// 输入非法时原样返回公共检查或领域字段规则错误，查询故障记录操作上下文后原样返回。
// 资料固定按 ID 降序排列，页码超出结果范围时返回空页及实际总数。
func (s *UserProfileService) QueryUserProfilePage(params QueryUserProfilePageParams) (*QueryUserProfilePageResult, error) {
	if err := validateQueryUserProfilePageParams(params); err != nil {
		return nil, err
	}
	result, err := queryUserProfilePage(params, appruntime.Current().Repositories.UserProfile)
	if err != nil {
		log.Printf("Query user profile page failed: error=%v", err)
		return nil, err
	}
	return result, nil
}

// queryUserProfilePage 读取总数及当前页，保留仓储的排序和查询语义。
func queryUserProfilePage(params QueryUserProfilePageParams, profiles *repository.UserProfileRepository) (*QueryUserProfilePageResult, error) {
	rows, total, err := profiles.QueryProfilePage(params.UserName, params.IsEnabled, params.PageNumber, params.PageSize)
	if err != nil {
		return nil, err
	}
	result := &QueryUserProfilePageResult{
		UserProfiles: make([]*models.UserProfile, 0, len(rows)),
		TotalCount:   total,
	}
	for i := range rows {
		result.UserProfiles = append(result.UserProfiles, profileModel(&rows[i]))
	}
	return result, nil
}

// shared

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
