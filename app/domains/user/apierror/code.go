// Package apierror 维护用户领域对外业务错误码及其响应映射。
// 领域错误与对外业务码分别维护，Service 不依赖本包或 HTTP 响应结构。
package apierror

import "go-service-template/internal/response"

const (
	// CodeUserProfileNotFound 表示请求的用户资料不存在，固定归属 NOT_FOUND。
	CodeUserProfileNotFound response.BusinessErrorCode = "USER_PROFILE_NOT_FOUND"
)
