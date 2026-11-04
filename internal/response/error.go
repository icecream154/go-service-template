package response

import (
	"net/http"
	"strings"
)

// ErrorCode 是客户端用于判断错误类型的稳定机器码，与 HTTP 状态码分别维护。
type ErrorCode string

const (
	CodeInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	CodeNotFound        ErrorCode = "NOT_FOUND"
	CodeInternalError   ErrorCode = "INTERNAL_ERROR"
)

// ErrorInfo 是对外错误结构，不直接序列化内部 error 或数据库驱动错误。
// HTTPStatus 只用于写入 HTTP 状态行，不属于 JSON 正文。
type ErrorInfo struct {
	Code       ErrorCode    `json:"code"`
	Message    string       `json:"message"`
	Details    []FieldError `json:"details,omitempty"`
	HTTPStatus int          `json:"-"`
}

// FieldError 定位请求中的一个校验问题，Reason 为可对外展示的原因。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// InvalidArgument 绑定 HTTP 400 和参数错误码；空白文案使用默认说明。
func InvalidArgument(message string, details ...FieldError) ErrorInfo {
	if strings.TrimSpace(message) == "" {
		message = "invalid request parameters"
	}
	return ErrorInfo{HTTPStatus: http.StatusBadRequest, Code: CodeInvalidArgument, Message: message, Details: details}
}

// NotFound 绑定 HTTP 404 和目标不存在错误码。
func NotFound(message string) ErrorInfo {
	if strings.TrimSpace(message) == "" {
		message = "resource not found"
	}
	return ErrorInfo{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: message}
}

// InternalError 仅返回固定安全文案，原始原因由调用方记录到服务端日志。
func InternalError() ErrorInfo {
	return ErrorInfo{HTTPStatus: http.StatusInternalServerError, Code: CodeInternalError, Message: "internal server error"}
}
