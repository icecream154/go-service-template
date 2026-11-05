package check

import (
	"errors"
	"strconv"
)

var (
	// ErrInvalidValue 是输入错误的分类标识，表示数据不满足一条有效规则。
	// 例如要求数值在 1～10 之间，实际传入 20。具体问题由 Violation 携带，
	// 调用方使用 errors.Is(err, ErrInvalidValue) 判断类别，不比较错误文案。
	ErrInvalidValue = errors.New("value does not satisfy the check")
	// ErrInvalidRule 是规则定义错误的分类标识，表示检查条件本身无效。
	// 例如下限大于上限、正则模式无法编译。这是程序或装配问题，
	// 需要修正规则，不能一律作为待检查数据的问题返回给调用方。
	ErrInvalidRule = errors.New("invalid check rule")
)

// Violation 携带输入违反基础规则的具体原因，通过 Error 方法实现 error 接口。
// 调用方可用 errors.As(err, &violation) 读取 Rule、Reason；本类型不决定 HTTP 状态码，
// 也不携带业务错误码或 DTO 字段路径，向外如何映射由上层负责。
type Violation struct {
	// Rule 是稳定的基础规则名称，用于日志与问题识别，不是 HTTP 或业务错误码。
	Rule string
	// Reason 说明违反的约束，可以包含规则边界。调用方应只写可公开的规则说明，
	// 不拼接原始输入或敏感值；结构体及构造函数不会自动清洗文案。
	Reason string
}

// Error 生成展示或日志文案，程序分支应使用 errors.Is、errors.As 判断错误。
func (e *Violation) Error() string {
	if e == nil {
		// nil 接收者的保护只避免方法崩溃，不代表携带该指针的 error 接口为 nil。
		// 例如 var e *Violation; var err error = e，此时 err != nil。
		// 检查成功应直接 return nil，不返回类型化的 nil 错误指针。
		return "<nil>"
	}
	return e.Rule + ": " + e.Reason
}

// Is 声明当前错误属于 ErrInvalidValue 类别，供 errors.Is 自动调用。
// Violation 没有通过 Unwrap 包装该标识，而是通过此方法建立分类关系；
// 即使外层再包装 IndexError、KeyError，errors.Is 仍能沿错误链识别这个类别。
func (e *Violation) Is(target error) bool { return e != nil && target == ErrInvalidValue }

// RuleError 携带规则定义或参数错误，应在装配规则时修正，不归因于待检查数据。
// 它与 Violation 使用相同的字段结构，但表达不同的失败类别。
type RuleError struct {
	// Rule 标识哪条基础检查规则的定义无效，不是对外错误码。
	Rule string
	// Reason 说明规则参数或配置需要如何修正，不应包含原始输入或敏感配置值。
	Reason string
}

// Error 返回规则定义错误的说明；nil 接收者仅返回占位文案，不改变接口的 nil 语义。
func (e *RuleError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Rule + ": " + e.Reason
}

// Is 支持 errors.Is(err, ErrInvalidRule)，将规则定义错误与输入违反规则明确区分。
func (e *RuleError) Is(target error) bool { return e != nil && target == ErrInvalidRule }

// IndexError 在原始错误外补充集合位置，不重新定义失败类别。
// Err 可以是输入错误、规则错误或检查回调返回的其他错误，不能仅凭外层是 IndexError
// 就认定属于输入问题。原始原因通过 Unwrap 保留，位置通过 errors.As 读取。
type IndexError struct {
	// Index 是问题元素从 0 开始的下标，不是 HTTP 字段名或字段路径。
	Index int
	// Err 是被包装的原始错误，正常构造时应为非 nil；保留其类型与错误链。
	Err error
}

// Error 在原始文案前增加位置，例如 item 2: rune_count: ...。
// strconv.Itoa 将整数下标转成十进制文本；Err 为 nil 时只展示位置，作为容错处理。
func (e *IndexError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return "item " + strconv.Itoa(e.Index)
	}
	return "item " + strconv.Itoa(e.Index) + ": " + e.Err.Error()
}

// Unwrap 返回原始错误，使 errors.Is、errors.As 可以越过位置包装继续查找原因。
// 例如 errors.As 可以分别取得外层 IndexError 和内部 Violation。
func (e *IndexError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// KeyError 在原始错误外补充字符串键 map 中的问题位置，不重新定义失败类别。
// 与 IndexError 一样，调用方需要沿错误链判断实际原因，而不是只判断包装类型。
type KeyError struct {
	// Key 保存问题条目的键，调用方可通过 errors.As 取得后按需处理。
	// 默认 Error 文案不拼接此字段，避免自动输出可能包含业务数据的键。
	Key string
	// Err 是被包装的原始错误，正常构造时应为非 nil；不会被替换为新的错误类别。
	Err error
}

// Error 返回 map 条目检查失败的文案，不输出 Key 本身，也不改写内部错误文案。
// Err 为 nil 时返回占位说明，作为容错处理，不表示检查成功。
func (e *KeyError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return "map entry failed a check"
	}
	return "map entry: " + e.Err.Error()
}

// Unwrap 返回原始原因，保留 errors.Is 的分类判断和 errors.As 的类型提取能力。
func (e *KeyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewViolation 将规则名称和输入失败原因封装成 *Violation，并以 error 接口返回。
// 它只负责构造，不验证或清洗 rule、reason；reason 不应包含原始输入或敏感值。
// 统一返回 error 便于检查方法直接返回，具体字段仍可通过 errors.As 获取。
func NewViolation(rule, reason string) error { return &Violation{Rule: rule, Reason: reason} }

// NewRuleError 将无效规则参数或配置的原因封装成 *RuleError，并以 error 接口返回。
// 它只负责构造，不验证或清洗参数；调用方通过 errors.Is(err, ErrInvalidRule)
// 识别这类内部定义问题，不将其一律映射为用户输入错误。
func NewRuleError(rule, reason string) error { return &RuleError{Rule: rule, Reason: reason} }
