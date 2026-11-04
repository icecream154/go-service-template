package service

const (
	// MaxUserProfileBatchSize 限制一次批量查询提交的 ID 数量，包含重复项。
	MaxUserProfileBatchSize = 100
	// MaxUserProfilePageSize 限制分页查询每页最多返回的用户数量。
	MaxUserProfilePageSize = 100
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
	// ExpectedVersion 是读取资料时取得的旧版本，成功后递增。
	ExpectedVersion int64
	// UserName 是替换后的昵称，不能只包含空白。
	UserName string
	// AvatarURL 是替换后的头像；nil 明确表示清空头像。
	AvatarURL *string
}

// QueryUserProfilePageParams 表示页码分页与可选的精确筛选条件。
type QueryUserProfilePageParams struct {
	// PageNumber 从 1 开始，调用方负责填入接口约定的默认值。
	PageNumber int
	// PageSize 取值为 1～MaxUserProfilePageSize。
	PageSize int
	// UserName 非空时按完整昵称精确匹配。
	UserName string
	// IsEnabled 为 nil 时同时查询启用、停用用户。
	IsEnabled *bool
}
