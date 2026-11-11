package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-service-template/env"
)

type loadLoggerConfigurationCase struct {
	name        string
	environment string
	want        configuration
}

func TestLoadConfiguration(t *testing.T) {
	dir := t.TempDir()
	writeLoggerConfigFixture(t, dir, env.DevEnv, "service_name: dev-service\nmin_level: DEBUG\n")
	writeLoggerConfigFixture(t, dir, env.TestEnv, "service_name: ' test-service '\nmin_level: WARN\nfile_path: 'logs/${LOG_NAME}.jsonl'\n")
	writeLoggerConfigFixture(t, dir, env.ProdEnv, "service_name: prod-service\n")
	t.Setenv("APP_ENV", env.DevEnv)
	t.Setenv("LOG_NAME", "substituted")

	tests := []loadLoggerConfigurationCase{
		// 开发配置只提供服务名和级别，验证可选日志路径保持为空。
		{name: "development file", environment: env.DevEnv, want: configuration{ServiceName: "dev-service", MinLevel: LevelDebug}},
		// 同时存在开发文件和 APP_ENV，验证仍按显式环境选择文件，并保留空格和路径占位符原文。
		{
			name: "test file preserves literals", environment: env.TestEnv,
			want: configuration{ServiceName: " test-service ", MinLevel: LevelWarn, FilePath: "logs/${LOG_NAME}.jsonl"},
		},
		// 生产文件省略最低级别，验证加载器保留零值交给构造阶段判断。
		{name: "production file without level", environment: env.ProdEnv, want: configuration{ServiceName: "prod-service"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := loadConfiguration(dir, test.environment)
			if err != nil || got != test.want {
				t.Errorf("loadConfiguration() = %+v, %v, want %+v, nil", got, err, test.want)
			}
		})
	}
}

type rejectLoggerConfigurationCase struct {
	name            string
	environment     string
	fileEnvironment string
	document        string
	blankDirectory  bool
	wantErrorPart   string
	wantCause       error
}

func TestLoadConfigurationRejectsInvalidInput(t *testing.T) {
	const validDocument = "service_name: sample\nmin_level: INFO\n"
	const privateFixtureValue = "private-config-fixture"
	tests := []rejectLoggerConfigurationCase{
		// 环境使用大写，验证加载器只接受项目规定的精确环境名。
		{name: "unsupported environment", environment: "TEST", wantErrorPart: "unsupported logger deployment environment"},
		// 目录只有空白，验证加载器不会从进程目录读取配置。
		{name: "blank directory", environment: env.TestEnv, blankDirectory: true, wantErrorPart: "configuration directory is required"},
		// 请求生产环境但只有开发文件，验证缺失错误被保留且不会回退。
		{
			name: "missing selected file", environment: env.ProdEnv, fileEnvironment: env.DevEnv,
			document: validDocument, wantErrorPart: "cannot read logger configuration file", wantCause: os.ErrNotExist,
		},
		// 错拼字段名并放入测试值，验证严格解码拒绝未知字段，错误不回显配置正文。
		{
			name: "unknown field", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			document: validDocument + "fil_path: " + privateFixtureValue + "\n", wantErrorPart: "invalid logger configuration YAML",
		},
		// 用序列代替服务名标量，验证字段类型错误在读取阶段被拒绝。
		{
			name: "invalid scalar type", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			document: "service_name: [" + privateFixtureValue + "]\nmin_level: INFO\n", wantErrorPart: "invalid logger configuration YAML",
		},
		// 同一文档重复定义服务名，验证不会静默选择后一个值。
		{
			name: "duplicate field", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			document: validDocument + "service_name: " + privateFixtureValue + "\n", wantErrorPart: "invalid logger configuration YAML",
		},
		// 破坏 YAML 的序列边界，验证无法解析的文档只返回固定错误。
		{
			name: "malformed document", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			document: "service_name: [" + privateFixtureValue + "\n", wantErrorPart: "invalid logger configuration YAML",
		},
		// 文件零字节且没有首份文档，验证不会产生可用配置。
		{
			name: "empty file", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			wantErrorPart: "invalid logger configuration YAML",
		},
		// 首份文档合法但随后还有配置，验证加载器拒绝忽略或合并第二份文档。
		{
			name: "two documents", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			document: validDocument + "---\nservice_name: " + privateFixtureValue + "\n", wantErrorPart: "exactly one YAML document",
		},
		// 第二份文档只有起始分隔符，验证空文档也计入文档数量。
		{
			name: "empty second document", environment: env.TestEnv, fileEnvironment: env.TestEnv,
			document: validDocument + "---\n", wantErrorPart: "exactly one YAML document",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if test.fileEnvironment != "" {
				writeLoggerConfigFixture(t, dir, test.fileEnvironment, test.document)
			}
			if test.blankDirectory {
				dir = " \t"
			}
			got, err := loadConfiguration(dir, test.environment)
			if err == nil || !strings.Contains(err.Error(), test.wantErrorPart) {
				t.Fatalf("loadConfiguration() error = %v, want error containing %q", err, test.wantErrorPart)
			}
			if got != (configuration{}) {
				t.Errorf("loadConfiguration() = %+v on failure, want zero configuration", got)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Errorf("loadConfiguration() error = %v, want cause %v", err, test.wantCause)
			}
			if strings.Contains(err.Error(), privateFixtureValue) {
				t.Error("loadConfiguration() disclosed a raw configuration value")
			}
		})
	}
}

func writeLoggerConfigFixture(t *testing.T, dir, environment, document string) {
	t.Helper()
	path := filepath.Join(dir, environment+".yml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write logger configuration %s: %v", path, err)
	}
}
