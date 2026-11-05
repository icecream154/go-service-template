package format

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// ParseEmailAddress 解析一个 RFC 5322 邮件地址，允许显示名，不接受换行或非法 UTF-8。
// 返回标准库的类型化结果，不检查 DNS、MX、账户存在或所有权。
func ParseEmailAddress(value string) (*mail.Address, error) {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n") {
		return nil, check.NewViolation("email_address", "must be valid single-line UTF-8 email address text")
	}
	address, err := mail.ParseAddress(value)
	if err != nil {
		return nil, check.NewViolation("email_address", "must be a valid single email address")
	}
	return address, nil
}

// ParseMailbox 只接受裸 addr-spec，不接受显示名、注释、尖括号或引号外的空白。
// 引号内的合法本地部分仍由 RFC 解析器检查；不将整个地址改为小写。
func ParseMailbox(value string) (*mail.Address, error) {
	address, err := ParseEmailAddress(value)
	if err != nil {
		return nil, err
	}
	if address.Name != "" || !emailBareSyntax(value) {
		return nil, check.NewViolation("mailbox", "must be a bare mailbox without display name, comments or surrounding whitespace")
	}
	return address, nil
}

func emailBareSyntax(value string) bool {
	quoted, escaped := false, false
	for _, r := range value {
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
			continue
		}
		if r == '"' {
			quoted = true
		} else if unicode.IsSpace(r) || strings.ContainsRune("()<>", r) {
			return false
		}
	}
	return !quoted && !escaped
}

// MailboxDomainOneOf 检查裸邮箱的域部分精确属于允许集合，仅忽略域名大小写。
// 不做别名、IDNA、尾部点或提供商特例归一化，不修改本地部分。
func MailboxDomainOneOf(value string, allowed ...string) error {
	if len(allowed) == 0 {
		return check.NewRuleError("mailbox_domain", "at least one allowed domain is required")
	}
	for _, domain := range allowed {
		if domain == "" || !utf8.ValidString(domain) || strings.ContainsAny(domain, "@") || strings.ContainsFunc(domain, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return check.NewRuleError("mailbox_domain", "allowed domains must be non-empty domain values")
		}
		if _, err := ParseMailbox("check@" + domain); err != nil {
			return check.NewRuleError("mailbox_domain", "allowed domains must use valid mailbox domain syntax")
		}
	}
	address, err := ParseMailbox(value)
	if err != nil {
		return err
	}
	separator := strings.LastIndexByte(address.Address, '@')
	if separator >= 0 {
		for _, domain := range allowed {
			if strings.EqualFold(address.Address[separator+1:], domain) {
				return nil
			}
		}
	}
	return check.NewViolation("mailbox_domain", "must use an allowed mailbox domain")
}
