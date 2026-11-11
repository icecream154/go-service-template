package scalar_test

import (
	"errors"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/scalar"
)

// TestParseBoolText 验证布尔文本只接受精确拼写，并将其他输入归类为无效值。
func TestParseBoolText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    bool
		wantErr error
	}{
		// 用精确拼写的 true 验证真值解析成功。
		{name: "true literal", input: "true", want: true},
		// 用精确拼写的 false 验证合法假值可以成功解析。
		{name: "false literal", input: "false", want: false},
		// 用空串代表缺失文本，验证它不会被当成合法假值。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 用常见的数字真值验证接口拒绝以 1 表示 true。
		{name: "numeric true", input: "1", wantErr: check.ErrInvalidValue},
		// 用常见的数字假值验证接口拒绝以 0 表示 false。
		{name: "numeric false", input: "0", wantErr: check.ErrInvalidValue},
		// 用大写真值验证解析保留大小写约束。
		{name: "uppercase true", input: "TRUE", wantErr: check.ErrInvalidValue},
		// 用首字母大写的假值验证解析拒绝大小写变体。
		{name: "mixed-case false", input: "False", wantErr: check.ErrInvalidValue},
		// 用单字母真值别名验证接口要求完整拼写。
		{name: "short true alias", input: "t", wantErr: check.ErrInvalidValue},
		// 用单字母假值别名验证接口要求完整拼写。
		{name: "short false alias", input: "f", wantErr: check.ErrInvalidValue},
		// 给合法真值加前导空格，验证原始文本按精确匹配处理。
		{name: "leading whitespace", input: " true", wantErr: check.ErrInvalidValue},
		// 给合法假值加尾部制表符，验证尾部空白同样导致拒绝。
		{name: "trailing whitespace", input: "false\t", wantErr: check.ErrInvalidValue},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := scalar.ParseBoolText(test.input)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseBoolText(%q) error = %v, want %v", test.input, err, test.wantErr)
			}
			if test.wantErr == nil && got != test.want {
				t.Errorf("ParseBoolText(%q) = %t, want %t", test.input, got, test.want)
			}
		})
	}
}
