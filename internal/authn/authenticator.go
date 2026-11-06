package authn

import "context"

// Credential 表示交给认证器验证的一份凭证，不表示其中声明的身份已经可信。
// Scheme 和 Value 分开传递，凭证来源的读取与解析不属于本结构的职责。
// Value 属于敏感信息，不记录整个 Credential，不将原文放入错误、Principal 或业务参数。
// 本结构不实现自动校验或日志脱敏；调用方与认证器必须遵守这些使用约定。
type Credential struct {
	// Scheme 表示凭证方案名，例如 Bearer；方案名不决定调用方是用户还是服务。
	// 认证器按对应协议识别该名称，不支持的完整凭证返回 ErrUnsupportedScheme。
	Scheme string
	// Value 是凭证内容本身，不包含 Scheme；原文传给认证器，不在公共层裁剪或改写。
	Value string
}

// Authenticator 验证凭证并返回可信调用方，不负责业务授权或响应输出。
// 实现可以验证用户或服务凭证，身份类别及命名空间必须由可信验证结果产生。
//
// 成功返回完整 Principal 和 nil：Kind 为已声明类别，Namespace、SubjectID 均非空白。
// 失败返回 Principal{} 和非 nil 错误，调用方必须先检查错误，不能使用部分认证结果。
// 凭证缺失、不支持、无效、过期使用本包的对应错误值；明确撤销的凭证属于无效凭证。
// 网络、数据库、配置等技术故障原样返回，不将未知错误一律替换成 ErrInvalidCredential。
// 这些是具体实现必须遵守的契约，接口声明不会自动检查字段或转换错误。
//
// ctx 必须非 nil；认证器的外部调用应响应其取消与截止时间，并设置有限的调用预算。
// 取消或超时返回对应的 Context 错误或下层原始错误，不判定为凭证无效，不返回成功身份。
// 实现应支持并发调用，单次凭证、身份和执行状态使用局部变量，不保存在共享请求字段中。
type Authenticator interface {
	Authenticate(ctx context.Context, credential Credential) (Principal, error)
}
