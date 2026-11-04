// Package usererrors 声明用户领域内部可识别的业务错误，不依赖 HTTP 或对外错误码。
package usererrors

import "errors"

var (
	// ErrInvalidUserID 表示调用业务用例时传入的用户 ID 不合法。
	ErrInvalidUserID = errors.New("user ID must be positive")
	// ErrUserProfileNotFound 表示目标用户资料不存在。
	ErrUserProfileNotFound = errors.New("user profile not found")
)
