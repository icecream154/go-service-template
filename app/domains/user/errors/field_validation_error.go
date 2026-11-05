package usererrors

// ValidationField 标识领域规则检查的字段，不表示 HTTP 参数名或 DTO 路径。
// 接口适配层按具体请求契约将其映射为对外字段名称。
type ValidationField string

const (
	// FieldAccountSource 表示外部账户身份所属的命名空间。
	FieldAccountSource ValidationField = "AccountSource"
	// FieldExternalAccountID 表示身份命名空间内的外部账户标识。
	FieldExternalAccountID ValidationField = "ExternalAccountID"
	// FieldUserName 表示用户展示昵称。
	FieldUserName ValidationField = "UserName"
	// FieldAvatarURL 表示用户头像地址。
	FieldAvatarURL ValidationField = "AvatarURL"
)

// FieldValidationError 表示某个领域字段违反了规则，并保留底层检查结果。
// 本类型不提供 HTTP 状态、对外业务码或响应文案；调用方通过 errors.As
// 读取字段和原始原因，再由接口适配层构造对外错误。
type FieldValidationError struct {
	// Field 是领域内的字段标识，不是请求参数名。
	Field ValidationField
	// Err 保存原始检查错误；正常构造时必须非 nil。
	// 其错误类别保持不变，不能仅凭当前包装类型认定为输入问题。
	Err error
}

// Error 返回内部诊断说明，响应文案由接口适配层另行映射。
func (err *FieldValidationError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Err == nil {
		return string(err.Field) + ": field validation failed"
	}
	return string(err.Field) + ": " + err.Err.Error()
}

// Unwrap 保留原始错误，供 errors.Is 和 errors.As 识别基础输入错误或规则错误。
func (err *FieldValidationError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}
