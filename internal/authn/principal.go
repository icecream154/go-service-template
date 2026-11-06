// Package authn 定义通用身份、凭证、认证器接口及认证错误，不依赖传输协议或具体业务。
// 具体认证器验证凭证后返回 Principal；本包的类型声明本身不执行认证或权限判断。
package authn

// PrincipalKind 表示调用方的主体大类；同一大类下的不同身份体系由 Namespace 区分。
// 例如普通用户和员工均使用 user，内部服务和合作方服务均使用 service，无须另增 Kind。
// 只有现有大类无法表达新主体时，才在本包统一增加常量，例如将设备作为独立主体大类。
// 扩展时同步身份契约、受影响认证器的允许值与生成逻辑，以及使用方的类别判断；
// 新增常量不会自动实现对应认证能力，不支持该类别的认证器仍应拒绝。
// 当前有效值只有本包声明的 user、service；虽然底层 string 允许业务构造其他值，
// 但未纳入公共契约的自定义值不能作为成功认证的类别，零值也不表示匿名认证成功。
type PrincipalKind string

const (
	// PrincipalKindUser 表示用户身份，包括内部用户和尚未注册的外部用户。
	PrincipalKindUser PrincipalKind = "user"
	// PrincipalKindService 表示服务以自身身份调用，不隐含代表任意用户的权限。
	PrincipalKindService PrincipalKind = "service"
)

// Principal 表示认证器确认的调用方，Kind、Namespace 和 SubjectID 共同确定一个身份。
// 它不要求主体已有内部用户 ID，也不保存完整业务模型、原始凭证或权限判断结果。
// 成功结果按值传递，接收方按只读约定使用；零值不是有效身份。
// 直接构造本结构不代表完成认证，字段合法性和来源可信性由认证器保证。
type Principal struct {
	// Kind 必填，当前使用 PrincipalKindUser 或 PrincipalKindService；新增大类按上述约定统一扩展。
	// 认证器还须确认 Kind 与项目采用的 Namespace 匹配，字段的 Go 类型不会自动约束组合。
	Kind PrincipalKind
	// Namespace 必填且非空白，表示业务约定的完整身份命名空间，例如 member 或 internalServices。
	// 业务可为不同身份体系定义 member、staff、partners 等名称，无须修改 PrincipalKind。
	// 本项目的具体值集中声明在 app/identity：user_namespace.go 使用 UserNamespace 前缀，
	// service_namespace.go 使用 ServiceNamespace 前缀；认证实现和使用方引用相应常量。
	// 例如 identity.UserNamespaceMember 与 PrincipalKindUser 配合使用，不能搭配 PrincipalKindService。
	// 本包保持通用 string 字段，不反向导入项目枚举；上述其他名称仅为示例，不表示均已登记。
	// 它划分主体标识所属的范围，不表示角色或权限等级；同一身份的权限变化不改变命名空间。
	// 同一身份体系新增主体只需使用新的 SubjectID，不为每个主体单独增加 Namespace。
	Namespace string
	// SubjectID 必填且非空白，是该类别和命名空间内的主体标识，例如 "123" 或 "orderService"。
	// 使用字符串兼容内部数字 ID、外部账户及服务标识；本包不解析数字、不修剪或转换大小写。
	// Namespace 和 SubjectID 均按原文精确比较，具体规范化规则由对应身份来源明确约定。
	SubjectID string
}
