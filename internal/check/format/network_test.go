package format_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

type networkParseCase[T comparable] struct {
	name    string
	input   string
	want    T
	wantErr error
}

func TestParseIPAddress(t *testing.T) {
	testNetworkParser(t, format.ParseIPAddress, []networkParseCase[netip.Addr]{
		// 用文档保留的 IPv4 地址验证四个八位组都正确保留。
		{name: "IPv4", input: "192.0.2.42", want: netip.AddrFrom4([4]byte{192, 0, 2, 42})},
		// 用展开的 IPv6 文本验证解析得到对应的地址值。
		{name: "expanded IPv6", input: "2001:0db8:0:0:0:0:0:1", want: netip.MustParseAddr("2001:db8::1")},
		// 用带接口标识的链路本地地址验证 zone 原样保留。
		{name: "IPv6 with zone", input: "fe80::1%eth0", want: netip.MustParseAddr("fe80::1").WithZone("eth0")},
		// 用 mapped 地址验证解析保留 IPv6 表示。
		{name: "mapped IPv4", input: "::ffff:192.0.2.42", want: netip.MustParseAddr("::ffff:192.0.2.42")},
		// 用有效域名验证入口只接受 IP 文本。
		{name: "hostname", input: "example.test", wantErr: check.ErrInvalidValue},
		// 给原生地址附加端口，验证地址解析拒绝组合文本。
		{name: "address and port", input: "192.0.2.42:80", wantErr: check.ErrInvalidValue},
	})
}

type networkTextCase struct {
	name    string
	input   string
	wantErr error
}

func TestIPv4(t *testing.T) {
	testNetworkText(t, format.IPv4, []networkTextCase{
		// 用原生 IPv4 代表允许的地址族。
		{name: "native IPv4", input: "192.0.2.42"},
		// 用 mapped 地址验证 IPv4 检查仍要求原生表示。
		{name: "mapped IPv4", input: "::ffff:192.0.2.42", wantErr: check.ErrInvalidValue},
		// 用原生 IPv6 验证地址族检查。
		{name: "native IPv6", input: "2001:db8::1", wantErr: check.ErrInvalidValue},
		// 用越界八位组验证解析错误由入口返回。
		{name: "invalid octet", input: "192.0.2.256", wantErr: check.ErrInvalidValue},
	})
}

func TestIPv6(t *testing.T) {
	testNetworkText(t, format.IPv6, []networkTextCase{
		// 用压缩写法验证原生 IPv6 通过。
		{name: "native IPv6", input: "2001:db8::1"},
		// 用 mapped 地址验证它仍属于 IPv6。
		{name: "mapped IPv4", input: "::ffff:192.0.2.42"},
		// 用带 zone 地址验证地址族检查保留接口信息。
		{name: "IPv6 with zone", input: "fe80::1%eth0"},
		// 用原生 IPv4 验证地址族不匹配时返回输入错误。
		{name: "native IPv4", input: "192.0.2.42", wantErr: check.ErrInvalidValue},
		// 用空 zone 验证入口会传递解析错误。
		{name: "invalid zone", input: "fe80::1%", wantErr: check.ErrInvalidValue},
	})
}

type networkAddressCase struct {
	name    string
	address netip.Addr
	wantErr error
}

func TestIPWithoutZone(t *testing.T) {
	testNetworkAddress(t, format.IPWithoutZone, []networkAddressCase{
		// 用无 zone 的 IPv6 验证有效地址通过。
		{name: "plain IPv6", address: netip.MustParseAddr("fe80::1")},
		// 为同一地址加 zone，验证接口信息触发拒绝。
		{name: "with zone", address: netip.MustParseAddr("fe80::1").WithZone("eth0"), wantErr: check.ErrInvalidValue},
		// 用零值验证还要求地址本身有效。
		{name: "invalid address", address: netip.Addr{}, wantErr: check.ErrInvalidValue},
	})
}

func TestIPNotMappedIPv4(t *testing.T) {
	testNetworkAddress(t, format.IPNotMappedIPv4, []networkAddressCase{
		// 用原生 IPv4 验证可直接通过。
		{name: "native IPv4", address: netip.MustParseAddr("192.0.2.42")},
		// 用普通 IPv6 验证检查允许不同地址族中的原生表示。
		{name: "native IPv6", address: netip.MustParseAddr("2001:db8::1")},
		// 用 mapped 表示验证同一个 IPv4 值在 IPv6 包装下被拒绝。
		{name: "mapped IPv4", address: netip.MustParseAddr("::ffff:192.0.2.42"), wantErr: check.ErrInvalidValue},
		// 用零地址验证有效性检查。
		{name: "invalid address", address: netip.Addr{}, wantErr: check.ErrInvalidValue},
	})
}

func TestParseIPPrefix(t *testing.T) {
	testNetworkParser(t, format.ParseIPPrefix, []networkParseCase[netip.Prefix]{
		// 用主机位非零的输入验证解析保留完整地址与掩码位数。
		{name: "host bits retained", input: "192.0.2.42/24", want: netip.PrefixFrom(netip.MustParseAddr("192.0.2.42"), 24)},
		// 用 IPv6 前缀验证支持另一个地址族的掩码。
		{name: "IPv6 prefix", input: "2001:db8::/32", want: netip.PrefixFrom(netip.MustParseAddr("2001:db8::"), 32)},
		// 用 0 位掩码验证有效长度的下界。
		{name: "zero bits", input: "0.0.0.0/0", want: netip.PrefixFrom(netip.MustParseAddr("0.0.0.0"), 0)},
		// 用越过 IPv4 长度上限一位的掩码验证边界拒绝。
		{name: "prefix length overflow", input: "192.0.2.42/33", wantErr: check.ErrInvalidValue},
		// 用裸地址验证必须提供 CIDR 位数。
		{name: "missing length", input: "192.0.2.42", wantErr: check.ErrInvalidValue},
	})
}

func TestCanonicalIPPrefix(t *testing.T) {
	testNetworkText(t, format.CanonicalIPPrefix, []networkTextCase{
		// 用已掩码的 IPv4 文本验证规范形式通过。
		{name: "canonical IPv4", input: "192.0.2.0/24"},
		// 用压缩、全小写的 IPv6 文本验证规范形式通过。
		{name: "canonical IPv6", input: "2001:db8::/32"},
		// 用非零主机位验证入口要求输入本身已掩码。
		{name: "unmasked prefix", input: "192.0.2.42/24", wantErr: check.ErrInvalidValue},
		// 用相同网络的展开写法验证文本也须符合规范输出。
		{name: "noncanonical spelling", input: "2001:0DB8:0:0:0:0:0:0/32", wantErr: check.ErrInvalidValue},
		// 用缺少位数的地址验证解析错误被保留。
		{name: "invalid prefix", input: "192.0.2.0", wantErr: check.ErrInvalidValue},
	})
}

func TestParseAddressPort(t *testing.T) {
	testNetworkParser(t, format.ParseAddressPort, []networkParseCase[netip.AddrPort]{
		// 用显式零端口验证地址和端口下界均保留。
		{name: "IPv4 with zero port", input: "192.0.2.42:0", want: netip.AddrPortFrom(netip.MustParseAddr("192.0.2.42"), 0)},
		// 用括号与 zone 验证 IPv6 地址和最大端口正确分离。
		{name: "bracketed IPv6 with zone", input: "[fe80::1%eth0]:65535", want: netip.AddrPortFrom(netip.MustParseAddr("fe80::1%eth0"), 65535)},
		// 用去掉括号的 IPv6 验证必须明确区分地址和端口。
		{name: "unbracketed IPv6", input: "2001:db8::1:443", wantErr: check.ErrInvalidValue},
		// 用带端口域名验证地址必须是 IP。
		{name: "hostname with port", input: "example.test:443", wantErr: check.ErrInvalidValue},
		// 用裸地址验证端口不可省略。
		{name: "missing port", input: "192.0.2.42", wantErr: check.ErrInvalidValue},
		// 用超过 16 位的端口验证组合输入的边界检查。
		{name: "overflowing port", input: "192.0.2.42:65536", wantErr: check.ErrInvalidValue},
	})
}

func TestParsePort(t *testing.T) {
	testNetworkParser(t, format.ParsePort, []networkParseCase[uint16]{
		// 用 0 验证词法解析接受端口下界。
		{name: "minimum", input: "0", want: 0},
		// 用带前导零和数字 8 的输入验证固定十进制。
		{name: "leading zeros", input: "0080", want: 80},
		// 用 65535 验证端口上界有效。
		{name: "maximum", input: "65535", want: 65535},
		// 用 65536 验证恰好超出范围时拒绝。
		{name: "overflow", input: "65536", wantErr: check.ErrInvalidValue},
		// 用空串验证必须提供数字。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 为有效数字加正号，验证只接受十进制数字。
		{name: "leading plus", input: "+80", wantErr: check.ErrInvalidValue},
		// 用负零验证符号本身触发拒绝。
		{name: "leading minus", input: "-0", wantErr: check.ErrInvalidValue},
		// 用前导空格验证解析按原始文本校验。
		{name: "leading whitespace", input: " 80", wantErr: check.ErrInvalidValue},
		// 用全角数字验证仅接受 ASCII 数字。
		{name: "fullwidth digits", input: "８０", wantErr: check.ErrInvalidValue},
	})
}

func TestPortInRange(t *testing.T) {
	tests := []struct {
		name                    string
		value, minimum, maximum uint16
		wantErr                 error
	}{
		// 用 0 作为下端点验证范围包含零端口。
		{name: "zero lower endpoint", value: 0, minimum: 0, maximum: 10},
		// 用上端点验证闭区间包含最大值。
		{name: "upper endpoint", value: 10, minimum: 1, maximum: 10},
		// 用内部端口验证端点之间的取值。
		{name: "inside bounds", value: 5, minimum: 1, maximum: 10},
		// 用相等边界验证单点范围有效。
		{name: "single value", value: 65535, minimum: 65535, maximum: 65535},
		// 用紧邻下端点的值验证输入错误。
		{name: "below minimum", value: 0, minimum: 1, maximum: 10, wantErr: check.ErrInvalidValue},
		// 用紧邻上端点的值验证输入错误。
		{name: "above maximum", value: 11, minimum: 1, maximum: 10, wantErr: check.ErrInvalidValue},
		// 对调边界验证规则错误的分类。
		{name: "reversed bounds", value: 5, minimum: 10, maximum: 1, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertNetworkError(t, format.PortInRange(test.value, test.minimum, test.maximum), test.wantErr)
		})
	}
}

func TestHostname(t *testing.T) {
	label := strings.Repeat("a", 63)
	threeLabels := label + "." + label + "." + label + "."
	maximumName := threeLabels + strings.Repeat("b", 61)
	tests := []struct {
		name    string
		input   string
		options format.HostnameOptions
		wantErr error
	}{
		// 用混合大小写和内部连字符的单标签验证默认语法。
		{name: "single ASCII label", input: "Local-9"},
		// 用两个标签验证显式的多标签要求。
		{name: "multiple labels", input: "service.example", options: format.HostnameOptions{RequireMultipleLabels: true}},
		// 用同一个单标签验证启用多标签约束后的拒绝。
		{name: "single label disallowed", input: "Local-9", options: format.HostnameOptions{RequireMultipleLabels: true}, wantErr: check.ErrInvalidValue},
		// 用空串验证非空检查。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 用非法编码验证主机名输入必须是有效 UTF-8。
		{name: "invalid UTF-8", input: "\xff.example", wantErr: check.ErrInvalidValue},
		// 用显式根点验证默认策略拒绝尾部点。
		{name: "root dot disallowed", input: "service.example.", wantErr: check.ErrInvalidValue},
		// 对同一根点启用许可，验证该选项独立生效。
		{name: "root dot allowed", input: "service.example.", options: format.HostnameOptions{AllowTrailingDot: true}},
		// 追加第二个根点，验证允许的形式只有一个尾部点。
		{name: "double root dot", input: "service.example..", options: format.HostnameOptions{AllowTrailingDot: true}, wantErr: check.ErrInvalidValue},
		// 用最左通配标签验证默认策略拒绝通配符。
		{name: "wildcard disallowed", input: "*.service.example", wantErr: check.ErrInvalidValue},
		// 启用通配且保留两个普通标签，验证同时满足两项约束。
		{name: "wildcard allowed", input: "*.service.example", options: format.HostnameOptions{AllowWildcard: true, RequireMultipleLabels: true}},
		// 通配符后只有一个普通标签，验证通配符不占普通标签数量。
		{name: "wildcard does not count as label", input: "*.example", options: format.HostnameOptions{AllowWildcard: true, RequireMultipleLabels: true}, wantErr: check.ErrInvalidValue},
		// 把通配符放进中间标签，验证许可仅限最左的完整标签。
		{name: "embedded wildcard", input: "service.*.example", options: format.HostnameOptions{AllowWildcard: true}, wantErr: check.ErrInvalidValue},
		// 用包含下划线的服务标签验证默认拒绝。
		{name: "underscore disallowed", input: "_service.example", wantErr: check.ErrInvalidValue},
		// 同一服务标签在明确允许下划线时应通过。
		{name: "underscore allowed", input: "_service.example", options: format.HostnameOptions{AllowUnderscore: true}},
		// 用 Unicode 标签验证默认只接受 ASCII。
		{name: "IDNA disabled", input: "bücher.example", wantErr: check.ErrInvalidValue},
		// 用 Unicode 标签验证 IDNA 开关允许转换后的有效标签。
		{name: "Unicode label", input: "bücher.example", options: format.HostnameOptions{AllowIDNA: true}},
		// 用 A-label 验证已经编码的 IDNA 主机名也可通过。
		{name: "ASCII IDNA label", input: "xn--bcher-kva.example", options: format.HostnameOptions{AllowIDNA: true}},
		// 把合法 ASCII 下划线标签与 Unicode 标签组合，验证选项可同时使用。
		{name: "ASCII underscore and IDNA labels", input: "_service.bücher.example", options: format.HostnameOptions{AllowIDNA: true, AllowUnderscore: true}},
		// 用缺少上下文的连接符验证 IDNA 转换失败的错误分类。
		{name: "invalid IDNA joiner", input: "\u200d.example", options: format.HostnameOptions{AllowIDNA: true}, wantErr: check.ErrInvalidValue},
		// 用连续点构造空标签并验证拒绝。
		{name: "empty label", input: "service..example", wantErr: check.ErrInvalidValue},
		// 用最左连字符验证标签端点限制。
		{name: "leading hyphen", input: "-service.example", wantErr: check.ErrInvalidValue},
		// 用最右连字符验证标签另一端的限制。
		{name: "trailing hyphen", input: "service-.example", wantErr: check.ErrInvalidValue},
		// 用恰好 63 个 ASCII 字节验证单标签长度上界。
		{name: "maximum label length", input: label},
		// 用 64 字节验证越过单标签长度上界。
		{name: "label too long", input: label + "a", wantErr: check.ErrInvalidValue},
		// 用有效标签拼出 253 字节名称验证整名长度上界。
		{name: "maximum hostname length", input: maximumName},
		// 用有效标签拼出 254 字节名称，验证独立的总长度限制。
		{name: "hostname too long", input: maximumName + "b", wantErr: check.ErrInvalidValue},
		// 给达到长度上界的名称加根点，验证根点不计入总长度。
		{name: "root dot does not count toward length", input: maximumName + ".", options: format.HostnameOptions{AllowTrailingDot: true}},
		// 给达到长度上界的名称加通配标签，验证通配标签计入总长度。
		{name: "wildcard counts toward length", input: "*." + maximumName, options: format.HostnameOptions{AllowWildcard: true}, wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertNetworkError(t, format.Hostname(test.input, test.options), test.wantErr)
		})
	}
}

func testNetworkParser[T comparable](t *testing.T, parse func(string) (T, error), tests []networkParseCase[T]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input)
			assertNetworkError(t, err, test.wantErr)
			if test.wantErr == nil && got != test.want {
				t.Errorf("parse(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func testNetworkText(t *testing.T, validate func(string) error, tests []networkTextCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertNetworkError(t, validate(test.input), test.wantErr)
		})
	}
}

func testNetworkAddress(t *testing.T, validate func(netip.Addr) error, tests []networkAddressCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertNetworkError(t, validate(test.address), test.wantErr)
		})
	}
}

func assertNetworkError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
