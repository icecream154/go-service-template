// Package format 检查标准格式及其本地结构，不验证资源存在、所有权或远程可信性。
package format

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// ParseURL 按 Go URL 语法解析 URI 引用，允许相对引用及空引用，不等同于 HTTP URL 检查。
func ParseURL(value string) (*url.URL, error) {
	if !utf8.ValidString(value) {
		return nil, check.NewViolation("url", "must be valid UTF-8 text")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, check.NewViolation("url", "must be a valid URL reference")
	}
	return parsed, nil
}

// ParseAbsoluteURL 要求 URI 包含协议；绝对 opaque URI（如 mailto）仍可接受。
func ParseAbsoluteURL(value string) (*url.URL, error) {
	parsed, err := ParseURL(value)
	if err != nil {
		return nil, err
	}
	if !parsed.IsAbs() {
		return nil, check.NewViolation("absolute_url", "must have an absolute URI scheme")
	}
	return parsed, nil
}

// ParseHTTPURL 要求绝对、层次型 HTTP/HTTPS URL 且主机名非空，协议不区分大小写。
// 不限制来源、userinfo、端口、query 或 fragment，不发起网络请求。
func ParseHTTPURL(value string) (*url.URL, error) {
	parsed, err := ParseAbsoluteURL(value)
	if err != nil {
		return nil, err
	}
	if parsed.Opaque != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, check.NewViolation("http_url", "must be an absolute HTTP(S) URL with a hostname")
	}
	return parsed, nil
}

// URLSchemeOneOf 检查解析结果的协议属于调用方给定集合，按 ASCII 大小写不敏感匹配。
func URLSchemeOneOf(value *url.URL, allowed ...string) error {
	if len(allowed) == 0 {
		return check.NewRuleError("url_scheme", "at least one allowed scheme is required")
	}
	for _, scheme := range allowed {
		if !urlValidScheme(scheme) {
			return check.NewRuleError("url_scheme", "allowed schemes must use URI scheme syntax")
		}
	}
	if value != nil {
		for _, scheme := range allowed {
			if strings.EqualFold(value.Scheme, scheme) {
				return nil
			}
		}
	}
	return check.NewViolation("url_scheme", "must use an allowed URL scheme")
}

func urlValidScheme(value string) bool {
	if value == "" || !((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// URLHostnameOneOf 检查允许的主机，不匹配子域，不做尾部点或 IDNA 等价转换。
// DNS 名称按大小写折叠比较；IP 按地址值比较，IPv6 zone 区分大小写且不移除 mapped 表示。
func URLHostnameOneOf(value *url.URL, allowed ...string) error {
	if len(allowed) == 0 {
		return check.NewRuleError("url_hostname", "at least one allowed hostname is required")
	}
	for _, hostname := range allowed {
		if hostname == "" || strings.ContainsAny(hostname, "/?#@[]") || !utf8.ValidString(hostname) || strings.ContainsFunc(hostname, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return check.NewRuleError("url_hostname", "allowed hostnames must be non-empty hostname values")
		}
		if err := Hostname(hostname, HostnameOptions{AllowTrailingDot: true, AllowIDNA: true}); err != nil {
			if _, ipErr := ParseIPAddress(hostname); ipErr != nil {
				return check.NewRuleError("url_hostname", "allowed hostnames must be DNS names or IP addresses without ports")
			}
		}
	}
	if value != nil && value.Hostname() != "" {
		for _, hostname := range allowed {
			if acceptedIP, err := ParseIPAddress(hostname); err == nil {
				actualIP, actualErr := ParseIPAddress(value.Hostname())
				if actualErr == nil && actualIP == acceptedIP {
					return nil
				}
				continue
			}
			if strings.EqualFold(value.Hostname(), hostname) {
				return nil
			}
		}
	}
	return check.NewViolation("url_hostname", "must use an allowed hostname")
}

// URLHostnameWithinDomain 按完整 DNS 标签边界检查子域，includeRoot 控制是否接受根域。
// domain 必须为无尾部点、无通配符的 ASCII DNS 名称，不执行 DNS 或公共后缀查询。
func URLHostnameWithinDomain(value *url.URL, domain string, includeRoot bool) error {
	if err := Hostname(domain, HostnameOptions{}); err != nil || strings.HasSuffix(domain, ".") {
		return check.NewRuleError("url_domain", "domain must be an ASCII DNS hostname without a trailing dot")
	}
	if value == nil || value.Hostname() == "" {
		return check.NewViolation("url_domain", "must have a hostname within the specified domain")
	}
	hostname := value.Hostname()
	if err := Hostname(hostname, HostnameOptions{}); err != nil {
		return check.NewViolation("url_domain", "must have an ASCII DNS hostname")
	}
	if includeRoot && strings.EqualFold(hostname, domain) {
		return nil
	}
	if strings.HasSuffix(strings.ToLower(hostname), "."+strings.ToLower(domain)) {
		return nil
	}
	return check.NewViolation("url_domain", "hostname must lie within the specified DNS label boundary")
}

// URLPortInRange 检查显式端口的闭区间；未提供端口时由 allowAbsent 决定。
// 不猜测 HTTP/HTTPS 默认端口；显式空端口被拒绝。
func URLPortInRange(value *url.URL, minimum, maximum uint16, allowAbsent bool) error {
	if minimum > maximum {
		return check.NewRuleError("url_port", "minimum must not exceed maximum")
	}
	if value == nil || value.Hostname() == "" {
		return check.NewViolation("url_port", "must have a URL authority")
	}
	port := value.Port()
	if port == "" {
		// URL 可以由调用方直接构造，Port() 对无效端口也返回空字符串。
		// 只有真正没有端口分隔部分时，才适用 allowAbsent。
		hasPortPart := strings.Contains(value.Host, ":")
		if strings.HasPrefix(value.Host, "[") {
			closing := strings.LastIndexByte(value.Host, ']')
			hasPortPart = closing < 0 || closing+1 != len(value.Host)
		}
		if hasPortPart {
			return check.NewViolation("url_port", "an explicit port must be a non-empty decimal number")
		}
		if allowAbsent {
			return nil
		}
		return check.NewViolation("url_port", "an explicit port is required")
	}
	parsed, err := ParsePort(port)
	if err != nil {
		return err
	}
	return PortInRange(parsed, minimum, maximum)
}

// URLNoUserinfo 拒绝解析结果中的 userinfo，包括空用户名。
func URLNoUserinfo(value *url.URL) error {
	if value == nil || value.User != nil {
		return check.NewViolation("url_userinfo", "must not contain URL userinfo")
	}
	return nil
}

// URLNoQuery 拒绝非空 query 及显式空查询标记问号。
func URLNoQuery(value *url.URL) error {
	if value == nil || value.RawQuery != "" || value.ForceQuery {
		return check.NewViolation("url_query", "must not contain a URL query")
	}
	return nil
}

// URLNoFragment 从原始文本检查片段标记，包括显式空片段；不会丢失解析时省略的空标记。
func URLNoFragment(value string) error {
	if _, err := ParseURL(value); err != nil {
		return err
	}
	if strings.Contains(value, "#") {
		return check.NewViolation("url_fragment", "must not contain a URL fragment")
	}
	return nil
}
