package service

import "math"

const (
	// MinUserProfileBatchSize 表示单次批量查询提交的 ID 数量下限，包含重复项，允许等于该值。
	MinUserProfileBatchSize = 1
	// MaxUserProfileBatchSize 表示单次批量查询提交的 ID 数量上限，包含重复项，允许等于该值。
	MaxUserProfileBatchSize = 100
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
	// MinUserProfileExpectedVersion 表示更新资料时允许提交的版本下限，允许等于该值。
	MinUserProfileExpectedVersion int64 = 0
	// MaxUserProfileExpectedVersion 表示允许提交的版本上限，包含边界，并为成功后的版本递增保留空间。
	MaxUserProfileExpectedVersion int64 = math.MaxInt64 - 1
)

// CreateUserParams 表示首次注册所需的外部账户身份与初始资料。
// 注册时尚无内部用户 ID，身份凭证的真实性由后续认证接入负责。
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

// UpdateUserProfileParams 表示本人对昵称、头像的完整替换及并发控制条件。
type UpdateUserProfileParams struct {
	// OperatorUserID 是声明的操作发起方，当前必须与目标用户相同。
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
