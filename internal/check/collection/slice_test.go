package collection_test

import (
	"errors"
	"math"
	"slices"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/collection"
)

func TestSliceLengthInRange(t *testing.T) {
	tests := []struct {
		name    string
		values  []int
		minimum int
		maximum int
		wantErr error
	}{
		// nil 切片长度为零，验证它满足仅允许零项的规则。
		{name: "nil at zero length"},
		// 已分配的空切片也按零项计数。
		{name: "allocated empty slice at zero length", values: []int{}},
		// 长度为一时恰好等于下限，验证闭区间下端点。
		{name: "lower endpoint", values: []int{1}, minimum: 1, maximum: 2},
		// 长度为二时恰好等于上限，验证闭区间上端点。
		{name: "upper endpoint", values: []int{1, 2}, minimum: 1, maximum: 2},
		// 空切片低于正下限，应被识别为输入错误。
		{name: "below minimum", values: []int{}, minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 两项比上限一多一项，验证上侧越界被拒绝。
		{name: "above maximum", values: []int{1, 2}, maximum: 1, wantErr: check.ErrInvalidValue},
		// 上下限均非负且顺序颠倒，验证返回规则错误。
		{name: "reversed bounds", values: []int{1}, minimum: 2, maximum: 1, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCollectionError(t, collection.SliceLengthInRange(test.values, test.minimum, test.maximum), test.wantErr)
		})
	}
}

type sliceCase struct {
	name    string
	values  []int
	wantErr error
}

func TestSliceNotNil(t *testing.T) {
	testSliceCheck(t, collection.SliceNotNil[int], []sliceCase{
		// nil 切片应作为未分配的输入被拒绝。
		{name: "nil slice", wantErr: check.ErrInvalidValue},
		// 空但已分配的切片通过，验证此规则只检查 nil。
		{name: "allocated empty slice", values: []int{}},
		// 含一项的切片是正常的非 nil 输入。
		{name: "populated slice", values: []int{0}},
	})
}

func TestNonEmpty(t *testing.T) {
	testSliceCheck(t, collection.NonEmpty[int], []sliceCase{
		// nil 切片没有任何项，应被拒绝。
		{name: "nil slice", wantErr: check.ErrInvalidValue},
		// 已分配的空切片仍缺少元素，应被拒绝。
		{name: "allocated empty slice", values: []int{}, wantErr: check.ErrInvalidValue},
		// 零值元素计入长度，验证存在一项即可通过。
		{name: "one zero value item", values: []int{0}},
	})
}

func TestUnique(t *testing.T) {
	first, second, third := 0, 1, 2
	tests := []struct {
		name      string
		values    []any
		wantErr   error
		wantIndex *int
	}{
		// 空切片没有重复项，应通过。
		{name: "empty input"},
		// 整数和文本具有不同动态类型，验证相等判断保留类型及原顺序。
		{name: "distinct values and types", values: []any{2, "2", 1}},
		// 有两个重复位置时，验证报告原始序列中的第一个重复项。
		{name: "first repeated value", values: []any{"b", "a", "b", "a"}, wantErr: check.ErrInvalidValue, wantIndex: &third},
		// 用动态不可比较的切片作为第二项，验证返回输入错误及下标。
		{name: "noncomparable dynamic value", values: []any{1, []int{1}}, wantErr: check.ErrInvalidValue, wantIndex: &second},
		// 单个 NaN 也无法作为自反相等的键，验证在首项拒绝。
		{name: "nonreflexive value", values: []any{math.NaN()}, wantErr: check.ErrInvalidValue, wantIndex: &first},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.values)
			err := collection.Unique(test.values)
			assertCollectionError(t, err, test.wantErr)
			if test.wantIndex != nil {
				assertCollectionIndex(t, err, *test.wantIndex)
			}
			if test.wantErr == nil && !slices.Equal(test.values, before) {
				t.Errorf("values = %v, want original sequence %v", test.values, before)
			}
		})
	}
}

func TestUniqueBy(t *testing.T) {
	type entry struct {
		key   any
		label string
	}
	keyOf := func(value entry) any { return value.key }
	first, second := 0, 1
	tests := []struct {
		name      string
		values    []entry
		key       func(entry) any
		wantErr   error
		wantIndex *int
	}{
		// 空输入也需要有效的取键函数，缺少它应返回规则错误。
		{name: "nil key function", wantErr: check.ErrInvalidRule},
		// 配有有效函数的空输入应通过。
		{name: "empty input", key: keyOf},
		// 两项的其他字段相同而键不同，验证唯一性取决于派生键。
		{name: "different keys with equal labels", values: []entry{{key: 2, label: "same"}, {key: 1, label: "same"}}, key: keyOf},
		// 两项标签不同而键相同，应报告第二项是重复键。
		{name: "duplicate key with different labels", values: []entry{{key: 1, label: "first"}, {key: 1, label: "second"}}, key: keyOf, wantErr: check.ErrInvalidValue, wantIndex: &second},
		// 派生出动态不可比较的切片时，验证错误分类和首项下标。
		{name: "noncomparable derived key", values: []entry{{key: []int{1}}}, key: keyOf, wantErr: check.ErrInvalidValue, wantIndex: &first},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := collection.UniqueBy(test.values, test.key)
			assertCollectionError(t, err, test.wantErr)
			if test.wantIndex != nil {
				assertCollectionIndex(t, err, *test.wantIndex)
			}
		})
	}
}

func TestEach(t *testing.T) {
	rejected := errors.New("callback rejected item")
	rejectValue := 1
	second := 1
	tests := []struct {
		name        string
		values      []int
		nilCallback bool
		rejectValue *int
		wantErr     error
		wantIndex   *int
		wantVisited []int
	}{
		// 无元素时仍需提供回调，验证缺少回调属于规则错误。
		{name: "nil callback", nilCallback: true, wantErr: check.ErrInvalidRule},
		// nil 切片配有有效回调，应通过且不调用回调。
		{name: "nil input"},
		// 使用未排序且有重复的序列，验证每项按原顺序检查。
		{name: "all items in order", values: []int{3, 1, 3}, wantVisited: []int{3, 1, 3}},
		// 第二项触发自定义错误，验证原始错误和下标被保留且第三项未执行。
		{name: "first failing item", values: []int{3, 1, 2}, rejectValue: &rejectValue, wantErr: rejected, wantIndex: &second, wantVisited: []int{3, 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var visited []int
			var validate func(int) error
			if !test.nilCallback {
				validate = func(value int) error {
					visited = append(visited, value)
					if test.rejectValue != nil && value == *test.rejectValue {
						return rejected
					}
					return nil
				}
			}
			err := collection.Each(test.values, validate)
			assertCollectionError(t, err, test.wantErr)
			if test.wantIndex != nil {
				assertCollectionIndex(t, err, *test.wantIndex)
			}
			if !slices.Equal(visited, test.wantVisited) {
				t.Errorf("visited values = %v, want %v", visited, test.wantVisited)
			}
		})
	}
}

func testSliceCheck(t *testing.T, validate func([]int) error, tests []sliceCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCollectionError(t, validate(test.values), test.wantErr)
		})
	}
}
