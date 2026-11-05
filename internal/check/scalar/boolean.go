package scalar

import "go-service-template/internal/check"

// ParseBoolText 只接受区分大小写的 true、false；不接受 1、0、大小写别名或空白。
// 已经是 bool 的值无需再次验证，缺失与 false 的区别由调用方输入契约表达。
func ParseBoolText(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, check.NewViolation("boolean.parse", "must be true or false")
	}
}
