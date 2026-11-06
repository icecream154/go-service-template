package authn

import "errors"

// 通用认证错误使用固定命名错误值，初始化后不重新赋值；调用方用 errors.Is 识别，不能比较文案。
// 各错误彼此独立，ErrExpiredCredential 不自动匹配 ErrInvalidCredential。
// 本包不声明 HTTP 状态、业务错误码或新的错误包装类型，也不为技术故障另设统一认证失败错误。
var (
	// ErrMissingCredential 表示没有提供凭证：Scheme 和 Value 均为空或仅含空白。
	// 仅缺少其中一部分属于 ErrInvalidCredential，不能把残缺凭证当作未提供。
	ErrMissingCredential = errors.New("authentication credential is missing")
	// ErrUnsupportedScheme 表示已提供完整凭证，但当前认证器不支持对应的 Scheme。
	// 它不表示已识别调用方，也不应触发跳过认证或降级为匿名身份。
	ErrUnsupportedScheme = errors.New("authentication scheme is not supported")
	// ErrInvalidCredential 表示凭证不完整、格式不合法，或已明确验证失败、被撤销等。
	// 无法完成验证的技术故障不属于本错误，应保留下层错误供调用方区分。
	ErrInvalidCredential = errors.New("authentication credential is invalid")
	// ErrExpiredCredential 表示凭证真实性已经确认，但已超过该凭证约定的有效期。
	// 不能只读取未经验证的到期字段就返回本错误，也不能用它表示认证过程自身超时。
	ErrExpiredCredential = errors.New("authentication credential has expired")
)
