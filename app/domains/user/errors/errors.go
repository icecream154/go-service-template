// Package usererrors 声明用户领域内部可识别的业务错误，不依赖 HTTP 或对外错误码。
package usererrors

import "errors"

var (
	// ErrInvalidUserID 表示调用业务用例时传入的用户 ID 不合法。
	ErrInvalidUserID = errors.New("user ID must be positive")
	// ErrUserProfileNotFound 表示目标用户资料不存在。
	ErrUserProfileNotFound = errors.New("user profile not found")
	// ErrAccountAlreadyExists 表示同一来源的外部账户已经关联了用户。
	ErrAccountAlreadyExists = errors.New("user account already exists")
	// ErrUserDisabled 表示停用用户不能修改资料。
	ErrUserDisabled = errors.New("user is disabled")
	// ErrUserProfileVersionConflict 表示提交的资料版本已经过期。
	ErrUserProfileVersionConflict = errors.New("user profile version conflict")
	// ErrUserProfilePermissionDenied 表示发起方不允许修改目标用户的资料。
	ErrUserProfilePermissionDenied = errors.New("user profile operation is not permitted")
)

// InvalidArgumentError 表示业务用例收到的某个参数不符合约束。
// Field 和 Reason 描述可公开的参数问题，不包含数据库错误或其他内部详情。
// 它只表达内部校验结果，HTTP 类别和字段明细由接口适配层转换。
type InvalidArgumentError struct {
	// Field 是不合法的参数名称。
	Field string
	// Reason 说明该参数违反的约束。
	Reason string
}

// Error 返回内部参数校验说明；接口适配层通过 errors.As 读取字段，不比较此文案。
func (err *InvalidArgumentError) Error() string {
	return err.Field + ": " + err.Reason
}
