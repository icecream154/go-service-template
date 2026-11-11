package format_test

import (
	"errors"
	"strings"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

type identifierParseCase struct {
	name    string
	input   string
	want    [16]byte
	wantErr error
}

func TestParseUUID(t *testing.T) {
	testIdentifierParse(t, format.ParseUUID, []identifierParseCase{
		// 依次递增的字节用于验证各段的解码顺序。
		{name: "byte order", input: "00112233-4455-6677-8899-aabbccddeeff", want: [16]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}},
		// 大写字母应解码为相同的十六进制数值。
		{name: "uppercase accepted", input: "FF000000-0000-0000-0000-0000000000AF", want: [16]byte{0: 0xff, 15: 0xaf}},
		// 全零标识符验证基础结构解析允许零值。
		{name: "zero UUID", input: "00000000-0000-0000-0000-000000000000"},
		// 去掉连字符后仍有十六字节，验证长度及标准文本形式。
		{name: "no hyphens", input: "00112233445566778899aabbccddeeff", wantErr: check.ErrInvalidValue},
		// 保持 36 字节，只替换连字符，验证分隔符检查。
		{name: "wrong separator", input: "00112233_4455-6677-8899-aabbccddeeff", wantErr: check.ErrInvalidValue},
		// 保持结构正确，仅换一个非法数字验证解码错误。
		{name: "invalid digit", input: "00112233-4455-6677-8899-aabbccddeefg", wantErr: check.ErrInvalidValue},
		// 花括号中的地址内容合法，验证解析要求裸标准表示。
		{name: "braced text", input: "{00112233-4455-6677-8899-aabbccddeeff}", wantErr: check.ErrInvalidValue},
	})
}

type identifierTextCase struct {
	name    string
	input   string
	wantErr error
}

func TestCanonicalUUID(t *testing.T) {
	testIdentifierText(t, format.CanonicalUUID, []identifierTextCase{
		// 小写文本满足 UUID 的规范表示。
		{name: "lowercase", input: "00112233-4455-6677-8899-aabbccddeeff"},
		// 结构合法的大写文本验证规范检查要求小写。
		{name: "uppercase", input: "00112233-4455-6677-8899-AABBCCDDEEFF", wantErr: check.ErrInvalidValue},
		// 小写但结构不完整的文本验证规范入口仍检查基础语法。
		{name: "invalid format", input: "abcdef", wantErr: check.ErrInvalidValue},
	})
}

type identifierAllowedCase[T any] struct {
	name    string
	input   string
	allowed []T
	wantErr error
}

func TestUUIDVersionOneOf(t *testing.T) {
	testIdentifierAllowed(t, format.UUIDVersionOneOf, []identifierAllowedCase[uint8]{
		// 版本 4 位于允许列表第二项，验证遍历整个集合。
		{name: "second allowed version", input: "00112233-4455-4677-8899-aabbccddeeff", allowed: []uint8{1, 4}},
		// 版本 8 是允许配置的上边界。
		{name: "maximum version", input: "00112233-4455-8677-8899-aabbccddeeff", allowed: []uint8{8}},
		// 全零 UUID 的版本位为零，验证它仍需满足版本集合。
		{name: "zero UUID", input: "00000000-0000-0000-0000-000000000000", allowed: []uint8{1, 8}, wantErr: check.ErrInvalidValue},
		// 合法文本配空规则，验证允许版本必须显式提供。
		{name: "empty allowed versions", input: "00112233-4455-4677-8899-aabbccddeeff", wantErr: check.ErrInvalidRule},
		// 版本零低于规则下限。
		{name: "rule below minimum", input: "00112233-4455-4677-8899-aabbccddeeff", allowed: []uint8{0}, wantErr: check.ErrInvalidRule},
		// 版本九超过上限，匹配项在前也须拒绝整套规则。
		{name: "rule above maximum", input: "00112233-4455-4677-8899-aabbccddeeff", allowed: []uint8{4, 9}, wantErr: check.ErrInvalidRule},
		// 规则有效时，格式错误应沿入口返回。
		{name: "invalid UUID", input: "4", allowed: []uint8{4}, wantErr: check.ErrInvalidValue},
	})
}

func TestUUIDVariantOneOf(t *testing.T) {
	testIdentifierAllowed(t, format.UUIDVariantOneOf, []identifierAllowedCase[format.UUIDVariant]{
		// 最高位为零的第九字节验证 NCS 布局。
		{name: "NCS", input: "00112233-4455-4677-7899-aabbccddeeff", allowed: []format.UUIDVariant{format.UUIDVariantNCS}},
		// 第九字节以二进制 10 开头，验证匹配 RFC 选项。
		{name: "RFC", input: "00112233-4455-4677-8899-aabbccddeeff", allowed: []format.UUIDVariant{format.UUIDVariantNCS, format.UUIDVariantRFC}},
		// 第九字节以二进制 110 开头，验证 Microsoft 布局。
		{name: "Microsoft", input: "00112233-4455-4677-c899-aabbccddeeff", allowed: []format.UUIDVariant{format.UUIDVariantMicrosoft}},
		// 第九字节以二进制 111 开头，验证保留变体。
		{name: "reserved", input: "00112233-4455-4677-e899-aabbccddeeff", allowed: []format.UUIDVariant{format.UUIDVariantReserved}},
		// 合法 RFC UUID 交给仅允许 NCS 的规则，应被拒绝。
		{name: "variant excluded", input: "00112233-4455-4677-8899-aabbccddeeff", allowed: []format.UUIDVariant{format.UUIDVariantNCS}, wantErr: check.ErrInvalidValue},
		// 空集合无法定义允许的变体，验证规则错误。
		{name: "empty allowed variants", input: "00112233-4455-4677-8899-aabbccddeeff", wantErr: check.ErrInvalidRule},
		// 越界枚举验证每项规则都要先校验。
		{name: "unknown variant", input: "00112233-4455-4677-8899-aabbccddeeff", allowed: []format.UUIDVariant{format.UUIDVariantRFC, format.UUIDVariant(255)}, wantErr: check.ErrInvalidRule},
		// 格式错误验证变体检查仍依赖标准 UUID 解析。
		{name: "invalid UUID", input: "8", allowed: []format.UUIDVariant{format.UUIDVariantRFC}, wantErr: check.ErrInvalidValue},
	})
}

func TestParseULID(t *testing.T) {
	testIdentifierParse(t, format.ParseULID, []identifierParseCase{
		// 26 个零验证最小值解码为全零字节。
		{name: "zero", input: strings.Repeat("0", 26)},
		// 低两位 10 表示 32，验证跨 Base32 位的字节组合。
		{name: "low digits", input: strings.Repeat("0", 24) + "10", want: [16]byte{15: 32}},
		// 7 后跟 25 个 Z 恰好表示最大 128 位值。
		{name: "maximum", input: "7" + strings.Repeat("Z", 25), want: [16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		// 小写 z 验证基础解析允许小写字母。
		{name: "lowercase", input: strings.Repeat("0", 25) + "z", want: [16]byte{15: 31}},
		// 首位为 8 时超过 128 位容量，应被拒绝。
		{name: "overflow", input: "8" + strings.Repeat("0", 25), wantErr: check.ErrInvalidValue},
		// 只少一个字符仍应被固定长度规则拒绝。
		{name: "short length", input: strings.Repeat("0", 25), wantErr: check.ErrInvalidValue},
		// 在末位使用 O 验证不接受零的字母别名。
		{name: "invalid alias", input: strings.Repeat("0", 25) + "O", wantErr: check.ErrInvalidValue},
	})
}

func TestCanonicalULID(t *testing.T) {
	testIdentifierText(t, format.CanonicalULID, []identifierTextCase{
		// 大写末位属于规范 ULID。
		{name: "uppercase", input: strings.Repeat("0", 25) + "Z"},
		// 小写末位能解析但不符合规范大小写要求。
		{name: "lowercase", input: strings.Repeat("0", 25) + "z", wantErr: check.ErrInvalidValue},
		// 长度错误时仍应按基础格式失败。
		{name: "invalid format", input: "Z", wantErr: check.ErrInvalidValue},
	})
}

func testIdentifierParse(t *testing.T, parse func(string) ([16]byte, error), tests []identifierParseCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input)
			assertIdentifierError(t, err, test.wantErr)
			if test.wantErr == nil && got != test.want {
				t.Errorf("parse(%q) = %x, want %x", test.input, got, test.want)
			}
		})
	}
}

func testIdentifierText(t *testing.T, validate func(string) error, tests []identifierTextCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertIdentifierError(t, validate(test.input), test.wantErr)
		})
	}
}

func testIdentifierAllowed[T any](t *testing.T, validate func(string, ...T) error, tests []identifierAllowedCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertIdentifierError(t, validate(test.input, test.allowed...), test.wantErr)
		})
	}
}

func assertIdentifierError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}
