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
	var fieldValidation *usererrors.FieldValidationError
	var invalidArgument *usererrors.InvalidArgumentError
	switch {
	case errors.As(err, &fieldValidation) && fieldValidation != nil:
		if detail, ok := fieldValidationDetail(fieldValidation); ok {
			return response.InvalidArgument("", detail)
		}
		return response.InternalError()
	case errors.As(err, &invalidArgument) && invalidArgument != nil:
		return response.InvalidArgument("", response.FieldError{
			Field: invalidArgument.Field, Reason: invalidArgument.Reason,
		})
	case errors.Is(err, usererrors.ErrInvalidUserID):
		return response.InvalidArgument("", response.FieldError{
			Field: "user_id", Reason: "must be a positive 64-bit integer",
		})
	case errors.Is(err, usererrors.ErrUserProfileNotFound):
		return response.NotFound("user profile not found").WithBusinessCode(CodeUserProfileNotFound)
	case errors.Is(err, usererrors.ErrAccountAlreadyExists):
		return response.Conflict("user account already exists").WithBusinessCode(CodeAccountAlreadyExists)
	case errors.Is(err, usererrors.ErrUserDisabled):
		return response.Conflict("user is disabled").WithBusinessCode(CodeUserDisabled)
	case errors.Is(err, usererrors.ErrUserProfileVersionConflict):
		return response.Conflict("user profile version conflict").WithBusinessCode(CodeUserProfileVersionConflict)
	case errors.Is(err, usererrors.ErrUserProfilePermissionDenied):
		return response.PermissionDenied("user profile operation is not permitted").WithBusinessCode(CodeUserProfilePermissionDenied)
	default:
		return response.InternalError()
	}
}
