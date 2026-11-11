package format_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

func TestValidJSON(t *testing.T) {
	defaults := format.JSONOptions{MaximumBytes: 128, MaximumDepth: 4}
	tests := []struct {
		name      string
		input     string
		options   format.JSONOptions
		wantErr   error
		wantKey   string
		wantIndex *int
	}{
		// 两个容器正好达到深度上限，验证标量本身不增加容器层数。
		{name: "exact depth", input: `[{"x":true}]`, options: format.JSONOptions{MaximumBytes: 32, MaximumDepth: 2}},
		// 深度为零时仍接受顶层标量，包含前后空白的长度正好达到上限。
		{name: "scalar at exact byte limit", input: " 0 ", options: format.JSONOptions{MaximumBytes: 3}},
		// 三字节汉字加引号恰好五字节，验证按原始字节计量。
		{name: "byte limit", input: `"中"`, options: format.JSONOptions{MaximumBytes: 5}},
		// 字节数超限时应返回输入错误。
		{name: "one byte above limit", input: `"中"`, options: format.JSONOptions{MaximumBytes: 4}, wantErr: check.ErrInvalidValue},
		// 标量深度可为零，容器在同一规则下应被拒绝。
		{name: "container at zero depth", input: "[]", options: format.JSONOptions{MaximumBytes: 8}, wantErr: check.ErrInvalidValue},
		// 多一个嵌套容器用于验证越过深度上限时保留父数组位置。
		{name: "over depth", input: "[[]]", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: 1}, wantErr: check.ErrInvalidValue, wantIndex: jsonTestIndex(0)},
		// 空输入隔离字节上限的配置错误。
		{name: "zero byte rule", input: "", options: format.JSONOptions{MaximumDepth: 1}, wantErr: check.ErrInvalidRule},
		// 负深度不构成可用的递归规则。
		{name: "negative depth", input: "0", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: -1}, wantErr: check.ErrInvalidRule},
		// 超出技术保护上限的配置应归类为规则错误。
		{name: "depth rule above technical maximum", input: "0", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: format.MaximumJSONDepth + 1}, wantErr: check.ErrInvalidRule},
		// 技术上限本身仍是有效配置。
		{name: "maximum supported depth rule", input: "0", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: format.MaximumJSONDepth}},
		// 有效 JSON 配未知类型选项，验证规则优先校验。
		{name: "unknown kind rule", input: "{}", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: 1, AllowedKinds: []format.JSONKind{"tuple"}}, wantErr: check.ErrInvalidRule},
		// 顶层数组在允许列表第二位，验证任一允许类型均可通过。
		{name: "allowed kind", input: "[]", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: 1, AllowedKinds: []format.JSONKind{format.JSONObject, format.JSONArray}}},
		// 内容可解析，顶层布尔类型仍应被对象限制拒绝。
		{name: "disallowed kind", input: "true", options: format.JSONOptions{MaximumBytes: 8, MaximumDepth: 1, AllowedKinds: []format.JSONKind{format.JSONObject}}, wantErr: check.ErrInvalidValue},
		// 两个各自合法的 JSON 值验证只接受一个完整值。
		{name: "trailing second value", input: "true false", options: defaults, wantErr: check.ErrInvalidValue},
		// 引号内放非法 UTF-8 字节，验证源文本编码检查。
		{name: "invalid UTF-8", input: "\"\xff\"", options: defaults, wantErr: check.ErrInvalidValue},
		// 不闭合的字符串验证未终止文本被拒绝。
		{name: "unterminated string", input: `"abc`, options: defaults, wantErr: check.ErrInvalidValue},
		// 末尾反斜线缺少转义目标，应拒绝截断的字符串。
		{name: "trailing escape", input: "\"abc\\", options: defaults, wantErr: check.ErrInvalidValue},
		// 截断的 Unicode 转义验证四位十六进制长度要求。
		{name: "short Unicode escape", input: `"\u12`, options: defaults, wantErr: check.ErrInvalidValue},
		// 足够长但含非法十六进制字符的转义仍应被拒绝。
		{name: "invalid Unicode digit", input: `"\u12G4"`, options: defaults, wantErr: check.ErrInvalidValue},
		// 单独的低代理项验证代理项必须成对出现。
		{name: "unpaired low surrogate", input: `"\uDC00"`, options: defaults, wantErr: check.ErrInvalidValue},
		// 高代理项之后接普通字符，验证后续项必须使用 Unicode 转义。
		{name: "unpaired high surrogate", input: `"\uD800abcdef"`, options: defaults, wantErr: check.ErrInvalidValue},
		// 高代理项之后虽有转义，普通码元仍无法组成代理对。
		{name: "wrong low surrogate", input: `"\uD800\u0041"`, options: defaults, wantErr: check.ErrInvalidValue},
		// 合法的代理对混用大小写十六进制字母，应被接受。
		{name: "valid surrogate pair", input: `"\ud83D\uDe00"`, options: defaults},
		// 转义的双引号验证字符串扫描跨过该字符继续处理。
		{name: "escaped quote", input: `"a\"b"`, options: defaults},
		// 普通单码元转义验证代理项规则保留基本多文种字符。
		{name: "non-surrogate Unicode escape", input: `"\u4E2d"`, options: defaults},
		// 转义和字面量键解码为同名，验证重复键按解码后文本识别。
		{name: "decoded duplicate key", input: `{"x":1,"\u0078":2}`, options: format.JSONOptions{MaximumBytes: 64, MaximumDepth: 1, RejectDuplicateKeys: true}, wantErr: check.ErrInvalidValue, wantKey: "x"},
		// 同名键位于两个对象作用域，验证独立对象可重复使用键。
		{name: "same key in separate objects", input: `[{"x":1},{"x":2}]`, options: format.JSONOptions{MaximumBytes: 64, MaximumDepth: 2, RejectDuplicateKeys: true}},
		// 没有第一个值时应报告语法错误。
		{name: "empty input", input: "", options: defaults, wantErr: check.ErrInvalidValue},
		// 对象键的位置放数字验证键必须使用字符串语法。
		{name: "nonstring object key", input: "{1:0}", options: defaults, wantErr: check.ErrInvalidValue},
		// 值位置放无效文本，验证错误保留所在对象键。
		{name: "invalid object item", input: `{"x":?}`, options: defaults, wantErr: check.ErrInvalidValue, wantKey: "x"},
		// 第二项放无效文本，验证错误保留从零开始的数组下标。
		{name: "invalid array item", input: "[0,?]", options: defaults, wantErr: check.ErrInvalidValue, wantIndex: jsonTestIndex(1)},
		// 对象开头之后立即结束，验证闭合符要求。
		{name: "incomplete object", input: "{", options: defaults, wantErr: check.ErrInvalidValue},
		// 数组开头之后立即结束，验证数组同样需要闭合符。
		{name: "incomplete array", input: "[", options: defaults, wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := format.ValidJSON([]byte(test.input), test.options)
			assertJSONError(t, err, test.wantErr)
			if test.wantKey != "" {
				var got *check.KeyError
				if !errors.As(err, &got) || got.Key != test.wantKey {
					t.Errorf("key error = %v, want key %q", got, test.wantKey)
				}
			}
			if test.wantIndex != nil {
				var got *check.IndexError
				if !errors.As(err, &got) || got.Index != *test.wantIndex {
					t.Errorf("index error = %v, want index %d", got, *test.wantIndex)
				}
			}
		})
	}
}

func TestParseJSON(t *testing.T) {
	options := format.JSONOptions{MaximumBytes: 128, MaximumDepth: 4}
	tests := []struct {
		name    string
		input   string
		want    any
		wantErr error
	}{
		// 嵌套树包含各标量类型和超出精确浮点范围的整数，验证解码类型与精度。
		{name: "nested JSON tree", input: `{"x":[9007199254740993,true,null,"中"]}`, want: map[string]any{"x": []any{json.Number("9007199254740993"), true, nil, "中"}}},
		// 默认允许重复键时，验证后出现的值覆盖前一个。
		{name: "last duplicate wins", input: `{"x":1,"x":2}`, want: map[string]any{"x": json.Number("2")}},
		// 空数组验证解码树保留容器类型与空值区别。
		{name: "empty array", input: "[]", want: []any{}},
		// 顶层 null 解码为 nil 并正常返回。
		{name: "null scalar", input: "null"},
		// 可读取的第一个值后还有数据，验证解析入口保留同样的语法约束。
		{name: "trailing value", input: "0 1", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := format.ParseJSON([]byte(test.input), options)
			assertJSONError(t, err, test.wantErr)
			if test.wantErr == nil && !reflect.DeepEqual(got, test.want) {
				t.Errorf("ParseJSON(%q) = %#v, want %#v", test.input, got, test.want)
			}
		})
	}
}

func assertJSONError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func jsonTestIndex(index int) *int {
	return &index
}
