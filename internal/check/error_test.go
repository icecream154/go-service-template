package check_test

import (
	"errors"
	"strings"
	"testing"

	"go-service-template/internal/check"
)

type errorMessageCase struct {
	name      string
	value     error
	wantParts []string
	hidden    string
}

func TestViolationError(t *testing.T) {
	testErrorMessage(t, []errorMessageCase{
		// 给规则和原因不同的文本，验证展示文案保留两者。
		{name: "rule and reason", value: &check.Violation{Rule: "example.range", Reason: "must be positive"}, wantParts: []string{"example.range", "must be positive"}},
		// 带类型的 nil 接收者仍能调用 Error，应返回占位文本。
		{name: "nil receiver", value: (*check.Violation)(nil), wantParts: []string{"<nil>"}},
	})
}

type errorMatchCase struct {
	name   string
	value  error
	target error
	want   bool
}

func TestViolationIs(t *testing.T) {
	violation := &check.Violation{Rule: "example.range", Reason: "must be positive"}
	testErrorMatch(t, []errorMatchCase{
		// 有效规则下的坏数据应匹配输入错误类别。
		{name: "invalid value", value: violation, target: check.ErrInvalidValue, want: true},
		// 相同错误匹配规则错误时应失败，验证两个类别彼此独立。
		{name: "invalid rule", value: violation, target: check.ErrInvalidRule},
		// nil 接收者没有失败原因，验证它不匹配输入错误。
		{name: "nil receiver", value: (*check.Violation)(nil), target: check.ErrInvalidValue},
	})
}

func TestRuleErrorError(t *testing.T) {
	testErrorMessage(t, []errorMessageCase{
		// 保留规则名和配置失败原因，便于定位错误规则。
		{name: "rule and reason", value: &check.RuleError{Rule: "example.range", Reason: "bounds are reversed"}, wantParts: []string{"example.range", "bounds are reversed"}},
		// nil 规则错误仍能生成占位文本。
		{name: "nil receiver", value: (*check.RuleError)(nil), wantParts: []string{"<nil>"}},
	})
}

func TestRuleErrorIs(t *testing.T) {
	ruleErr := &check.RuleError{Rule: "example.range", Reason: "bounds are reversed"}
	testErrorMatch(t, []errorMatchCase{
		// 无效的规则配置应匹配规则错误类别。
		{name: "invalid rule", value: ruleErr, target: check.ErrInvalidRule, want: true},
		// 同一错误匹配输入错误时应失败，验证它不会被当作字段输入错误。
		{name: "invalid value", value: ruleErr, target: check.ErrInvalidValue},
		// nil 接收者应安全返回且不匹配规则错误。
		{name: "nil receiver", value: (*check.RuleError)(nil), target: check.ErrInvalidRule},
	})
}

func TestIndexErrorError(t *testing.T) {
	cause := errors.New("original error")
	testErrorMessage(t, []errorMessageCase{
		// 使用两位数下标和自定义原因，验证位置与原因均出现在展示文案中。
		{name: "index and cause", value: &check.IndexError{Index: 12, Err: cause}, wantParts: []string{"item 12", "original error"}},
		// 内部原因缺失时，应仍可显示首项位置。
		{name: "missing cause", value: &check.IndexError{Index: 0}, wantParts: []string{"item 0"}},
		// nil 接收者应安全地生成占位文本。
		{name: "nil receiver", value: (*check.IndexError)(nil), wantParts: []string{"<nil>"}},
	})
}

type errorChainCase struct {
	name         string
	value        error
	wantCause    error
	wantCategory error
}

func TestIndexErrorUnwrap(t *testing.T) {
	cause := check.NewViolation("example.item", "item rejected")
	testErrorChain(t, []errorChainCase{
		// 包装输入错误后，解包应保留原始对象和输入错误类别。
		{name: "wrapped violation", value: &check.IndexError{Index: 2, Err: cause}, wantCause: cause, wantCategory: check.ErrInvalidValue},
		// 有下标且没有内部原因时，解包应返回 nil。
		{name: "missing cause", value: &check.IndexError{Index: 2}},
		// nil 接收者的解包也应安全返回 nil。
		{name: "nil receiver", value: (*check.IndexError)(nil)},
	})
}

func TestKeyErrorError(t *testing.T) {
	const key = "example-key-marker"
	cause := errors.New("original error")
	testErrorMessage(t, []errorMessageCase{
		// 用可辨识的键验证文案保留原始原因，同时省略 map 键。
		{name: "cause without key", value: &check.KeyError{Key: key, Err: cause}, wantParts: []string{"map entry", "original error"}, hidden: key},
		// 原因缺失时应生成占位文案，键仍只保留在结构化字段中。
		{name: "missing cause", value: &check.KeyError{Key: key}, wantParts: []string{"map entry"}, hidden: key},
		// nil 接收者应安全返回占位文本。
		{name: "nil receiver", value: (*check.KeyError)(nil), wantParts: []string{"<nil>"}},
	})
}

func TestKeyErrorUnwrap(t *testing.T) {
	cause := check.NewRuleError("example.key", "rule rejected")
	testErrorChain(t, []errorChainCase{
		// 用规则错误验证加上键后仍能取得原始对象及其分类。
		{name: "wrapped rule error", value: &check.KeyError{Key: "entry", Err: cause}, wantCause: cause, wantCategory: check.ErrInvalidRule},
		// 键存在而原因缺失时，解包应返回 nil。
		{name: "missing cause", value: &check.KeyError{Key: "entry"}},
		// nil 接收者也应安全解包为空。
		{name: "nil receiver", value: (*check.KeyError)(nil)},
	})
}

type errorConstructorCase struct {
	name   string
	rule   string
	reason string
}

func TestNewViolation(t *testing.T) {
	tests := []errorConstructorCase{
		// 用带空白的字段验证构造器保存调用方提供的原值。
		{name: "preserves fields", rule: " example.value ", reason: " invalid value "},
		// 空字段仍可封装，验证构造器只负责创建错误。
		{name: "empty fields"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := check.NewViolation(test.rule, test.reason)
			var violation *check.Violation
			if !errors.As(err, &violation) || violation == nil {
				t.Fatalf("error = %v, want nonnil *check.Violation", err)
			}
			assertConstructedError(t, err, check.ErrInvalidValue, violation.Rule, violation.Reason, test)
		})
	}
}

func TestNewRuleError(t *testing.T) {
	tests := []errorConstructorCase{
		// 用带空白的字段验证构造器原样保留规则定义问题。
		{name: "preserves fields", rule: " example.rule ", reason: " invalid rule "},
		// 空字段也能构造规则错误，验证错误分类与内容无关。
		{name: "empty fields"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := check.NewRuleError(test.rule, test.reason)
			var ruleErr *check.RuleError
			if !errors.As(err, &ruleErr) || ruleErr == nil {
				t.Fatalf("error = %v, want nonnil *check.RuleError", err)
			}
			assertConstructedError(t, err, check.ErrInvalidRule, ruleErr.Rule, ruleErr.Reason, test)
		})
	}
}

func testErrorMessage(t *testing.T, tests []errorMessageCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.value.Error()
			for _, part := range test.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("Error() = %q, want it to include %q", got, part)
				}
			}
			if test.hidden != "" && strings.Contains(got, test.hidden) {
				t.Errorf("Error() = %q, want key %q omitted", got, test.hidden)
			}
		})
	}
}

func testErrorMatch(t *testing.T, tests []errorMatchCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := errors.Is(test.value, test.target); got != test.want {
				t.Errorf("errors.Is(%v, %v) = %t, want %t", test.value, test.target, got, test.want)
			}
		})
	}
}

func testErrorChain(t *testing.T, tests []errorChainCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := errors.Unwrap(test.value); got != test.wantCause {
				t.Errorf("errors.Unwrap(%v) = %v, want %v", test.value, got, test.wantCause)
			}
			if test.wantCategory != nil && !errors.Is(test.value, test.wantCategory) {
				t.Errorf("error = %v, want category %v", test.value, test.wantCategory)
			}
		})
	}
}

func assertConstructedError(t *testing.T, err, wantCategory error, rule, reason string, test errorConstructorCase) {
	t.Helper()
	if rule != test.rule || reason != test.reason {
		t.Errorf("fields = (%q, %q), want (%q, %q)", rule, reason, test.rule, test.reason)
	}
	if !errors.Is(err, wantCategory) {
		t.Errorf("error = %v, want category %v", err, wantCategory)
	}
}
