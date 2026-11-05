package format

import (
	"encoding/base64"
	"encoding/hex"
	"strings"

	"go-service-template/internal/check"
)

// Base64Variant 明确区分 RFC 4648 的字符表及是否带等号填充。
type Base64Variant uint8

const (
	Base64Standard Base64Variant = iota
	Base64StandardRaw
	Base64URL
	Base64URLRaw
)

// Hex 检查十六进制文本，接受大小写及空串，要求完整字节（偶数位）。
func Hex(value string) error {
	if len(value)%2 != 0 {
		return check.NewViolation("hex", "must contain an even number of hexadecimal digits")
	}
	for i := range value {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return check.NewViolation("hex", "must contain only hexadecimal digits")
		}
	}
	return nil
}

// CanonicalHex 检查指定大小写的规范十六进制文本，不改变输入。
func CanonicalHex(value string, uppercase bool) error {
	if err := Hex(value); err != nil {
		return err
	}
	for i := range value {
		if uppercase && value[i] >= 'a' && value[i] <= 'f' || !uppercase && value[i] >= 'A' && value[i] <= 'F' {
			return check.NewViolation("hex.canonical", "must use the specified letter case")
		}
	}
	return nil
}

// ParseHex 返回新的字节切片，解码前检查数量上限；上限为 0 时仅接受空串。
func ParseHex(value string, maximumDecodedBytes int) ([]byte, error) {
	if maximumDecodedBytes < 0 {
		return nil, check.NewRuleError("hex", "maximum decoded byte count must not be negative")
	}
	if err := Hex(value); err != nil {
		return nil, err
	}
	if len(value)/2 > maximumDecodedBytes {
		return nil, check.NewViolation("hex.length", "decoded byte count exceeds the maximum")
	}
	result, _ := hex.DecodeString(value)
	return result, nil
}

// ParseBase64 解析规范 Base64 文本，拒绝空白、非零填充位和不匹配的填充形式。
// variant 决定字符表和填充规则；空串可表示空数据，maximumDecodedBytes 必须非负。
// 解码结果独立分配，大小严格不超过上限；标准解码器的容量预估最多多分配两个字节。
func ParseBase64(value string, variant Base64Variant, maximumDecodedBytes int) ([]byte, error) {
	if maximumDecodedBytes < 0 {
		return nil, check.NewRuleError("base64", "maximum decoded byte count must not be negative")
	}
	var encoding *base64.Encoding
	padded := false
	switch variant {
	case Base64Standard:
		encoding, padded = base64.StdEncoding, true
	case Base64StandardRaw:
		encoding = base64.RawStdEncoding
	case Base64URL:
		encoding, padded = base64.URLEncoding, true
	case Base64URLRaw:
		encoding = base64.RawURLEncoding
	default:
		return nil, check.NewRuleError("base64", "unknown base64 variant")
	}
	if strings.ContainsAny(value, "\r\n\t ") {
		return nil, check.NewViolation("base64", "must not contain whitespace")
	}
	expected := encoding.DecodedLen(len(value))
	if padded && len(value) >= 4 && len(value)%4 == 0 {
		if value[len(value)-1] == '=' {
			expected--
		}
		if value[len(value)-2] == '=' {
			expected--
		}
	}
	if expected > maximumDecodedBytes {
		return nil, check.NewViolation("base64.length", "decoded byte count exceeds the maximum")
	}
	result, err := encoding.Strict().DecodeString(value)
	if err != nil || len(result) > maximumDecodedBytes || encoding.EncodeToString(result) != value {
		return nil, check.NewViolation("base64", "must use the canonical representation of the selected variant")
	}
	return result, nil
}
