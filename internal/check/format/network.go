package format

import (
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"go-service-template/internal/check"

	"golang.org/x/net/idna"
)

// ParseIPAddress 解析 IPv4 或 IPv6 文本；保留显式 IPv6 zone 及 mapped 地址，不查询 DNS。
func ParseIPAddress(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, check.NewViolation("ip_address", "must be a valid IPv4 or IPv6 address")
	}
	return address, nil
}

// IPv4 要求原生 IPv4 表示，不将 IPv4-mapped IPv6 隐式转为 IPv4。
func IPv4(value string) error {
	address, err := ParseIPAddress(value)
	if err != nil {
		return err
	}
	if !address.Is4() {
		return check.NewViolation("ipv4", "must be a native IPv4 address")
	}
	return nil
}

// IPv6 要求 IPv6 表示，包含 mapped 地址及合法 zone；额外限制可组合以下方法。
func IPv6(value string) error {
	address, err := ParseIPAddress(value)
	if err != nil {
		return err
	}
	if !address.Is6() {
		return check.NewViolation("ipv6", "must be an IPv6 address")
	}
	return nil
}

// IPWithoutZone 要求有效地址且不带接口 zone，不改写原值。
func IPWithoutZone(value netip.Addr) error {
	if !value.IsValid() || value.Zone() != "" {
		return check.NewViolation("ip_zone", "must be a valid address without a zone")
	}
	return nil
}

// IPNotMappedIPv4 拒绝 IPv4-mapped IPv6，避免调用方无意混用两种表示。
func IPNotMappedIPv4(value netip.Addr) error {
	if !value.IsValid() || value.Is4In6() {
		return check.NewViolation("ip_mapped", "must not be an IPv4-mapped IPv6 address")
	}
	return nil
}

// ParseIPPrefix 解析 CIDR；允许主机位非零，规范网络前缀使用 CanonicalIPPrefix 检查。
func ParseIPPrefix(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, check.NewViolation("ip_prefix", "must be a valid CIDR prefix")
	}
	return prefix, nil
}

// CanonicalIPPrefix 要求主机位全零且文本恰为 netip 的规范输出，不隐式掩码或重写。
func CanonicalIPPrefix(value string) error {
	prefix, err := ParseIPPrefix(value)
	if err != nil {
		return err
	}
	if prefix != prefix.Masked() || prefix.String() != value {
		return check.NewViolation("canonical_ip_prefix", "must use a canonical masked CIDR representation")
	}
	return nil
}

// ParseAddressPort 解析 IP 与显式端口，IPv6 需要方括号；不接受域名，不查询 DNS。
func ParseAddressPort(value string) (netip.AddrPort, error) {
	address, err := netip.ParseAddrPort(value)
	if err != nil {
		return netip.AddrPort{}, check.NewViolation("address_port", "must be an IP address with an explicit valid port")
	}
	return address, nil
}

// ParsePort 解析不带符号的十进制 0..65535；端口 0 是否允许由使用场景另行约束。
func ParsePort(value string) (uint16, error) {
	if value == "" {
		return 0, check.NewViolation("port", "must be an unsigned decimal port")
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, check.NewViolation("port", "must be an unsigned decimal port")
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, check.NewViolation("port", "must be within the 16-bit port range")
	}
	return uint16(parsed), nil
}

// PortInRange 检查包含端点的端口范围，不擅自将端口 0 作为空值。
func PortInRange(value, minimum, maximum uint16) error {
	if minimum > maximum {
		return check.NewRuleError("port_range", "minimum must not exceed maximum")
	}
	if value < minimum || value > maximum {
		return check.NewViolation("port_range", "must be within the inclusive port range")
	}
	return nil
}

// HostnameOptions 显式描述 DNS 主机名的词法政策；零值接受 ASCII 单标签名称。
type HostnameOptions struct {
	RequireMultipleLabels bool // 为 true 时至少有两个非通配标签，不等同于 DNS 或公共后缀检查。
	AllowTrailingDot      bool // 是否接受表示 DNS 根的单个尾部点。
	AllowWildcard         bool // 是否接受唯一的最左侧完整通配标签 "*."。
	AllowUnderscore       bool // 是否接受 ASCII 标签中的下划线，适用于明确的 DNS 记录场景。
	AllowIDNA             bool // 是否用固定的非过渡 IDNA Lookup 配置验证 Unicode 或 A-label 名称。
}

// 两个配置只在包初始化时构造，此后只读；不使用可被其他调用方替换的 idna.Lookup 变量。
var (
	networkIDNAProfile           = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.BidiRule())
	networkIDNAUnderscoreProfile = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.StrictDomainName(false), idna.BidiRule())
)

// Hostname 按 DNS 标签和显式选项检查主机名；不解析 IP 语义，不改写大小写或原文。
// 标签转换只用于验证长度和形式，允许下划线不放宽 Unicode 标签的 IDNA 规则。
func Hostname(value string, options HostnameOptions) error {
	if value == "" || !utf8.ValidString(value) {
		return check.NewViolation("hostname", "must be non-empty valid UTF-8 hostname text")
	}
	name := value
	wildcardBytes := 0
	if strings.HasPrefix(name, "*.") {
		if !options.AllowWildcard {
			return check.NewViolation("hostname", "must not use a wildcard label")
		}
		name = strings.TrimPrefix(name, "*.")
		wildcardBytes = 2
	}
	if options.AllowIDNA {
		profile := networkIDNAProfile
		if options.AllowUnderscore {
			profile = networkIDNAUnderscoreProfile
		}
		converted, err := profile.ToASCII(name)
		if err != nil {
			return check.NewViolation("hostname", "must contain valid IDNA labels")
		}
		name = converted
	}
	if strings.HasSuffix(name, ".") {
		if !options.AllowTrailingDot {
			return check.NewViolation("hostname", "must not have a trailing root dot")
		}
		name = strings.TrimSuffix(name, ".")
	}
	labels := strings.Split(name, ".")
	if options.RequireMultipleLabels && len(labels) < 2 {
		return check.NewViolation("hostname", "must contain at least two DNS labels")
	}
	total := wildcardBytes
	for index, label := range labels {
		ascii := label
		if !networkValidLabel(ascii, options.AllowUnderscore) {
			return check.NewViolation("hostname", "must use DNS labels of 1 to 63 bytes with valid characters")
		}
		if index != 0 {
			total++
		}
		total += len(ascii)
		if total > 253 {
			return check.NewViolation("hostname", "DNS name must contain at most 253 bytes without its root dot")
		}
	}
	return nil
}

func networkValidLabel(value string, allowUnderscore bool) bool {
	if len(value) == 0 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for i := range value {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || (allowUnderscore && c == '_')) {
			return false
		}
	}
	return true
}
