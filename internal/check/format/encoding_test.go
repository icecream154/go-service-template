package format_test

import (
	"bytes"
	"errors"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

func TestHex(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		// 空串对应零字节，验证最小长度也合法。
		{name: "empty text", input: ""},
		// 一段文本同时包含数字和两种字母大小写，验证基础语法接受它们。
		{name: "mixed case", input: "01abAF"},
		// 单个合法数字只有半个字节，应报告输入错误。
		{name: "odd digit count", input: "a", wantErr: check.ErrInvalidValue},
		// 偶数长度中的 g 验证字符检查独立于长度。
		{name: "invalid digit", input: "0g", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertEncodingError(t, format.Hex(test.input), test.wantErr)
		})
	}
}

func TestCanonicalHex(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		uppercase bool
		wantErr   error
	}{
		// 大写字母与数字验证大写规则接受规范文本。
		{name: "uppercase", input: "01AB", uppercase: true},
		// 小写字母验证相反规则也有合法表示。
		{name: "lowercase", input: "01ab"},
		// 只有数字时大小写要求仍可通过。
		{name: "digits only", input: "0123", uppercase: true},
		// 合法十六进制的小写字母违反大写规则。
		{name: "lowercase rejected", input: "01ab", uppercase: true, wantErr: check.ErrInvalidValue},
		// 合法十六进制的大写字母违反小写规则。
		{name: "uppercase rejected", input: "01AB", wantErr: check.ErrInvalidValue},
		// 大小写校验入口仍要拒绝非十六进制字符。
		{name: "invalid digit", input: "0g", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertEncodingError(t, format.CanonicalHex(test.input, test.uppercase), test.wantErr)
		})
	}
}

type decodeBytesCase struct {
	name    string
	input   string
	want    []byte
	wantErr error
}

func TestParseHex(t *testing.T) {
	tests := []struct {
		decodeBytesCase
		maximum int
	}{
		// 零上限配空文本验证可解码空数据。
		{decodeBytesCase: decodeBytesCase{name: "empty at zero limit"}, maximum: 0},
		// 两个字节恰好达到上限，且混合大小写按原数值解码。
		{decodeBytesCase: decodeBytesCase{name: "exact byte limit", input: "0aFF", want: []byte{0x0a, 0xff}}, maximum: 2},
		// 同一份数据的字节数超过新上限，应被拒绝。
		{decodeBytesCase: decodeBytesCase{name: "above byte limit", input: "0aFF", wantErr: check.ErrInvalidValue}, maximum: 1},
		// 足够的字节上限仍要求输入采用十六进制语法。
		{decodeBytesCase: decodeBytesCase{name: "invalid digit", input: "0g", wantErr: check.ErrInvalidValue}, maximum: 1},
		// 空文本隔离输入因素，验证负上限属于规则错误。
		{decodeBytesCase: decodeBytesCase{name: "negative limit", wantErr: check.ErrInvalidRule}, maximum: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := format.ParseHex(test.input, test.maximum)
			assertDecodedBytes(t, got, err, test.decodeBytesCase)
		})
	}
}

func TestParseBase64(t *testing.T) {
	tests := []struct {
		decodeBytesCase
		variant format.Base64Variant
		maximum int
	}{
		// 用含加号和两位填充的标准文本验证按实际单字节计算上限。
		{decodeBytesCase: decodeBytesCase{name: "standard with two padding bytes", input: "+w==", want: []byte{0xfb}}, variant: format.Base64Standard, maximum: 1},
		// 用单个等号填充的标准文本验证预估恰好为两个字节。
		{decodeBytesCase: decodeBytesCase{name: "standard with one padding byte", input: "Zm8=", want: []byte("fo")}, variant: format.Base64Standard, maximum: 2},
		// 完整的三字节块验证带填充变体可以接受无需填充的文本。
		{decodeBytesCase: decodeBytesCase{name: "complete standard block", input: "Zm9v", want: []byte("foo")}, variant: format.Base64Standard, maximum: 3},
		// 移除填充后，原始标准变体应保留标准字符表。
		{decodeBytesCase: decodeBytesCase{name: "raw standard", input: "+w", want: []byte{0xfb}}, variant: format.Base64StandardRaw, maximum: 1},
		// 用下划线替代斜杠验证 URL 字符表。
		{decodeBytesCase: decodeBytesCase{name: "padded URL", input: "_w==", want: []byte{0xff}}, variant: format.Base64URL, maximum: 1},
		// URL 原始变体接受同一字符表的无填充文本。
		{decodeBytesCase: decodeBytesCase{name: "raw URL", input: "_w", want: []byte{0xff}}, variant: format.Base64URLRaw, maximum: 1},
		// 空文本配零上限验证空数据边界。
		{decodeBytesCase: decodeBytesCase{name: "empty at zero limit"}, variant: format.Base64Standard},
		// 把一字节解码文本交给零上限，验证长度约束。
		{decodeBytesCase: decodeBytesCase{name: "above byte limit", input: "Zg==", wantErr: check.ErrInvalidValue}, variant: format.Base64Standard},
		// 在有效文本中插入换行，验证拒绝标准库可能忽略的空白。
		{decodeBytesCase: decodeBytesCase{name: "embedded newline", input: "Zg\n==", wantErr: check.ErrInvalidValue}, variant: format.Base64Standard, maximum: 10},
		// 省略标准变体需要的等号，验证填充形式错误。
		{decodeBytesCase: decodeBytesCase{name: "missing padding", input: "Zg", wantErr: check.ErrInvalidValue}, variant: format.Base64Standard, maximum: 2},
		// 给原始变体加等号，验证同样拒绝多余填充。
		{decodeBytesCase: decodeBytesCase{name: "padding on raw variant", input: "Zg==", wantErr: check.ErrInvalidValue}, variant: format.Base64StandardRaw, maximum: 3},
		// 末组低位非零但仍可映射为同一字节，应按非规范填充位拒绝。
		{decodeBytesCase: decodeBytesCase{name: "nonzero pad bits", input: "Zh==", wantErr: check.ErrInvalidValue}, variant: format.Base64Standard, maximum: 1},
		// 标准字符表的加号交给 URL 变体，验证字符表区别。
		{decodeBytesCase: decodeBytesCase{name: "wrong alphabet", input: "+w", wantErr: check.ErrInvalidValue}, variant: format.Base64URLRaw, maximum: 1},
		// 负的解码上限表示规则错误。
		{decodeBytesCase: decodeBytesCase{name: "negative limit", wantErr: check.ErrInvalidRule}, variant: format.Base64Standard, maximum: -1},
		// 未知枚举值表示编码规则配置错误。
		{decodeBytesCase: decodeBytesCase{name: "unknown variant", wantErr: check.ErrInvalidRule}, variant: format.Base64Variant(255)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := format.ParseBase64(test.input, test.variant, test.maximum)
			assertDecodedBytes(t, got, err, test.decodeBytesCase)
		})
	}
}

func assertDecodedBytes(t *testing.T, got []byte, err error, test decodeBytesCase) {
	t.Helper()
	assertEncodingError(t, err, test.wantErr)
	if test.wantErr == nil && !bytes.Equal(got, test.want) {
		t.Errorf("decode(%q) = %x, want %x", test.input, got, test.want)
	}
}

func assertEncodingError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
