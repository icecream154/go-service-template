package mysql

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfig 按已确定的部署环境读取唯一文件，不回退其他环境，不建立数据库连接。
// configDir 是直接包含 dev.yml、test.yml、prod.yml 的 mysql 目录，而非配置根目录。
// 整个流程依次完成 YAML 解码、字符串占位符展开、统一校验和 CA 路径规范化。
// 失败时不返回部分配置；错误只标识阶段、字段或变量名，绝不包含密码和原始解码错误。
func LoadConfig(configDir, environment string) (Config, error) {
	switch environment {
	case "dev", "test", "prod":
	default:
		return Config{}, fmt.Errorf("unsupported MySQL deployment environment")
	}
	if strings.TrimSpace(configDir) == "" {
		return Config{}, fmt.Errorf("MySQL configuration directory is required")
	}
	filePath, err := filepath.Abs(filepath.Join(configDir, environment+".yml"))
	if err != nil {
		return Config{}, fmt.Errorf("cannot resolve MySQL configuration path")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read MySQL configuration file %q", filePath)
	}
	config, err := decodeConfig(data)
	if err != nil {
		return Config{}, err
	}
	config.FilePath = filePath
	config.environment = environment
	for _, name := range sortedConnectionNames(config.Connections) {
		connection := config.Connections[name]
		if !connection.Enabled {
			continue // 禁用连接可以保留未注入的凭据变量，不影响当前环境启动。
		}
		if err := expandConnection(&connection, "Connections."+name); err != nil {
			return Config{}, err
		}
		config.Connections[name] = connection
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	// 统一校验已确认 CA 路径非空；此时再解析相对路径，不会把空值误变成目录。
	for name, connection := range config.Connections {
		if connection.Enabled && connection.Write.TLS.Enabled && !filepath.IsAbs(connection.Write.TLS.CAFile) {
			connection.Write.TLS.CAFile = filepath.Join(filepath.Dir(filePath), connection.Write.TLS.CAFile)
			config.Connections[name] = connection
		}
	}
	return config, nil
}

// decodeConfig 采用与参考项目相同的强类型解码方式：拒绝未知字段和多文档输入。
// yaml.v3 会将浮点数截断后赋给 int，因此先做很小的标签检查；本配置没有浮点字段。
// YAML 锚点和合并键仍交给库处理，不自行实现合并或类型转换。
// 其他标量沿用库的标准规则，例如整数字面量可解为字符串；需要固定字符串字面值时
// 应在 YAML 中用引号包住它，不再通过通用转换钩子额外定义一套标量转换规则。
func decodeConfig(data []byte) (Config, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return Config{}, fmt.Errorf("invalid MySQL configuration YAML")
	}
	if containsFloat(&node) {
		return Config{}, fmt.Errorf("invalid MySQL configuration YAML: floating-point values are not supported")
	}
	var document struct {
		Gormv2 struct {
			Mysql Config `yaml:"Mysql"`
		} `yaml:"Gormv2"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return Config{}, fmt.Errorf("invalid MySQL configuration YAML: check field names and scalar types")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("MySQL configuration must contain exactly one YAML document")
	}
	return document.Gormv2.Mysql, nil
}

// containsFloat 只检查原文档的节点内容，不沿 Alias 回访锚点，避免循环引用。
// 锚点原始定义已位于 Content 中；这里只拒绝浮点标签，不解释任何配置值。
func containsFloat(node *yaml.Node) bool {
	if node.Tag == "!!float" {
		return true
	}
	return slices.ContainsFunc(node.Content, containsFloat)
}

// expandConnection 显式列出允许插值的字符串，维护者可直接核对字段，不使用通用反射。
// 增加字符串字段时在此同步登记；Read 尚未启用，因此不访问它的占位符。
// 固定枚举字段也保留既有的插值能力，展开后仍须通过 Validate 检查允许值。
func expandConnection(connection *ConnectionConfig, path string) error {
	endpoint := &connection.Write
	// 每项显式绑定配置路径和目标字段，错误可定位字段，赋值只作用于当前连接副本。
	type stringField struct {
		path  string
		value *string
	}
	fields := []stringField{
		{"LogLevel", &connection.LogLevel},
		{"Behavior.NowFunc.Location", &connection.Behavior.NowFunc.Location},
		{"Behavior.NowFunc.Precision", &connection.Behavior.NowFunc.Precision},
		{"Write.Host", &endpoint.Host}, {"Write.DataBase", &endpoint.DataBase},
		{"Write.User", &endpoint.User}, {"Write.Pass", &endpoint.Pass},
		{"Write.Charset", &endpoint.Charset}, {"Write.Collation", &endpoint.Collation},
		{"Write.Loc", &endpoint.Loc}, {"Write.Session.TimeZone", &endpoint.Session.TimeZone},
		{"Write.Session.TransactionIsolation", &endpoint.Session.TransactionIsolation},
		{"Write.Session.SQLMode", &endpoint.Session.SQLMode},
	}
	if endpoint.TLS.Enabled {
		fields = append(fields,
			stringField{"Write.TLS.CAFile", &endpoint.TLS.CAFile},
			stringField{"Write.TLS.ServerName", &endpoint.TLS.ServerName},
			stringField{"Write.TLS.MinVersion", &endpoint.TLS.MinVersion},
		)
	}
	for _, field := range fields {
		value, err := expandPlaceholders(*field.value, path+"."+field.path)
		if err != nil {
			return err
		}
		*field.value = value
	}
	return nil
}

// placeholderPattern 只识别 ${NAME}，不会将普通美元符号或 $NAME 当作配置占位符。
var placeholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandPlaceholders 在解码后执行一次字面替换，不把密码重新解析为 YAML 或再次展开。
// 残留语法检查针对原字符串，替换得到的密码即使含有 ${OTHER} 也保持原样。
// 缺失或空变量只在错误中列出字段路径和变量名，不回显字段值。
func expandPlaceholders(value, path string) (string, error) {
	if strings.Contains(placeholderPattern.ReplaceAllString(value, ""), "${") {
		return "", invalidField(path, "malformed environment variable placeholder")
	}
	var expansionErr error
	expanded := placeholderPattern.ReplaceAllStringFunc(value, func(placeholder string) string {
		name := placeholder[2 : len(placeholder)-1]
		replacement, ok := os.LookupEnv(name)
		if (!ok || replacement == "") && expansionErr == nil {
			expansionErr = fmt.Errorf("environment variable %s required by %s is missing or empty", name, path)
		}
		return replacement
	})
	return expanded, expansionErr
}
