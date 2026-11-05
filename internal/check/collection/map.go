package collection

import (
	"sort"

	"go-service-template/internal/check"
)

// MapLengthInRange 检查 map 的条目数量闭区间，nil map 的数量为 0。
func MapLengthInRange[K comparable, V any](values map[K]V, minimum, maximum int) error {
	return countInRange(len(values), minimum, maximum)
}

// MapNotNil 只拒绝 nil map；非 nil 空 map 合法。
func MapNotNil[K comparable, V any](values map[K]V) error {
	if values == nil {
		return check.NewViolation("collection.not_nil", "map must not be nil")
	}
	return nil
}

// RequireKeys 检查字符串键是否存在，不把键对应的零值当作缺失。
// 按 required 的传入顺序返回首个缺失键，空 required 通过；不修改输入 map。
func RequireKeys[V any](values map[string]V, required ...string) error {
	for _, key := range required {
		if _, exists := values[key]; !exists {
			return &check.KeyError{Key: key, Err: check.NewViolation("collection.required_key", "required key is missing")}
		}
	}
	return nil
}

// AllowedKeys 检查 map 不包含允许集合以外的键；空 allowed 只允许空 map。
// 仅对键的副本排序，按字典序返回首个失败键，检查结果不依赖 map 的遍历顺序。
func AllowedKeys[V any](values map[string]V, allowed ...string) error {
	keys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		keys[key] = struct{}{}
	}
	for _, key := range sortedKeys(values) {
		if _, exists := keys[key]; !exists {
			return &check.KeyError{Key: key, Err: check.NewViolation("collection.allowed_key", "key is not allowed")}
		}
	}
	return nil
}

// EachMapValue 按键字典序检查 map 的值，保留首个失败键与原始错误。
// 空 map 通过；validate 必须为只读纯检查函数，不能在遍历期间修改 map。
func EachMapValue[V any](values map[string]V, validate func(V) error) error {
	if validate == nil {
		return check.NewRuleError("collection.each_map_value", "validation function must not be nil")
	}
	for _, key := range sortedKeys(values) {
		if err := validate(values[key]); err != nil {
			return &check.KeyError{Key: key, Err: err}
		}
	}
	return nil
}

// EachMapEntry 按键字典序检查键和值，返回首个失败条目。
// validate 只读取参数，不应修改 map 或访问外部状态。
func EachMapEntry[V any](values map[string]V, validate func(string, V) error) error {
	if validate == nil {
		return check.NewRuleError("collection.each_map_entry", "validation function must not be nil")
	}
	for _, key := range sortedKeys(values) {
		if err := validate(key, values[key]); err != nil {
			return &check.KeyError{Key: key, Err: err}
		}
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
