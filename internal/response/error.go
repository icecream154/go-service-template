package response

import (
	"net/http"
	"strings"
)

// ErrorCode 表示跨领域通用的错误类别，只允许使用本组件登记的枚举。
// 每个类别固定对应一个 HTTP 状态码，由 Fail 统一选择，调用方不单独设置状态码。
type ErrorCode string

const (
	// CodeInvalidArgument 表示请求参数缺失、格式错误或不符合约束，对应 HTTP 400。
	CodeInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	// CodeUnauthenticated 表示未提供有效身份凭证，或凭证已失效，对应 HTTP 401。
	CodeUnauthenticated ErrorCode = "UNAUTHENTICATED"
	// CodePermissionDenied 表示调用方身份已确认，但没有执行该操作的权限，对应 HTTP 403。
	CodePermissionDenied ErrorCode = "PERMISSION_DENIED"
	// CodeNotFound 表示请求的资源或路由不存在，对应 HTTP 404。
	CodeNotFound ErrorCode = "NOT_FOUND"
	// CodeConflict 表示请求与资源当前状态冲突，例如唯一标识重复或版本不匹配，对应 HTTP 409。
	CodeConflict ErrorCode = "CONFLICT"
	// CodeRateLimited 表示请求超过允许的频率或额度，对应 HTTP 429。
	CodeRateLimited ErrorCode = "RATE_LIMITED"
	// CodeInternalError 表示未预期的内部错误或未处理异常，对应 HTTP 500。
	// 对外仅返回固定文案，原始错误详情由调用方记录到服务端日志。
	CodeInternalError ErrorCode = "INTERNAL_ERROR"
	// CodeServiceUnavailable 表示服务暂时无法处理请求，例如临时过载或依赖不可用，对应 HTTP 503。
	CodeServiceUnavailable ErrorCode = "SERVICE_UNAVAILABLE"
)

// BusinessErrorCode 表示对外公开的具体业务失败原因，可不提供。
// 业务码及其通用类别归属由各领域的接口适配层集中维护，本组件不声明业务码。
type BusinessErrorCode string

// ErrorInfo 是对外错误结构，不直接序列化内部 error 或数据库驱动错误。
// Code 必填；BusinessCode 可选，未设置时不输出该字段。
type ErrorInfo struct {
	Code         ErrorCode         `json:"code"`
	BusinessCode BusinessErrorCode `json:"business_code,omitempty"`
	Message      string            `json:"message"`
	Details      []FieldError      `json:"details,omitempty"`
}

// WithBusinessCode 返回附带业务码的错误副本，不修改原错误定义。
// 调用方应复用领域接口适配层的映射，保证同一业务码始终归属同一通用类别。
func (failure ErrorInfo) WithBusinessCode(code BusinessErrorCode) ErrorInfo {
	failure.BusinessCode = code
	return failure
}

// FieldError 定位请求中的一个校验问题，Reason 为可对外展示的原因。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// InvalidArgument 绑定 HTTP 400 和参数错误码；空白文案使用默认说明。
func InvalidArgument(message string, details ...FieldError) ErrorInfo {
	failure := newErrorInfo(CodeInvalidArgument, message)
	failure.Details = details
	return failure
}

// Unauthenticated 绑定 HTTP 401，表示缺少有效身份凭证；空白文案使用默认说明。
func Unauthenticated(message string) ErrorInfo {
	return newErrorInfo(CodeUnauthenticated, message)
}

// PermissionDenied 绑定 HTTP 403，表示调用方无权执行操作；空白文案使用默认说明。
func PermissionDenied(message string) ErrorInfo {
	return newErrorInfo(CodePermissionDenied, message)
}

// NotFound 绑定 HTTP 404 和目标不存在错误码。
func NotFound(message string) ErrorInfo {
	return newErrorInfo(CodeNotFound, message)
}

// Conflict 绑定 HTTP 409，表示请求与资源当前状态冲突；空白文案使用默认说明。
func Conflict(message string) ErrorInfo {
	return newErrorInfo(CodeConflict, message)
}

// RateLimited 绑定 HTTP 429，表示请求超过频率限制；空白文案使用默认说明。
func RateLimited(message string) ErrorInfo {
	return newErrorInfo(CodeRateLimited, message)
}

// InternalError 仅返回固定安全文案，原始原因由调用方记录到服务端日志。
func InternalError() ErrorInfo {
	return newErrorInfo(CodeInternalError, "")
}

// ServiceUnavailable 绑定 HTTP 503，表示服务暂时无法处理请求；空白文案使用默认说明。
func ServiceUnavailable(message string) ErrorInfo {
	return newErrorInfo(CodeServiceUnavailable, message)
}

// errorDefinition 集中保存通用类别的状态码和默认文案，不允许外部修改映射。
type errorDefinition struct {
	httpStatus     int
	defaultMessage string
}

// definition 同时作为通用错误码的允许列表，未登记的字符串不是合法类别。
func (code ErrorCode) definition() (errorDefinition, bool) {
	switch code {
	case CodeInvalidArgument:
		return errorDefinition{http.StatusBadRequest, "invalid request parameters"}, true
	case CodeUnauthenticated:
		return errorDefinition{http.StatusUnauthorized, "authentication required"}, true
	case CodePermissionDenied:
		return errorDefinition{http.StatusForbidden, "permission denied"}, true
	case CodeNotFound:
		return errorDefinition{http.StatusNotFound, "resource not found"}, true
	case CodeConflict:
		return errorDefinition{http.StatusConflict, "resource conflict"}, true
	case CodeRateLimited:
		return errorDefinition{http.StatusTooManyRequests, "too many requests"}, true
	case CodeInternalError:
		return errorDefinition{http.StatusInternalServerError, "internal server error"}, true
	case CodeServiceUnavailable:
		return errorDefinition{http.StatusServiceUnavailable, "service temporarily unavailable"}, true
	default:
		return errorDefinition{}, false
	}
}

// newErrorInfo 仅供已登记类别的构造器使用，空白文案采用该类别的默认说明。
func newErrorInfo(code ErrorCode, message string) ErrorInfo {
	definition, _ := code.definition()
	if strings.TrimSpace(message) == "" {
		message = definition.defaultMessage
	}
	return ErrorInfo{Code: code, Message: message}
}
