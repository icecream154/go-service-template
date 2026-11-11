package format_test

import (
	"errors"
	"net/url"
	"reflect"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

type urlParseCase struct {
	name    string
	input   string
	want    url.URL
	wantErr error
}

func TestParseURL(t *testing.T) {
	testURLParser(t, format.ParseURL, []urlParseCase{
		// 用空字符串验证空引用也是合法的 URL 引用。
		{name: "empty reference", input: "", want: url.URL{}},
		// 用带查询和片段的相对引用验证各部分分别保留。
		{name: "relative reference", input: "child?view=list#heading", want: url.URL{Path: "child", RawQuery: "view=list", Fragment: "heading"}},
		// 用无协议的网络路径验证主机和路径都能解析。
		{name: "network path", input: "//example.test/path", want: url.URL{Host: "example.test", Path: "/path"}},
		// 用编码的斜杠验证解码路径和原始转义都可读取。
		{name: "escaped path", input: "/part%2Fname", want: url.URL{Path: "/part/name", RawPath: "/part%2Fname"}},
		// 用非法 UTF-8 验证解析前检查原始文本编码。
		{name: "invalid UTF-8", input: "/\xff", wantErr: check.ErrInvalidValue},
		// 用不完整的百分号编码验证 URL 语法错误的分类。
		{name: "malformed escape", input: "/part%2", wantErr: check.ErrInvalidValue},
	})
}

func TestParseAbsoluteURL(t *testing.T) {
	testURLParser(t, format.ParseAbsoluteURL, []urlParseCase{
		// 用层次型绝对 URL 验证协议、主机和路径得到保留。
		{name: "hierarchical absolute URL", input: "https://example.test/path", want: url.URL{Scheme: "https", Host: "example.test", Path: "/path"}},
		// 用不透明的 mailto URI 验证绝对性只要求存在协议。
		{name: "opaque absolute URL", input: "mailto:reader@example.test", want: url.URL{Scheme: "mailto", Opaque: "reader@example.test"}},
		// 用有主机的网络路径验证主机不能替代协议。
		{name: "network path without scheme", input: "//example.test/path", wantErr: check.ErrInvalidValue},
		// 用带协议的非法转义验证底层语法错误仍会返回。
		{name: "malformed absolute URL", input: "https://example.test/%xy", wantErr: check.ErrInvalidValue},
	})
}

func TestParseHTTPURL(t *testing.T) {
	testURLParser(t, format.ParseHTTPURL, []urlParseCase{
		// 用大写协议和完整 URL 部分验证协议大小写不影响解析。
		{name: "uppercase scheme with optional parts", input: "HTTPS://reader:fixture@Example.test:443/path?view=list#heading", want: url.URL{Scheme: "https", User: url.UserPassword("reader", "fixture"), Host: "Example.test:443", Path: "/path", RawQuery: "view=list", Fragment: "heading"}},
		// 用括号内 IPv6 和显式零端口验证带 IP 主机的 HTTP 地址。
		{name: "IPv6 host", input: "http://[2001:db8::1]:0", want: url.URL{Scheme: "http", Host: "[2001:db8::1]:0"}},
		// 用只有主机的 HTTPS 地址验证路径可以为空。
		{name: "empty path", input: "https://example.test", want: url.URL{Scheme: "https", Host: "example.test"}},
		// 用无协议的相对引用验证 HTTP URL 必须绝对。
		{name: "relative path", input: "path", wantErr: check.ErrInvalidValue},
		// 用含协议但不透明的 URI 验证必须采用层次型形式。
		{name: "opaque HTTP URI", input: "http:example.test", wantErr: check.ErrInvalidValue},
		// 用其他层次型协议验证协议限制。
		{name: "non-HTTP scheme", input: "ftp://example.test/path", wantErr: check.ErrInvalidValue},
		// 用端口代替主机验证要求非空主机名。
		{name: "port without hostname", input: "http://:80/path", wantErr: check.ErrInvalidValue},
		// 用非法转义验证语法错误在判断 HTTP 结构之前返回。
		{name: "malformed HTTP URL", input: "https://example.test/%", wantErr: check.ErrInvalidValue},
	})
}

type urlAllowCase struct {
	name    string
	value   *url.URL
	allowed []string
	wantErr error
}

func TestURLSchemeOneOf(t *testing.T) {
	testURLAllowList(t, format.URLSchemeOneOf, []urlAllowCase{
		// 用大小写不同的协议以及排在后面的选项验证折叠匹配。
		{name: "match later scheme ignoring case", value: &url.URL{Scheme: "https"}, allowed: []string{"http", "HTTPS"}},
		// 用包含数字和标点的协议验证允许的 URI 协议语法。
		{name: "valid scheme punctuation", value: &url.URL{Scheme: "v2+feature-beta.test"}, allowed: []string{"V2+Feature-Beta.Test"}},
		// 用合法但不同的协议验证名单没有匹配时返回输入错误。
		{name: "scheme outside allow list", value: &url.URL{Scheme: "ftp"}, allowed: []string{"https"}, wantErr: check.ErrInvalidValue},
		// 用没有协议的引用验证必须实际匹配名单中的协议。
		{name: "missing scheme", value: &url.URL{Path: "child"}, allowed: []string{"https"}, wantErr: check.ErrInvalidValue},
		// 用 nil 解析结果验证它不能匹配有效名单。
		{name: "nil URL", allowed: []string{"https"}, wantErr: check.ErrInvalidValue},
		// 用空名单验证必须提供规则。
		{name: "empty allow list", value: &url.URL{Scheme: "https"}, wantErr: check.ErrInvalidRule},
		// 用空协议项验证规则会检查单个协议。
		{name: "empty scheme rule", value: &url.URL{Scheme: "https"}, allowed: []string{""}, wantErr: check.ErrInvalidRule},
		// 用数字起始协议验证首字符限制。
		{name: "nonletter scheme prefix", value: &url.URL{Scheme: "https"}, allowed: []string{"1http"}, wantErr: check.ErrInvalidRule},
		// 在首个匹配项之后放非法字符，验证完整规则先校验。
		{name: "invalid suffix after matching rule", value: &url.URL{Scheme: "https"}, allowed: []string{"https", "ht_tp"}, wantErr: check.ErrInvalidRule},
	})
}

func TestURLHostnameOneOf(t *testing.T) {
	testURLAllowList(t, format.URLHostnameOneOf, []urlAllowCase{
		// 用大小写不同的 DNS 名和端口验证只比较主机名且折叠大小写。
		{name: "DNS name ignoring case and port", value: &url.URL{Host: "Api.Example.test:443"}, allowed: []string{"other.test", "api.example.test"}},
		// 用末尾根点完全一致的名称验证允许名单可以包含根点。
		{name: "matching root dots", value: &url.URL{Host: "Example.test."}, allowed: []string{"example.test."}},
		// 用带根点和不带根点的文本验证它们保持独立。
		{name: "different root dot", value: &url.URL{Host: "example.test."}, allowed: []string{"example.test"}, wantErr: check.ErrInvalidValue},
		// 用根域和子域验证此入口只接受精确主机匹配。
		{name: "subdomain differs", value: &url.URL{Host: "api.example.test"}, allowed: []string{"example.test"}, wantErr: check.ErrInvalidValue},
		// 用合法 Unicode 标签验证相同 Unicode 主机名可以匹配。
		{name: "matching Unicode hostname", value: &url.URL{Host: "bücher.example"}, allowed: []string{"bücher.example"}},
		// 用对应的 A-label 和 Unicode 文本验证入口不进行 IDNA 等价匹配。
		{name: "A-label differs from Unicode", value: &url.URL{Host: "bücher.example"}, allowed: []string{"xn--bcher-kva.example"}, wantErr: check.ErrInvalidValue},
		// 用展开和压缩的 IPv6 文本验证按地址值匹配。
		{name: "equivalent IPv6 address", value: &url.URL{Host: "[2001:0db8:0:0:0:0:0:1]:443"}, allowed: []string{"2001:db8::1"}},
		// 用相同地址和相同 zone 验证 zone 参与匹配。
		{name: "matching zone", value: &url.URL{Host: "[fe80::1%eth0]"}, allowed: []string{"fe80::1%eth0"}},
		// 仅更改 zone 的大小写，验证地址匹配保留 zone 的区别。
		{name: "zone case differs", value: &url.URL{Host: "[fe80::1%ETH0]"}, allowed: []string{"fe80::1%eth0"}, wantErr: check.ErrInvalidValue},
		// 用 mapped 地址和原生 IPv4 验证二者保持不同的地址值。
		{name: "mapped IPv4 differs", value: &url.URL{Host: "[::ffff:192.0.2.42]"}, allowed: []string{"192.0.2.42"}, wantErr: check.ErrInvalidValue},
		// 用 DNS 主机和先出现的 IP 项验证未匹配的 IP 项可以跳过。
		{name: "DNS match after IP", value: &url.URL{Host: "api.example.test"}, allowed: []string{"192.0.2.42", "api.example.test"}},
		// 用没有主机的路径验证输入必须含主机名。
		{name: "missing hostname", value: &url.URL{Path: "/child"}, allowed: []string{"example.test"}, wantErr: check.ErrInvalidValue},
		// 用 nil 解析结果验证此输入属于无效值。
		{name: "nil URL", allowed: []string{"example.test"}, wantErr: check.ErrInvalidValue},
		// 用空名单验证必须至少提供一条规则。
		{name: "empty allow list", value: &url.URL{Host: "example.test"}, wantErr: check.ErrInvalidRule},
		// 用空项验证每条主机规则都须非空。
		{name: "empty hostname rule", value: &url.URL{Host: "example.test"}, allowed: []string{""}, wantErr: check.ErrInvalidRule},
		// 给规则附加路径，验证规则项必须只含主机。
		{name: "hostname rule with path", value: &url.URL{Host: "example.test"}, allowed: []string{"example.test/path"}, wantErr: check.ErrInvalidRule},
		// 用带括号的 IP 规则验证名单使用裸地址。
		{name: "bracketed address rule", value: &url.URL{Host: "[2001:db8::1]"}, allowed: []string{"[2001:db8::1]"}, wantErr: check.ErrInvalidRule},
		// 给主机规则附加端口，验证端口不属于允许的规则内容。
		{name: "hostname rule with port", value: &url.URL{Host: "example.test"}, allowed: []string{"example.test:443"}, wantErr: check.ErrInvalidRule},
		// 用非法 UTF-8 验证规则文本的编码检查。
		{name: "invalid UTF-8 rule", value: &url.URL{Host: "example.test"}, allowed: []string{"exa\xffmple.test"}, wantErr: check.ErrInvalidRule},
		// 用 Unicode 空白验证禁止规则中出现空白字符。
		{name: "whitespace in hostname rule", value: &url.URL{Host: "example.test"}, allowed: []string{"example.test\u00a0"}, wantErr: check.ErrInvalidRule},
		// 在有效匹配项后放无效标签，验证完整名单先校验。
		{name: "invalid label after matching rule", value: &url.URL{Host: "example.test"}, allowed: []string{"example.test", "bad..test"}, wantErr: check.ErrInvalidRule},
	})
}

func TestURLHostnameWithinDomain(t *testing.T) {
	tests := []struct {
		name        string
		value       *url.URL
		domain      string
		includeRoot bool
		wantErr     error
	}{
		// 用两级子域和混合大小写验证按 DNS 标签边界折叠比较。
		{name: "nested subdomain", value: &url.URL{Host: "Deep.Api.Example.test:443"}, domain: "EXAMPLE.test"},
		// 用等于根域的名称验证显式开启根域许可时通过。
		{name: "root allowed", value: &url.URL{Host: "Example.test"}, domain: "example.test", includeRoot: true},
		// 用同一个根域验证默认只允许子域。
		{name: "root disallowed", value: &url.URL{Host: "example.test"}, domain: "example.test", wantErr: check.ErrInvalidValue},
		// 用末尾文本相同但无标签分界的名称验证拒绝。
		{name: "suffix without label boundary", value: &url.URL{Host: "anotherexample.test"}, domain: "example.test", wantErr: check.ErrInvalidValue},
		// 在目标域后添加标签，验证只有正确的后缀才通过。
		{name: "target domain in prefix", value: &url.URL{Host: "example.test.other"}, domain: "example.test", wantErr: check.ErrInvalidValue},
		// 用 Unicode 子域验证主机名须为 ASCII。
		{name: "non-ASCII hostname", value: &url.URL{Host: "bücher.example.test"}, domain: "example.test", wantErr: check.ErrInvalidValue},
		// 用带尾部根点的主机验证域内比较要求无根点。
		{name: "hostname with root dot", value: &url.URL{Host: "api.example.test."}, domain: "example.test", wantErr: check.ErrInvalidValue},
		// 用无主机的引用验证输入必须含主机名。
		{name: "missing hostname", value: &url.URL{Path: "/child"}, domain: "example.test", wantErr: check.ErrInvalidValue},
		// 用 nil 和有效规则验证缺少 URL 的错误分类。
		{name: "nil URL", domain: "example.test", wantErr: check.ErrInvalidValue},
		// 用带尾部点的域规则验证规则语法拒绝根点。
		{name: "root dot in rule", value: &url.URL{Host: "api.example.test"}, domain: "example.test.", wantErr: check.ErrInvalidRule},
		// 用通配域规则验证这里要求实际的 DNS 标签。
		{name: "wildcard in rule", value: &url.URL{Host: "api.example.test"}, domain: "*.example.test", wantErr: check.ErrInvalidRule},
		// 用 Unicode 域规则验证规则也要求 ASCII。
		{name: "non-ASCII rule", value: &url.URL{Host: "api.example.test"}, domain: "bücher.test", wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertURLError(t, format.URLHostnameWithinDomain(test.value, test.domain, test.includeRoot), test.wantErr)
		})
	}
}

func TestURLPortInRange(t *testing.T) {
	tests := []struct {
		name             string
		value            *url.URL
		minimum, maximum uint16
		allowAbsent      bool
		wantErr          error
	}{
		// 用端口 0 作为下端点验证显式零仍参与范围比较。
		{name: "zero lower endpoint", value: &url.URL{Host: "example.test:0"}, minimum: 0, maximum: 10},
		// 用 65535 验证显式最大端口和闭区间上端点。
		{name: "largest port", value: &url.URL{Host: "example.test:65535"}, minimum: 1, maximum: 65535},
		// 用带端口的括号 IPv6 验证地址冒号和端口分隔符正确区分。
		{name: "IPv6 with explicit port", value: &url.URL{Host: "[2001:db8::1]:443"}, minimum: 443, maximum: 443},
		// 用带前导零的端口验证比较使用解析后的十进制数值。
		{name: "port with leading zeros", value: &url.URL{Host: "example.test:0443"}, minimum: 443, maximum: 443},
		// 用低于范围一位的合法端口验证输入错误。
		{name: "below bounds", value: &url.URL{Host: "example.test:9"}, minimum: 10, maximum: 20, wantErr: check.ErrInvalidValue},
		// 用高于范围一位的合法端口验证输入错误。
		{name: "above bounds", value: &url.URL{Host: "example.test:21"}, minimum: 10, maximum: 20, wantErr: check.ErrInvalidValue},
		// 用超过 16 位的数字端口验证解析错误。
		{name: "overflowing port", value: &url.URL{Host: "example.test:65536"}, minimum: 0, maximum: 65535, wantErr: check.ErrInvalidValue},
		// 用无端口 HTTPS 地址验证允许缺失时无需猜测默认端口。
		{name: "absent port allowed", value: &url.URL{Scheme: "https", Host: "example.test"}, minimum: 1, maximum: 10, allowAbsent: true},
		// 用相同地址验证要求显式端口时不会使用协议的默认值。
		{name: "absent port disallowed", value: &url.URL{Scheme: "https", Host: "example.test"}, minimum: 443, maximum: 443, wantErr: check.ErrInvalidValue},
		// 用无端口的括号 IPv6 验证地址内部的冒号不计为端口。
		{name: "IPv6 without a port", value: &url.URL{Host: "[2001:db8::1]"}, minimum: 1, maximum: 10, allowAbsent: true},
		// 用 DNS 主机后的空端口验证它不会视作缺失。
		{name: "explicit empty port", value: &url.URL{Host: "example.test:"}, minimum: 1, maximum: 10, allowAbsent: true, wantErr: check.ErrInvalidValue},
		// 用 IPv6 括号后的空端口验证同一约束也适用于 IPv6。
		{name: "IPv6 with empty port", value: &url.URL{Host: "[2001:db8::1]:"}, minimum: 1, maximum: 10, allowAbsent: true, wantErr: check.ErrInvalidValue},
		// 直接构造非数字端口，验证结构化输入也不会绕过端口语法检查。
		{name: "explicit nondigit port", value: &url.URL{Host: "example.test:service"}, minimum: 1, maximum: 10, allowAbsent: true, wantErr: check.ErrInvalidValue},
		// 用未闭合的主机括号验证它不是合法的无端口形式。
		{name: "unclosed host bracket", value: &url.URL{Host: "[broken"}, minimum: 1, maximum: 10, allowAbsent: true, wantErr: check.ErrInvalidValue},
		// 用仅有端口的 authority 验证主机名不能为空。
		{name: "missing hostname", value: &url.URL{Host: ":10"}, minimum: 1, maximum: 10, wantErr: check.ErrInvalidValue},
		// 用 nil 和有效区间验证缺少 URL 的错误分类。
		{name: "nil URL", minimum: 1, maximum: 10, wantErr: check.ErrInvalidValue},
		// 用倒置区间和 nil URL 验证规则错误优先。
		{name: "reversed bounds", minimum: 10, maximum: 1, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertURLError(t, format.URLPortInRange(test.value, test.minimum, test.maximum, test.allowAbsent), test.wantErr)
		})
	}
}

type urlValueCase struct {
	name    string
	value   *url.URL
	wantErr error
}

func TestURLNoUserinfo(t *testing.T) {
	testURLValue(t, format.URLNoUserinfo, []urlValueCase{
		// 用省略 User 的地址验证普通主机可以通过。
		{name: "absent userinfo", value: &url.URL{Host: "example.test"}},
		// 用只含用户名的 userinfo 验证密码可以省略也应被拒绝。
		{name: "username only", value: &url.URL{Host: "example.test", User: url.User("reader")}, wantErr: check.ErrInvalidValue},
		// 用显式空用户名验证是否存在 userinfo 由结构决定。
		{name: "empty username", value: &url.URL{Host: "example.test", User: url.User("")}, wantErr: check.ErrInvalidValue},
		// 用 nil 验证缺少 URL 本身仍属于输入错误。
		{name: "nil URL", wantErr: check.ErrInvalidValue},
	})
}

func TestURLNoQuery(t *testing.T) {
	testURLValue(t, format.URLNoQuery, []urlValueCase{
		// 用没有查询字段的地址验证普通路径通过。
		{name: "absent query", value: &url.URL{Path: "/path"}},
		// 用非空 RawQuery 验证显式查询被拒绝。
		{name: "nonempty query", value: &url.URL{Path: "/path", RawQuery: "view=list"}, wantErr: check.ErrInvalidValue},
		// 用 ForceQuery 表示空查询标记，验证问号仍算查询。
		{name: "empty query marker", value: &url.URL{Path: "/path", ForceQuery: true}, wantErr: check.ErrInvalidValue},
		// 用 nil 验证缺少 URL 本身仍属于输入错误。
		{name: "nil URL", wantErr: check.ErrInvalidValue},
	})
}

func TestURLNoFragment(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		// 用有查询但无片段的引用验证入口只检查片段分隔符。
		{name: "query without fragment", input: "/path?view=list"},
		// 用转义的井号验证数据字符不构成片段标记。
		{name: "escaped hash", input: "/part%23name"},
		// 用空引用验证其通过解析且无片段。
		{name: "empty reference", input: ""},
		// 用有实际内容的片段验证普通片段被拒绝。
		{name: "fragment", input: "/path#heading", wantErr: check.ErrInvalidValue},
		// 用末尾的空片段标记验证必须检查原始文本。
		{name: "empty fragment marker", input: "/path#", wantErr: check.ErrInvalidValue},
		// 用无片段的非法转义验证入口仍先检查 URL 语法。
		{name: "malformed URL", input: "/part%", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertURLError(t, format.URLNoFragment(test.input), test.wantErr)
		})
	}
}

func testURLParser(t *testing.T, parse func(string) (*url.URL, error), tests []urlParseCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.input)
			assertURLError(t, err, test.wantErr)
			if test.wantErr == nil && !reflect.DeepEqual(got, &test.want) {
				t.Errorf("parse(%q) = %#v, want %#v", test.input, got, &test.want)
			}
		})
	}
}

func testURLAllowList(t *testing.T, validate func(*url.URL, ...string) error, tests []urlAllowCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertURLError(t, validate(test.value, test.allowed...), test.wantErr)
		})
	}
}

func testURLValue(t *testing.T, validate func(*url.URL) error, tests []urlValueCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertURLError(t, validate(test.value), test.wantErr)
		})
	}
}

func assertURLError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
