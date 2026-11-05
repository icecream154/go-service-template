// Package collection 检查集合数量、元素和枚举关系，不改变集合内容或顺序。
package collection

import (
	"fmt"
	"reflect"

	"go-service-template/internal/check"
)

// SliceLengthInRange 检查切片长度闭区间；nil 与空切片的长度都为 0。
func SliceLengthInRange[T any](values []T, minimum, maximum int) error {
	return countInRange(len(values), minimum, maximum)
}

// SliceNotNil 只拒绝 nil 切片；非 nil 空切片合法，是否允许空集合需单独检查。
func SliceNotNil[T any](values []T) error {
	if values == nil {
		return check.NewViolation("collection.not_nil", "slice must not be nil")
	}
	return nil
}

// NonEmpty 检查切片至少包含一项；nil 和空切片均失败。
func NonEmpty[T any](values []T) error {
	if len(values) == 0 {
		return check.NewViolation("collection.nonempty", "must contain at least one item")
	}
	return nil
}

// Unique 按 Go == 语义检查切片元素唯一性，定位第一个重复项，不排序或去重。
// 空集合通过；NaN 等非自反键和接口中动态不可比较的输入值均返回输入检查错误。
func Unique[T comparable](values []T) error {
	return UniqueBy(values, func(value T) T { return value })
}

// UniqueBy 按只读 key 函数选出的可比较键判断唯一性，键的相等性采用 Go ==。
// key 不能为空，不应修改值或访问外部状态；失败下标从 0 开始。
func UniqueBy[T any, K comparable](values []T, key func(T) K) error {
	if key == nil {
		return check.NewRuleError("collection.unique", "key function must not be nil")
	}
	seen := make(map[K]struct{}, len(values))
	for i, value := range values {
		current := key(value)
		if err := comparableValue(current); err != nil {
			return &check.IndexError{Index: i, Err: err}
		}
		if _, exists := seen[current]; exists {
			return &check.IndexError{Index: i, Err: check.NewViolation("collection.unique", "must contain distinct keys")}
		}
		seen[current] = struct{}{}
	}
	return nil
}

// Each 按输入顺序检查每项，返回首个失败的下标并保留原始错误。
// nil 和空切片通过，调用方需要至少一项时另外组合 NonEmpty；validate 必须为只读纯检查函数。
func Each[T any](values []T, validate func(T) error) error {
	if validate == nil {
		return check.NewRuleError("collection.each", "validation function must not be nil")
	}
	for i, value := range values {
		if err := validate(value); err != nil {
			return &check.IndexError{Index: i, Err: err}
		}
	}
	return nil
}

func countInRange(count, minimum, maximum int) error {
	if minimum < 0 || minimum > maximum {
		return check.NewRuleError("collection.length", "count bounds must be nonnegative and ordered")
	}
	if count < minimum || count > maximum {
		return check.NewViolation("collection.length", fmt.Sprintf("must contain between %d and %d items inclusive", minimum, maximum))
	}
	return nil
}

// comparableValue 检查动态接口值可用于 ==，同时拒绝 NaN 等非自反键。
// 这里仅检查通用相等性，不反射 DTO、不推断字段存在性。
// 不可比较的待检查值属于输入问题；枚举候选集合中的同类问题由 enumRuleValues 转为规则错误。
func comparableValue[T comparable](value T) error {
	dynamic := reflect.ValueOf(value)
	if dynamic.IsValid() && !dynamic.Comparable() {
		return check.NewViolation("collection.equality", "dynamic key values must support equality")
	}
	if value != value {
		return check.NewViolation("collection.equality", "key values must support reflexive equality")
	}
	return nil
}
