package snowflake_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-service-template/internal/snowflake"
)

type loadConfigCase struct {
	name        string
	environment string
	want        snowflake.Config
}

func TestLoadConfigSelectsEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeSnowflakeConfig(t, dir, "dev", "machine_id: 37\n")
	writeSnowflakeConfig(t, dir, "test", "machine_id: 0\n")
	writeSnowflakeConfig(t, dir, "prod", "machine_id: 127\n")

	tests := []loadConfigCase{
		// 三份文件的编号不同，验证开发环境读取 dev.yml 中的常规编号。
		{name: "development file", environment: "dev", want: snowflake.Config{MachineID: 37}},
		// 测试环境显式设置 0，验证最小编号有效并且不会替换成其他环境的编号。
		{name: "explicit zero in test file", environment: "test", want: snowflake.Config{MachineID: 0}},
		// 生产环境使用 7 位编号的上界，验证 127 可以正常加载。
		{name: "maximum in production file", environment: "prod", want: snowflake.Config{MachineID: 127}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := snowflake.LoadConfig(dir, test.environment)
			if err != nil {
				t.Fatalf("LoadConfig(%q) error = %v, want nil", test.environment, err)
			}
			if got != test.want {
				t.Errorf("LoadConfig(%q) = %+v, want %+v", test.environment, got, test.want)
			}
		})
	}
}

type loadConfigPathErrorCase struct {
	name         string
	environment  string
	useBlankPath bool
	fileEnv      string
	wantError    string
}

func TestLoadConfigRejectsInvalidPathInputs(t *testing.T) {
	tests := []loadConfigPathErrorCase{
		// 即使同名文件存在，也应拒绝部署环境允许值之外的名称。
		{name: "unsupported environment", environment: "staging", fileEnv: "staging", wantError: "unsupported snowflake deployment environment"},
		// 用纯空白目录验证缺少路径时会直接报错。
		{name: "blank directory", environment: "test", useBlankPath: true, wantError: "snowflake configuration directory is required"},
		// 只提供开发配置而请求生产环境，验证缺少 prod.yml 时不回退到 dev.yml。
		{name: "missing selected file", environment: "prod", fileEnv: "dev", wantError: "cannot read snowflake configuration file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := " \t"
			if !test.useBlankPath {
				dir = t.TempDir()
				writeSnowflakeConfig(t, dir, test.fileEnv, "machine_id: 23\n")
			}
			got, err := snowflake.LoadConfig(dir, test.environment)
			assertSnowflakeConfigError(t, got, err, test.wantError)
		})
	}
}

type loadConfigDocumentErrorCase struct {
	name      string
	document  string
	wantError string
}

func TestLoadConfigRejectsInvalidDocuments(t *testing.T) {
	const integerRangeError = "machine_id must be an integer between 0 and 127"
	tests := []loadConfigDocumentErrorCase{
		// 空文件没有 YAML 文档，应提示提供有效文档。
		{name: "empty file", document: "", wantError: "one valid nonempty YAML document"},
		// 未闭合的序列使首份文档语法无效，应报告文档解析失败。
		{name: "malformed YAML", document: "machine_id: [\n", wantError: "one valid nonempty YAML document"},
		// 第一份文档有效，追加第二份文档后仍应拒绝整份配置。
		{name: "multiple documents", document: "machine_id: 42\n---\nmachine_id: 7\n", wantError: "exactly one YAML document"},
		// 附加文档虽不完整，仍不能只接受有效的第一份文档。
		{name: "malformed trailing document", document: "machine_id: 42\n---\nmachine_id: [\n", wantError: "exactly one YAML document"},
		// 列表中的对象包含正确键，但根节点必须是映射。
		{name: "sequence root", document: "- machine_id: 42\n", wantError: "must be a YAML mapping"},
		// 根节点带有自定义标签，即使键值正确也应拒绝。
		{name: "custom mapping tag", document: "!snowflake\nmachine_id: 42\n", wantError: "must be a YAML mapping"},
		// 只写空映射验证省略字段不会被解释为显式编号 0。
		{name: "missing machine ID", document: "{}\n", wantError: "machine_id is required"},
		// 正确字段后加拼写错误的键，验证额外字段不会被忽略。
		{name: "unknown field", document: "machine_id: 42\nmachine: 7\n", wantError: "unknown snowflake configuration field"},
		// 整数键不是字段名，即使同时含有有效的 machine_id 也应拒绝。
		{name: "non-string field name", document: "machine_id: 42\n1: other\n", wantError: "unknown snowflake configuration field"},
		// 同名字段给出两个不同编号，验证不会按位置挑选一个。
		{name: "duplicate field", document: "machine_id: 42\nmachine_id: 7\n", wantError: "duplicate snowflake configuration field machine_id"},
		// null 有键但无整数值，验证不能作为显式编号 0。
		{name: "null machine ID", document: "machine_id: null\n", wantError: integerRangeError},
		// 数值恰好为整数的浮点数仍带浮点标签，应在整数解码前拒绝。
		{name: "floating-point machine ID", document: "machine_id: 42.0\n", wantError: integerRangeError},
		// 加引号的数字是字符串，应拒绝隐式转换。
		{name: "quoted machine ID", document: "machine_id: \"42\"\n", wantError: integerRangeError},
		// 用只有一项的列表验证字段必须是单个整数。
		{name: "sequence machine ID", document: "machine_id: [42]\n", wantError: integerRangeError},
		// -1 恰好低于下界，应拒绝负机器号。
		{name: "machine ID below minimum", document: "machine_id: -1\n", wantError: integerRangeError},
		// 128 恰好超过 7 位上界，应拒绝越界机器号。
		{name: "machine ID above maximum", document: "machine_id: 128\n", wantError: integerRangeError},
		// 使用比 int64 上界大 1 的整数，验证解码失败也会报告机器号范围错误。
		{name: "machine ID overflows int64", document: "machine_id: 9223372036854775808\n", wantError: integerRangeError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSnowflakeConfig(t, dir, "test", test.document)

			got, err := snowflake.LoadConfig(dir, "test")
			assertSnowflakeConfigError(t, got, err, test.wantError)
		})
	}
}

func writeSnowflakeConfig(t *testing.T, dir, environment, document string) {
	t.Helper()
	filePath := filepath.Join(dir, environment+".yml")
	if err := os.WriteFile(filePath, []byte(document), 0600); err != nil {
		t.Fatalf("write fixture %s: %v", filePath, err)
	}
}

func assertSnowflakeConfigError(t *testing.T, got snowflake.Config, err error, wantError string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Errorf("LoadConfig() error = %v, want a message containing %q", err, wantError)
	}
	if got != (snowflake.Config{}) {
		t.Errorf("LoadConfig() = %+v on failure, want zero Config", got)
	}
}
