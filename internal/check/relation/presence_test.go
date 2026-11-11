package relation_test

import (
	"errors"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/relation"
)

type presenceCase struct {
	name    string
	present []bool
	wantErr error
}

func TestAtLeastOne(t *testing.T) {
	testPresence(t, relation.AtLeastOne, []presenceCase{
		// 缺少任何存在性条件时，应报告至少需要一个值。
		{name: "no conditions", wantErr: check.ErrInvalidValue},
		// 两个条件都为 false，验证输入非空仍需要至少一个值。
		{name: "all absent", present: []bool{false, false}, wantErr: check.ErrInvalidValue},
		// 把唯一的 true 放在末尾，验证前面的 false 可以跳过。
		{name: "one present", present: []bool{false, true}},
		// 两个 true 验证该规则只约束存在数量的下限。
		{name: "multiple present", present: []bool{true, true}},
	})
}

func TestAtMostOne(t *testing.T) {
	testPresence(t, relation.AtMostOne, []presenceCase{
		// 没有条件时，存在数量为零，符合上限。
		{name: "no conditions"},
		// 仅提供 false 条件，验证 false 不计入存在数量。
		{name: "all absent", present: []bool{false, false}},
		// 一个 true 正好达到允许的上限。
		{name: "one present", present: []bool{false, true, false}},
		// 两个 true 之间夹一个 false，验证超过上限时返回无效值。
		{name: "multiple present", present: []bool{true, false, true}, wantErr: check.ErrInvalidValue},
	})
}

func TestExactlyOne(t *testing.T) {
	testPresence(t, relation.ExactlyOne, []presenceCase{
		// 没有条件时计数为零，验证缺少唯一的值会失败。
		{name: "no conditions", wantErr: check.ErrInvalidValue},
		// 有两个条件且均为 false，验证条件本身的数量不影响计数。
		{name: "all absent", present: []bool{false, false}, wantErr: check.ErrInvalidValue},
		// 只有中间条件为 true，验证其余条件为 false 时仍可通过。
		{name: "one present", present: []bool{false, true, false}},
		// 两个非相邻条件为 true，验证重复提供时返回无效值。
		{name: "multiple present", present: []bool{true, false, true}, wantErr: check.ErrInvalidValue},
	})
}

func TestAllOrNone(t *testing.T) {
	testPresence(t, relation.AllOrNone, []presenceCase{
		// 无条件是允许的空组合。
		{name: "no conditions"},
		// 两个 false 验证允许所有值同时缺失。
		{name: "all absent", present: []bool{false, false}},
		// 两个 true 验证允许所有值同时提供。
		{name: "all present", present: []bool{true, true}},
		// 从 true 开始再出现 false，验证部分缺失时失败。
		{name: "present then absent", present: []bool{true, false}, wantErr: check.ErrInvalidValue},
		// 交换出现顺序，验证从 false 开始也能检测混合状态。
		{name: "absent then present", present: []bool{false, true}, wantErr: check.ErrInvalidValue},
	})
}

func TestRequiredIf(t *testing.T) {
	tests := []struct {
		name      string
		condition bool
		present   bool
		wantErr   error
	}{
		// 条件未触发且值缺失，验证此时允许省略。
		{name: "optional and absent", condition: false, present: false},
		// 条件未触发时主动提供值也应通过。
		{name: "optional and present", condition: false, present: true},
		// 条件触发且已经提供值，验证满足必填要求。
		{name: "required and present", condition: true, present: true},
		// 条件触发但值缺失，是唯一应拒绝的组合。
		{name: "required and absent", condition: true, present: false, wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRelationError(t, relation.RequiredIf(test.condition, test.present), test.wantErr)
		})
	}
}

func TestNotNil(t *testing.T) {
	var nilSlice []string
	emptySlice := []string{}
	tests := []struct {
		name    string
		value   *[]string
		wantErr error
	}{
		// 指针本身为 nil 时应报告无效值。
		{name: "nil pointer", wantErr: check.ErrInvalidValue},
		// 非 nil 指针指向切片零值，验证检查停留在指针这一层。
		{name: "pointer to nil slice", value: &nilSlice},
		// 非 nil 指针指向空集合，验证空长度不影响指针检查。
		{name: "pointer to empty slice", value: &emptySlice},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRelationError(t, relation.NotNil(test.value), test.wantErr)
		})
	}
}

func testPresence(t *testing.T, validate func(...bool) error, tests []presenceCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRelationError(t, validate(test.present...), test.wantErr)
		})
	}
}

func assertRelationError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
