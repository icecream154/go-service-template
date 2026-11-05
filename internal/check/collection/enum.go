package collection

import "go-service-template/internal/check"

// OneOf 按 Go == 语义检查值属于 allowed；空 allowed 表示没有任何允许值。
// 不改变字符串大小写或解析别名；规则集合不支持不可比较动态值或 NaN。
func OneOf[T comparable](value T, allowed []T) error {
	if err := enumRuleValues(allowed); err != nil {
		return err
	}
	if err := comparableValue(value); err != nil {
		return err
	}
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return check.NewViolation("collection.one_of", "must belong to the allowed set")
}

// NoneOf 检查值不属于 forbidden；空 forbidden 表示没有禁止值。
func NoneOf[T comparable](value T, forbidden []T) error {
	if err := enumRuleValues(forbidden); err != nil {
		return err
	}
	if err := comparableValue(value); err != nil {
		return err
	}
	for _, candidate := range forbidden {
		if value == candidate {
			return check.NewViolation("collection.none_of", "must not belong to the forbidden set")
		}
	}
	return nil
}

// AllOneOf 检查每个元素均属于 allowed，返回首个失败下标；空 values 通过。
// 不去重、不修改顺序；要求至少一个值时需另外组合 NonEmpty。
func AllOneOf[T comparable](values []T, allowed []T) error {
	if err := enumRuleValues(allowed); err != nil {
		return err
	}
	set := make(map[T]struct{}, len(allowed))
	for _, candidate := range allowed {
		set[candidate] = struct{}{}
	}
	for i, value := range values {
		if err := comparableValue(value); err != nil {
			return &check.IndexError{Index: i, Err: err}
		}
		if _, exists := set[value]; !exists {
			return &check.IndexError{Index: i, Err: check.NewViolation("collection.one_of", "must belong to the allowed set")}
		}
	}
	return nil
}

func enumRuleValues[T comparable](values []T) error {
	for i, value := range values {
		if err := comparableValue(value); err != nil {
			return &check.IndexError{Index: i, Err: check.NewRuleError("collection.enum", "rule values must support comparable, reflexive equality")}
		}
	}
	return nil
}
