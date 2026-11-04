package apierror

import (
	"errors"

	usererrors "go-service-template/app/domains/user/errors"
	"go-service-template/internal/response"
)

// FromError 将内部领域错误转换为固定的对外响应，支持识别被包装的领域错误。
// 未识别的错误（包括 nil）统一回退为内部错误，不暴露原始错误信息。
// 调用方仅在业务操作失败时调用；内部错误的原始详情由调用方记录到服务端日志。
func FromError(err error) response.ErrorInfo {
	switch {
	case errors.Is(err, usererrors.ErrInvalidUserID):
		return response.InvalidArgument("", response.FieldError{
			Field: "user_id", Reason: "must be a positive 64-bit integer",
		})
	case errors.Is(err, usererrors.ErrUserProfileNotFound):
		return response.NotFound("user profile not found").WithBusinessCode(CodeUserProfileNotFound)
	default:
		return response.InternalError()
	}
}
