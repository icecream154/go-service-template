package apierror

import (
	"errors"
	"fmt"

	usererrors "go-service-template/app/domains/user/errors"
	"go-service-template/app/domains/user/models"
	"go-service-template/internal/check"
	"go-service-template/internal/response"
)

// FieldValidationDetail 将 models 字段规则返回的输入错误转换为公开字段明细。
// Validator 和 Service 错误映射复用同一转换，不将内部 Error() 文本作为响应文案。
// 参数必须来自已有领域字段规则；无法识别意味着规则和接口映射没有同步，属于程序错误。
func FieldValidationDetail(err error) response.FieldError {
	detail, ok := fieldValidationDetail(err)
	if !ok {
		panic("unsupported user profile field validation result")
	}
	return detail
}

// fieldValidationDetail 只映射已知输入问题；无效规则或未知结果交由内部错误出口处理。
func fieldValidationDetail(err error) (response.FieldError, bool) {
	if err == nil || errors.Is(err, check.ErrInvalidRule) || !errors.Is(err, check.ErrInvalidValue) {
		return response.FieldError{}, false
	}
	var violation *check.Violation
	if !errors.As(err, &violation) || violation == nil {
		return response.FieldError{}, false
	}
	var fieldValidation *usererrors.FieldValidationError
	if !errors.As(err, &fieldValidation) || fieldValidation == nil {
		return response.FieldError{}, false
	}

	var field string
	var maximum int
	switch fieldValidation.Field {
	case usererrors.FieldAccountSource:
		field, maximum = "account_source", models.MaxAccountSourceRuneCount
	case usererrors.FieldExternalAccountID:
		field, maximum = "external_account_id", models.MaxExternalAccountIDRuneCount
	case usererrors.FieldUserName:
		field, maximum = "user_name", models.MaxUserNameRuneCount
	case usererrors.FieldAvatarURL:
		field, maximum = "avatar_url", models.MaxAvatarURLRuneCount
	default:
		return response.FieldError{}, false
	}

	var reason string
	switch violation.Rule {
	case "valid_utf8":
		reason = "must be valid UTF-8 text"
	case "not_blank":
		reason = "must not be blank"
	case "rune_count":
		reason = fmt.Sprintf("must contain at most %d Unicode code points", maximum)
	case "url", "absolute_url", "http_url":
		if fieldValidation.Field != usererrors.FieldAvatarURL {
			return response.FieldError{}, false
		}
		reason = "must be an absolute HTTP(S) URL with a hostname"
	default:
		return response.FieldError{}, false
	}
	return response.FieldError{Field: field, Reason: reason}, true
}
