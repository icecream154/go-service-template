package format

import (
	"encoding/hex"
	"slices"
	"strings"

	"go-service-template/internal/check"
)

// UUIDVariant 表示 UUID 变体位，仅检查位布局，不判断业务身份。
type UUIDVariant uint8

const (
	UUIDVariantNCS UUIDVariant = iota
	UUIDVariantRFC
	UUIDVariantMicrosoft
	UUIDVariantReserved
)

// ParseUUID 接受 8-4-4-4-12 的 ASCII 十六进制表示，允许大小写及全零 UUID。
// 不接受花括号、URN、无连字符表示，也不默认限制版本或变体。
func ParseUUID(value string) ([16]byte, error) {
	var result [16]byte
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return result, check.NewViolation("uuid", "must use the standard hyphenated UUID representation")
	}
	compact := value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	if _, err := hex.Decode(result[:], []byte(compact)); err != nil {
		return [16]byte{}, check.NewViolation("uuid", "must contain hexadecimal UUID digits")
	}
	return result, nil
}

// CanonicalUUID 在标准表示基础上要求十六进制字母为小写，不改写输入。
func CanonicalUUID(value string) error {
	if _, err := ParseUUID(value); err != nil {
		return err
	}
	if strings.ToLower(value) != value {
		return check.NewViolation("canonical_uuid", "must use lowercase hexadecimal UUID text")
	}
	return nil
}

// UUIDVersionOneOf 检查 UUID 版本位属于给定的标准版本 1..8，不默认接受零 UUID。
func UUIDVersionOneOf(value string, allowed ...uint8) error {
	if len(allowed) == 0 {
		return check.NewRuleError("uuid_version", "at least one UUID version is required")
	}
	for _, version := range allowed {
		if version < 1 || version > 8 {
			return check.NewRuleError("uuid_version", "allowed UUID versions must lie between 1 and 8")
		}
	}
	parsed, err := ParseUUID(value)
	if err != nil {
		return err
	}
	if slices.Contains(allowed, parsed[6]>>4) {
		return nil
	}
	return check.NewViolation("uuid_version", "must use an allowed UUID version")
}

// UUIDVariantOneOf 检查 UUID 变体位，允许集合由调用方明确提供。
func UUIDVariantOneOf(value string, allowed ...UUIDVariant) error {
	if len(allowed) == 0 {
		return check.NewRuleError("uuid_variant", "at least one UUID variant is required")
	}
	for _, variant := range allowed {
		if variant > UUIDVariantReserved {
			return check.NewRuleError("uuid_variant", "unsupported UUID variant")
		}
	}
	parsed, err := ParseUUID(value)
	if err != nil {
		return err
	}
	variant := UUIDVariantReserved
	switch {
	case parsed[8]&0x80 == 0:
		variant = UUIDVariantNCS
	case parsed[8]&0xc0 == 0x80:
		variant = UUIDVariantRFC
	case parsed[8]&0xe0 == 0xc0:
		variant = UUIDVariantMicrosoft
	}
	if slices.Contains(allowed, variant) {
		return nil
	}
	return check.NewViolation("uuid_variant", "must use an allowed UUID variant")
}

// ParseULID 接受 26 位 Crockford Base32 文本，允许大小写，拒绝 128 位溢出。
// 不接受 I、L、O、U 别名，不检查生成时间或业务存在性。
func ParseULID(value string) ([16]byte, error) {
	var result [16]byte
	if len(value) != 26 {
		return result, check.NewViolation("ulid", "must contain exactly 26 Crockford Base32 characters")
	}
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for index := range value {
		c := value[index]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		digit := strings.IndexByte(alphabet, c)
		if digit < 0 || (index == 0 && digit > 7) {
			return [16]byte{}, check.NewViolation("ulid", "must use valid Base32 digits without exceeding 128 bits")
		}
		carry := uint16(digit)
		for i := len(result) - 1; i >= 0; i-- {
			next := uint16(result[i])<<5 | carry
			result[i] = byte(next)
			carry = next >> 8
		}
	}
	return result, nil
}

// CanonicalULID 额外要求大写的规范 ULID 文本，不改变原输入。
func CanonicalULID(value string) error {
	if _, err := ParseULID(value); err != nil {
		return err
	}
	if strings.ToUpper(value) != value {
		return check.NewViolation("canonical_ulid", "must use uppercase ULID text")
	}
	return nil
}
