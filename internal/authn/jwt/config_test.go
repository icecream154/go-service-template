package jwt_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-service-template/internal/authn/jwt"
)

type loadJWTConfigCase struct {
	name        string
	environment string
	files       map[string]string
	blankDir    bool
	want        jwt.Config
	wantError   string
}

func TestLoadConfig(t *testing.T) {
	valid := jwtTestConfig()
	document := fmt.Sprintf("issuer: %s\naudience: %s\nsigning_key: %s\ntoken_ttl: 2m\n", valid.Issuer, valid.Audience, valid.SigningKey)
	const privateFixture = "fixture-value-must-stay-private"
	tests := []loadJWTConfigCase{
		// 开发文件内容损坏而目标环境文件完整，验证只读取请求的环境。
		{name: "selects requested environment", environment: "test", files: map[string]string{"dev": "issuer: [", "test": document}, want: valid},
		// 只放开发文件却选择生产环境，验证缺失时不会回退。
		{name: "missing file does not fall back", environment: "prod", files: map[string]string{"dev": document}, wantError: "cannot read JWT configuration file"},
		// 即使目录存在同名文件，也拒绝部署环境列表之外的名称。
		{name: "unsupported environment", environment: "local", files: map[string]string{"local": document}, wantError: "unsupported JWT deployment environment"},
		// 用纯空白目录隔离路径参数检查，验证不会尝试读取当前目录的配置。
		{name: "blank directory", environment: "test", blankDir: true, wantError: "JWT configuration directory is required"},
		// 空文件不会产生 YAML 文档，验证缺失配置被拒绝。
		{name: "empty document", environment: "test", files: map[string]string{"test": ""}, wantError: "valid nonempty YAML document"},
		// 未闭合的序列导致 YAML 语法错误，验证错误仅说明失败阶段。
		{name: "invalid YAML", environment: "test", files: map[string]string{"test": "issuer: [" + privateFixture}, wantError: "valid nonempty YAML document"},
		// 配置顶层为序列，验证只接受键值映射。
		{name: "not a mapping", environment: "test", files: map[string]string{"test": "- issuer"}, wantError: "must be a YAML mapping"},
		// 追加一份合法文档，验证加载器拒绝忽略第二份配置。
		{name: "multiple documents", environment: "test", files: map[string]string{"test": document + "---\n{}\n"}, wantError: "exactly one YAML document"},
		// 完整配置再增加未知键，验证拼写错误不会被静默忽略。
		{name: "unknown field", environment: "test", files: map[string]string{"test": document + "other: value\n"}, wantError: "unknown JWT configuration field"},
		// 使用布尔键，验证字段名必须是字符串。
		{name: "nonstring field name", environment: "test", files: map[string]string{"test": document + "true: value\n"}, wantError: "unknown JWT configuration field"},
		// 重复声明签发方，验证配置不会按最后一项覆盖。
		{name: "duplicate field", environment: "test", files: map[string]string{"test": document + "issuer: another\n"}, wantError: "duplicate JWT configuration field issuer"},
		// 显式写 null 无法提供接收方，验证 YAML 节点的类型检查。
		{name: "null field", environment: "test", files: map[string]string{"test": strings.Replace(document, "audience: "+valid.Audience, "audience: null", 1)}, wantError: "audience must be a string"},
		// 数字也可被一些 YAML 解码器转换为文本，验证此处要求原值就是字符串。
		{name: "numeric field", environment: "test", files: map[string]string{"test": strings.Replace(document, "token_ttl: 2m", "token_ttl: 2", 1)}, wantError: "token_ttl must be a string"},
		// 同时缺少前三项，验证始终按固定顺序报告缺少的签发方。
		{name: "missing fields", environment: "test", files: map[string]string{"test": "token_ttl: 2m\n"}, wantError: "issuer is required"},
		// 时长值是字符串却无法解析，验证不回显原文。
		{name: "invalid duration", environment: "test", files: map[string]string{"test": strings.Replace(document, "token_ttl: 2m", "token_ttl: "+privateFixture, 1)}, wantError: "valid duration string"},
		// 合法的时长字符串可被解析，但含小数秒会在加载后的业务校验阶段拒绝。
		{name: "fractional seconds after decoding", environment: "test", files: map[string]string{"test": strings.Replace(document, "token_ttl: 2m", "token_ttl: 1500ms", 1)}, wantError: "positive whole number of seconds"},
		// 即使同名环境变量已设置，配置中的占位符仍按原文校验并被拒绝。
		{name: "literal key placeholder", environment: "test", files: map[string]string{"test": strings.Replace(document, valid.SigningKey, "${JWT_TEST_SIGNING_KEY}", 1)}, wantError: "canonical standard Base64"},
	}
	t.Setenv("JWT_TEST_SIGNING_KEY", valid.SigningKey)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			for environment, content := range test.files {
				writeJWTConfigFile(t, directory, environment, content)
			}
			if test.blankDir {
				directory = " \t"
			}

			got, err := jwt.LoadConfig(directory, test.environment)
			assertJWTConfigError(t, err, test.wantError)
			if err != nil && (strings.Contains(err.Error(), privateFixture) || strings.Contains(err.Error(), valid.SigningKey)) {
				t.Error("LoadConfig() returned a fixture value in its error")
			}
			if got != test.want {
				t.Error("LoadConfig() returned different configuration fields than expected")
			}
		})
	}
}

type validateJWTConfigCase struct {
	name      string
	change    func(*jwt.Config)
	wantError string
}

func TestConfigValidate(t *testing.T) {
	valid := jwtTestConfig()
	const invalidKey = "invalid-fixture-secret!"
	tests := []validateJWTConfigCase{
		// 用已知有效的基线配置确认所有字段正确时通过。
		{name: "valid configuration"},
		// 非 ASCII 签发方和接收方都是有效 UTF-8，验证不会误拒绝。
		{name: "Unicode identifiers", change: func(c *jwt.Config) { c.Issuer, c.Audience = "签发方", "接收方" }},
		// 1 秒和 32 字节是有效期及解码后密钥长度的下界。
		{name: "minimum lifetime and key length", change: func(c *jwt.Config) { c.TokenTTL = time.Second }},
		// 长于最小限制的密钥也能使用，验证下界不被写成固定长度。
		{name: "longer key", change: func(c *jwt.Config) { c.SigningKey = base64.StdEncoding.EncodeToString(make([]byte, 33)) }},
		// 缺少签发方不能采用默认身份。
		{name: "empty issuer", change: func(c *jwt.Config) { c.Issuer = "" }, wantError: "issuer"},
		// 签发方带有前导空格，验证不会通过裁剪修复输入。
		{name: "issuer with whitespace", change: func(c *jwt.Config) { c.Issuer = " issuer" }, wantError: "issuer"},
		// JSON 编码会替换非法字节，验证配置阶段先拒绝无效签发方。
		{name: "invalid UTF-8 issuer", change: func(c *jwt.Config) { c.Issuer = "\xff" }, wantError: "issuer"},
		// 缺少接收方不能使凭证用于未指定的接收方。
		{name: "empty audience", change: func(c *jwt.Config) { c.Audience = "" }, wantError: "audience"},
		// 接收方带有尾随制表符，验证精确身份配置不会被改写。
		{name: "audience with whitespace", change: func(c *jwt.Config) { c.Audience = "audience\t" }, wantError: "audience"},
		// 无效 UTF-8 接收方应在签发前被拒绝。
		{name: "invalid UTF-8 audience", change: func(c *jwt.Config) { c.Audience = "\xff" }, wantError: "audience"},
		// 空密钥需要显式配置，验证不会生成默认密钥。
		{name: "missing key", change: func(c *jwt.Config) { c.SigningKey = "" }, wantError: "signing_key is required"},
		// 不属于标准 Base64 的文本应返回编码错误且不回显密钥。
		{name: "invalid key encoding", change: func(c *jwt.Config) { c.SigningKey = invalidKey }, wantError: "canonical standard Base64"},
		// Base64 解码器可以忽略换行，验证重新编码比较仍会拒绝它。
		{name: "noncanonical key with newline", change: func(c *jwt.Config) { c.SigningKey += "\n" }, wantError: "canonical standard Base64"},
		// 31 字节的编码文本可能与 32 字节一样长，验证以解码长度判定下界。
		{name: "short decoded key", change: func(c *jwt.Config) { c.SigningKey = base64.StdEncoding.EncodeToString(make([]byte, 31)) }, wantError: "at least 32 decoded bytes"},
		// 零表示没有有效期，验证必须使用正的时长。
		{name: "zero lifetime", change: func(c *jwt.Config) { c.TokenTTL = 0 }, wantError: "positive whole number of seconds"},
		// 500 毫秒为正但不足最小值，验证不接受亚秒有效期。
		{name: "short lifetime", change: func(c *jwt.Config) { c.TokenTTL = 500 * time.Millisecond }, wantError: "positive whole number of seconds"},
		// 1500 毫秒超过一秒仍不是整秒，验证不会截断有效期。
		{name: "fractional lifetime", change: func(c *jwt.Config) { c.TokenTTL = 1500 * time.Millisecond }, wantError: "positive whole number of seconds"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			if test.change != nil {
				test.change(&config)
			}
			err := config.Validate()
			assertJWTConfigError(t, err, test.wantError)
			if err != nil && strings.Contains(err.Error(), invalidKey) {
				t.Error("Validate() returned the raw fixture key in its error")
			}
		})
	}
}

func jwtTestConfig() jwt.Config {
	return jwt.Config{
		Issuer: "fixture-issuer", Audience: "fixture-audience",
		SigningKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		TokenTTL:   2 * time.Minute,
	}
}

func writeJWTConfigFile(t *testing.T, directory, environment, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, environment+".yml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture configuration: %v", err)
	}
}

func assertJWTConfigError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Errorf("configuration error = %v, want nil", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("configuration error = %v, want a message containing %q", err, want)
	}
}
