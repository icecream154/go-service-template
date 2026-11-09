package jwt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// minSigningKeyBytes 是 HS256 密钥解码后的最小字节数，不是 Base64 文本长度。
const minSigningKeyBytes = 32

// Config 保存 JWT 的签发和验证配置，包含敏感签名密钥，不记录整个配置对象。
// 所有字段必须显式配置；算法固定为 HS256，不由配置或收到的凭证选择。
type Config struct {
	// Issuer 是本组件签发并接受的签发方，必须为有效 UTF-8、非空且不含首尾空白，验证时精确匹配。
	Issuer string `yaml:"issuer"`
	// Audience 是凭证的预期接收方，必须为有效 UTF-8、非空且不含首尾空白，签发和验证使用同一值。
	Audience string `yaml:"audience"`
	// SigningKey 直接保存签名密钥的标准 Base64 文本，解码后至少为 32 字节安全随机数据。
	// 此值来自配置文件本身，不作为环境变量名，也不展开 ${NAME} 占位符。
	SigningKey string `yaml:"signing_key"`
	// TokenTTL 是每次签发的有效时长，至少 1 秒且必须为整秒，例如 YAML 中的 1h。
	// 此值不控制认证方法自身的执行超时，也不会在有效期结束后自动刷新凭证。
	TokenTTL time.Duration `yaml:"token_ttl"`
}

// LoadConfig 按部署环境读取唯一 JWT 配置文件，包括其中的签名密钥，不创建认证器。
// configDir 是直接包含 dev.yml、test.yml、prod.yml 的叶子目录，缺少文件时不回退。
// YAML 必须恰好包含四个字符串字段；拒绝未知、重复、缺失、null 和多文档输入。
func LoadConfig(configDir, environment string) (Config, error) {
	switch environment {
	case "dev", "test", "prod":
	default:
		return Config{}, errors.New("unsupported JWT deployment environment")
	}
	if strings.TrimSpace(configDir) == "" {
		return Config{}, errors.New("JWT configuration directory is required")
	}
	filePath, err := filepath.Abs(filepath.Join(configDir, environment+".yml"))
	if err != nil {
		return Config{}, errors.New("cannot resolve JWT configuration path")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read JWT configuration file %q", filePath)
	}
	return decodeConfig(data)
}

// Validate 检查配置字段及密钥的存在、编码和长度，不查询环境变量。
// 不填入默认值或规范化原值，错误仅说明约束，不回显密钥文本。
func (config Config) Validate() error {
	// JSON 会替换非法 UTF-8 字节，必须在签发前拒绝，避免写入凭证的值与配置不同。
	if !utf8.ValidString(config.Issuer) || config.Issuer == "" || strings.TrimSpace(config.Issuer) != config.Issuer {
		return errors.New("JWT issuer must be valid UTF-8 and nonempty without surrounding whitespace")
	}
	if !utf8.ValidString(config.Audience) || config.Audience == "" || strings.TrimSpace(config.Audience) != config.Audience {
		return errors.New("JWT audience must be valid UTF-8 and nonempty without surrounding whitespace")
	}
	if _, err := config.decodeSigningKey(); err != nil {
		return err
	}
	if config.TokenTTL < time.Second || config.TokenTTL%time.Second != 0 {
		return errors.New("JWT token_ttl must be a positive whole number of seconds")
	}
	return nil
}

// decodeConfig 直接检查 YAML 节点，避免数字或布尔值被隐式转换成字符串。
// duration 的解析错误可能包含输入原文，因此仅返回固定字段说明，不传播原始解码消息。
func decodeConfig(data []byte) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Config{}, errors.New("JWT configuration must contain one valid nonempty YAML document")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("JWT configuration must contain exactly one YAML document")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode || document.Content[0].Tag != "!!map" {
		return Config{}, errors.New("JWT configuration must be a YAML mapping")
	}

	var config Config
	var tokenTTL string
	fields := map[string]*string{
		"issuer": &config.Issuer, "audience": &config.Audience,
		"signing_key": &config.SigningKey, "token_ttl": &tokenTTL,
	}
	seen := make(map[string]bool, len(fields))
	mapping := document.Content[0]
	for i := 0; i < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		target, exists := fields[key.Value]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !exists {
			return Config{}, errors.New("unknown JWT configuration field")
		}
		if seen[key.Value] {
			return Config{}, fmt.Errorf("duplicate JWT configuration field %s", key.Value)
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return Config{}, fmt.Errorf("JWT configuration field %s must be a string", key.Value)
		}
		seen[key.Value] = true
		*target = value.Value
	}
	// 固定顺序报告缺失字段，避免 map 遍历顺序改变错误结果。
	for _, name := range []string{"issuer", "audience", "signing_key", "token_ttl"} {
		if !seen[name] {
			return Config{}, fmt.Errorf("JWT configuration field %s is required", name)
		}
	}
	var err error
	config.TokenTTL, err = time.ParseDuration(tokenTTL)
	if err != nil {
		return Config{}, errors.New("JWT token_ttl must be a valid duration string")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// decodeSigningKey 解码配置中的密钥，供 Validate 检查及 Provider 构造时取得独立字节切片。
// 只接受规范的标准 Base64 文本，重新编码比较还会拒绝解码器默认容忍的 CR/LF。
// 密钥长度不能证明随机性，部署方必须使用密码学安全随机源生成并持久保存密钥。
// 不返回原始密钥或解码错误，不提供默认密钥，也不在缺失时自动生成临时密钥。
func (config Config) decodeSigningKey() ([]byte, error) {
	encoded := config.SigningKey
	if encoded == "" {
		return nil, errors.New("JWT signing_key is required")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(key) != encoded {
		return nil, errors.New("JWT signing key must be canonical standard Base64 without whitespace")
	}
	if len(key) < minSigningKeyBytes {
		return nil, errors.New("JWT signing key must contain at least 32 decoded bytes")
	}
	return key, nil
}
