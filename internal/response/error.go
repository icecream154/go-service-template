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
	// 消息由错误产生处提供；本组件不因选择该类别而替换消息或字段明细。
	CodeInternalError ErrorCode = "INTERNAL_ERROR"
	// CodeServiceUnavailable 表示服务暂时无法处理请求，例如临时过载或依赖不可用，对应 HTTP 503。
	CodeServiceUnavailable ErrorCode = "SERVICE_UNAVAILABLE"
)

// BusinessErrorCode 表示对外公开的具体业务失败原因，可不提供。
// 具体业务码及其通用类别归属由对应 Controller 在自身文件内维护，本组件只声明类型。
type BusinessErrorCode string

// ErrorInfo 是对外错误结构，不直接序列化内部 error 或数据库驱动错误。
// Code 必填；BusinessCode 可选，未设置时不输出该字段。
type ErrorInfo struct {
	// Code 必填，表示跨领域通用的错误类别，供调用方执行统一错误处理。
	// 必须使用本组件登记的枚举；HTTP 状态码由该类别的固定映射决定。
	Code ErrorCode `json:"code"`

	// BusinessCode 可选，表示该类别下需要对外区分的具体业务失败原因，空值时省略。
	// 与 Code 的通用分类不同，具体业务码及映射由所属 Controller 集中定义。
	// 同一业务码固定归属一个 Code，不要求与内部领域 error 一一对应。
	BusinessCode BusinessErrorCode `json:"business_code,omitempty"`

	// Message 必填，直接采用接收到的 err.Error()，供人阅读，不作为程序判断错误的依据。
	// 项目自行声明的错误使用非空白、可公开的英文说明；第三方错误保留原始文案。
	// 包括内部错误在内，接口层不翻译、包装或替换已经收到的错误消息。
	Message string `json:"message"`

	// Details 可选，提供请求参数校验的字段明细，包括参数名和违反的约束。
	// 直接采用 Validator 等发生处已构造的明细，不修改字段、原因或顺序；没有明细时省略。
	Details []FieldError `json:"details,omitempty"`
}

// WithBusinessCode 返回附带业务码的错误副本，不修改原错误定义。
// Controller 通过自身映射保证同一业务码始终归属同一通用类别。
func (failure ErrorInfo) WithBusinessCode(code BusinessErrorCode) ErrorInfo {
	failure.BusinessCode = code
	return failure
}

// FieldError 定位请求中的一个校验问题，Reason 为可对外展示的原因。
type FieldError struct {
	// Field 是实际请求字段或接口约定的字段路径，由 Validator 确定。
	Field string `json:"field"`
	// Reason 是已准备好的失败说明，可直接采用底层 Error() 或由 Validator 组织。
	Reason string `json:"reason"`
}

// Error 返回字段及其失败说明，可作为字段校验失败响应的 Message。
func (err FieldError) Error() string {
	return err.Field + ": " + err.Reason
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

// InternalError 构造框架兜底使用的默认内部错误，原始原因由调用方记录到服务端日志。
// 已有具体错误时，接口层可以直接构造 CodeInternalError 并采用该错误的 Error()。
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
