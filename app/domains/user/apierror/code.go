// Package apierror 维护用户领域对外业务错误码及其响应映射。
// 领域错误与对外业务码分别维护，Service 不依赖本包或 HTTP 响应结构。
package apierror

import "go-service-template/internal/response"

const (
	// CodeUserProfileNotFound 表示请求的用户资料不存在，固定归属 NOT_FOUND。
	CodeUserProfileNotFound response.BusinessErrorCode = "USER_PROFILE_NOT_FOUND"
	// CodeAccountAlreadyExists 表示注册身份已经存在，固定归属 CONFLICT。
	CodeAccountAlreadyExists response.BusinessErrorCode = "USER_ACCOUNT_ALREADY_EXISTS"
	// CodeUserDisabled 表示停用用户不能修改资料，固定归属 CONFLICT。
	CodeUserDisabled response.BusinessErrorCode = "USER_DISABLED"
	// CodeUserProfileVersionConflict 表示资料已经被其他请求修改，固定归属 CONFLICT。
	CodeUserProfileVersionConflict response.BusinessErrorCode = "USER_PROFILE_VERSION_CONFLICT"
	// CodeUserProfilePermissionDenied 表示发起方无权修改目标资料，固定归属 PERMISSION_DENIED。
	CodeUserProfilePermissionDenied response.BusinessErrorCode = "USER_PROFILE_PERMISSION_DENIED"
)
