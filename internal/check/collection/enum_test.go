package collection_test

import (
	"errors"
	"math"
	"slices"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/collection"
)

type enumCase struct {
	name      string
	value     any
	set       []any
	wantErr   error
	wantIndex *int
}

func TestOneOf(t *testing.T) {
	second := 1
	testEnum(t, collection.OneOf[any], []enumCase{
		// 把匹配值放在第二个候选位置，验证会继续查找成员。
		{name: "member", value: "active", set: []any{"draft", "active"}},
		// 只改变大小写，验证枚举按 Go 相等语义比较原值。
		{name: "case differs", value: "Active", set: []any{"active"}, wantErr: check.ErrInvalidValue},
		// 空允许集合没有候选值，应拒绝任意普通输入。
		{name: "empty allowed set", value: "active", wantErr: check.ErrInvalidValue},
		// nil 是可比较的接口值，允许集合显式包含它时应匹配。
		{name: "nil member", set: []any{"active", nil}},
		// 候选项中含有动态不可比较的切片，即使较早的候选项匹配也要报告规则错误。
		{name: "noncomparable rule value", value: "active", set: []any{"active", []int{1}}, wantErr: check.ErrInvalidRule, wantIndex: &second},
		// 用 NaN 验证待检查值必须支持自反相等。
		{name: "nonreflexive input", value: math.NaN(), set: []any{1.0}, wantErr: check.ErrInvalidValue},
	})
}

func TestNoneOf(t *testing.T) {
	second := 1
	testEnum(t, collection.NoneOf[any], []enumCase{
		// 输入未出现于禁止集合时应通过。
		{name: "allowed value", value: "active", set: []any{"archived", "disabled"}},
		// 输入命中第二个禁止项时应返回无效值。
		{name: "forbidden member", value: "disabled", set: []any{"archived", "disabled"}, wantErr: check.ErrInvalidValue},
		// 空禁止集合允许普通输入。
		{name: "empty forbidden set", value: "active"},
		// NaN 出现在候选集合中时，应报告规则错误及候选下标。
		{name: "nonreflexive rule value", value: 2.0, set: []any{1.0, math.NaN()}, wantErr: check.ErrInvalidRule, wantIndex: &second},
		// 输入的动态类型为切片，验证不可比较输入归为无效值。
		{name: "noncomparable input", value: []int{1}, set: []any{1}, wantErr: check.ErrInvalidValue},
	})
}

func TestAllOneOf(t *testing.T) {
	first, second := 0, 1
	tests := []struct {
		name      string
		values    []any
		allowed   []any
		wantErr   error
		wantIndex *int
	}{
		// 保留重复值和输入顺序，验证该方法逐项检查成员关系。
		{name: "all members including duplicates", values: []any{"b", "a", "b"}, allowed: []any{"a", "b"}},
		// 没有待检查项时，空输入和空允许集合应通过。
		{name: "empty inputs"},
		// 有输入且允许集合为空时，应在首项报告失败。
		{name: "nonempty input with no candidates", values: []any{"a"}, wantErr: check.ErrInvalidValue, wantIndex: &first},
		// 连续两个不匹配值，验证返回较早的原始输入下标。
		{name: "first nonmember", values: []any{"a", "c", "b"}, allowed: []any{"a"}, wantErr: check.ErrInvalidValue, wantIndex: &second},
		// 在有效项后加入动态不可比较的值，验证输入错误带有该项的下标。
		{name: "noncomparable input", values: []any{1, []int{2}}, allowed: []any{1}, wantErr: check.ErrInvalidValue, wantIndex: &second},
		// 即使没有待检查项，候选集合中的 NaN 仍应被识别为规则错误。
		{name: "invalid rule with empty inputs", allowed: []any{math.NaN()}, wantErr: check.ErrInvalidRule, wantIndex: &first},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.values)
			err := collection.AllOneOf(test.values, test.allowed)
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

func testEnum(t *testing.T, validate func(any, []any) error, tests []enumCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validate(test.value, test.set)
			assertCollectionError(t, err, test.wantErr)
			if test.wantIndex != nil {
				assertCollectionIndex(t, err, *test.wantIndex)
			}
		})
	}
}

func assertCollectionError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func assertCollectionIndex(t *testing.T, err error, want int) {
	t.Helper()
	var indexed *check.IndexError
	if !errors.As(err, &indexed) {
		t.Fatalf("error = %v, want *check.IndexError", err)
	}
	if indexed.Index != want {
		t.Errorf("index = %d, want %d", indexed.Index, want)
	}
}
