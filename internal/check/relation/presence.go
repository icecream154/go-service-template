// Package relation 组合调用方明确提供的存在性判断，不反射字段，也不把零值推断为缺失。
package relation

import "go-service-template/internal/check"

// AtLeastOne 要求至少一个条件为 true；没有条件时失败。
func AtLeastOne(present ...bool) error {
	for _, provided := range present {
		if provided {
			return nil
		}
	}
	return check.NewViolation("relation.at_least_one", "at least one value must be provided")
}

// AtMostOne 要求至多一个条件为 true；没有条件时通过。
func AtMostOne(present ...bool) error {
	count := 0
	for _, provided := range present {
		if provided {
			count++
			if count > 1 {
				return check.NewViolation("relation.at_most_one", "at most one value may be provided")
			}
		}
	}
	return nil
}

// ExactlyOne 要求恰好一个条件为 true；没有条件时失败。
func ExactlyOne(present ...bool) error {
	count := 0
	for _, provided := range present {
		if provided {
			count++
			if count > 1 {
				return check.NewViolation("relation.exactly_one", "exactly one value must be provided")
			}
		}
	}
	if count != 1 {
		return check.NewViolation("relation.exactly_one", "exactly one value must be provided")
	}
	return nil
}

// AllOrNone 要求条件全部为 true 或全部为 false；没有条件时通过。
func AllOrNone(present ...bool) error {
	if len(present) == 0 {
		return nil
	}
	first := present[0]
	for _, provided := range present[1:] {
		if provided != first {
			return check.NewViolation("relation.all_or_none", "values must be provided together or all omitted")
		}
	}
	return nil
}

// RequiredIf 在 condition 为 true 时要求 present 为 true，不读取或推断原始字段。
func RequiredIf(condition, present bool) error {
	if condition && !present {
		return check.NewViolation("relation.required_if", "value is required when the condition holds")
	}
	return nil
}

// NotNil 只检查指针是否为 nil，不检查其指向的零值、空字符串或空集合。
func NotNil[T any](value *T) error {
	if value == nil {
		return check.NewViolation("relation.not_nil", "pointer must not be nil")
	}
	return nil
}
