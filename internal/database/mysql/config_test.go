package mysql_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go-service-template/internal/database/mysql"
)

const validMySQLConfigYAML = `Gormv2:
  Mysql:
    Connections:
      primary:
        Enabled: true
        SlowThreshold: 1
        LogLevel: warn
        Behavior:
          SingularTable: true
          DisableForeignKeyConstraintWhenMigrating: true
          NowFunc:
            Location: UTC
            Precision: millisecond
        Write:
          Host: mysql.example.test
          Port: 3306
          DataBase: app_db
          User: app_user
          Pass: fixture-password
          Charset: utf8mb4
          Collation: utf8mb4_0900_bin
          ParseTime: true
          Loc: UTC
          Session:
            TimeZone: "+00:00"
            TransactionIsolation: READ-COMMITTED
            SQLMode: ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION,NO_ZERO_DATE,NO_ZERO_IN_DATE,ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES
            Autocommit: 1
          TLS:
            Enabled: true
            CAFile: certificates/ca.pem
            ServerName: mysql.example.test
            MinVersion: "1.2"
          SetMaxOpenConns: 10
          SetMaxIdleConns: 2
          SetConnMaxLifetime: 60
          SetConnMaxIdleTime: 30
          ConnectTimeout: 5
          ReadTimeout: 5
          WriteTimeout: 5
        Read:
          Pass: ${MYSQL_CONFIG_TEST_UNUSED_READ}
      archive:
        Enabled: false
        Write:
          Pass: ${MYSQL_CONFIG_TEST_UNUSED_ARCHIVE}
`

func TestLoadConfig(t *testing.T) {
	// 用无效的 dev.yml 和有效的 test.yml 验证按环境选文件。密码带占位符和 YAML 特殊字符，验证只展开一次且跳过二次解析。
	configDir := t.TempDir()
	writeMySQLConfigFixture(t, configDir, "dev", "Gormv2: [")
	t.Setenv("MYSQL_CONFIG_TEST_LOG_LEVEL", "error")
	t.Setenv("MYSQL_CONFIG_TEST_HOST", "primary.example.test")
	t.Setenv("MYSQL_CONFIG_TEST_PASS_A", "${MYSQL_CONFIG_TEST_PASS_B}: #\n")
	t.Setenv("MYSQL_CONFIG_TEST_PASS_B", "suffix")
	t.Setenv("MYSQL_CONFIG_TEST_CA", "certificates/test-ca.pem")
	t.Setenv("MYSQL_CONFIG_TEST_UNUSED_READ", "")
	t.Setenv("MYSQL_CONFIG_TEST_UNUSED_ARCHIVE", "")
	document := strings.NewReplacer(
		"LogLevel: warn", "LogLevel: ${MYSQL_CONFIG_TEST_LOG_LEVEL}",
		"Host: mysql.example.test", "Host: ${MYSQL_CONFIG_TEST_HOST}",
		"Pass: fixture-password", "Pass: 'prefix$LITERAL-${MYSQL_CONFIG_TEST_PASS_A}${MYSQL_CONFIG_TEST_PASS_B}'",
		"CAFile: certificates/ca.pem", "CAFile: ${MYSQL_CONFIG_TEST_CA}",
	).Replace(validMySQLConfigYAML)
	filePath := writeMySQLConfigFixture(t, configDir, "test", document)

	config, err := mysql.LoadConfig(configDir, "test")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.FilePath != filePath || !filepath.IsAbs(config.FilePath) {
		t.Errorf("FilePath = %q, want absolute path %q", config.FilePath, filePath)
	}
	primary, exists := config.Connections["primary"]
	if !exists || !primary.Enabled {
		t.Fatal("expected an enabled primary connection from test.yml")
	}
	if primary.LogLevel != "error" || primary.Write.Host != "primary.example.test" || primary.Write.Port != 3306 {
		t.Errorf("LogLevel, Host, Port = %q, %q, %d, want error, primary.example.test, 3306", primary.LogLevel, primary.Write.Host, primary.Write.Port)
	}
	if primary.Write.Pass != "prefix$LITERAL-${MYSQL_CONFIG_TEST_PASS_B}: #\nsuffix" {
		t.Error("Pass did not preserve the literal dollar sign and replacement text")
	}
	if want := filepath.Join(configDir, "certificates", "test-ca.pem"); primary.Write.TLS.CAFile != want {
		t.Errorf("CAFile = %q, want %q", primary.Write.TLS.CAFile, want)
	}
	if primary.Read == nil || primary.Read.Pass != "${MYSQL_CONFIG_TEST_UNUSED_READ}" {
		t.Error("expected the unused read password placeholder to remain literal")
	}
	archive, exists := config.Connections["archive"]
	if !exists || archive.Enabled || archive.Write.Pass != "${MYSQL_CONFIG_TEST_UNUSED_ARCHIVE}" {
		t.Error("expected the disabled connection and its password placeholder to remain intact")
	}
}

type loadMySQLConfigErrorCase struct {
	name        string
	environment string
	document    string
	want        string
}

func TestLoadConfigRejectsInvalidInput(t *testing.T) {
	const malformedSecret = "fixture-secret-that-must-stay-private"
	const absentVariable = "MYSQL_CONFIG_TEST_MISSING_PASS"
	t.Setenv(absentVariable, "")
	if err := os.Unsetenv(absentVariable); err != nil {
		t.Fatalf("unset fixture environment variable: %v", err)
	}
	t.Setenv("MYSQL_CONFIG_TEST_EMPTY_PASS", "")

	tests := []loadMySQLConfigErrorCase{
		// 传入非部署环境名称，验证不会尝试读取对应名称的文件。
		{name: "unsupported environment", environment: "local", want: "unsupported MySQL deployment environment"},
		// 只创建有效的 dev.yml，验证缺失 prod.yml 时不会回退读取开发配置。
		{name: "selected file missing", environment: "prod", want: "cannot read MySQL configuration file"},
		// 放入未闭合的序列，验证 YAML 语法错误不回显文档内容。
		{name: "malformed YAML", environment: "test", document: "Gormv2: [" + malformedSecret, want: "invalid MySQL configuration YAML"},
		// 把嵌套端口改为浮点数，验证节点检查能找到它且不会将其截断为整数。
		{name: "nested float", environment: "test", document: strings.Replace(validMySQLConfigYAML, "Port: 3306", "Port: 3306.5", 1), want: "floating-point values are not supported"},
		// 把端口值改为不可转换的字符串，验证类型错误不会回显原始标量。
		{name: "wrong scalar type", environment: "test", document: strings.Replace(validMySQLConfigYAML, "Port: 3306", "Port: "+malformedSecret, 1), want: "check field names and scalar types"},
		// 将端口字段拼错，验证严格解码会拒绝未知字段。
		{name: "unknown field", environment: "test", document: strings.Replace(validMySQLConfigYAML, "Port: 3306", "Porrt: 3306", 1), want: "check field names and scalar types"},
		// 在有效配置后追加第二个文档，验证加载器拒绝只读取首份的歧义输入。
		{name: "multiple documents", environment: "test", document: validMySQLConfigYAML + "---\n{}\n", want: "exactly one YAML document"},
		// 在密码中使用非法变量名，验证错误定位到字段并隐藏原始密码。
		{name: "malformed placeholder", environment: "test", document: strings.Replace(validMySQLConfigYAML, "fixture-password", malformedSecret+"${WRONG-NAME}", 1), want: "Connections.primary.Write.Pass: malformed environment variable placeholder"},
		// 用未设置的变量替换密码，验证错误包含变量名和实际配置字段。
		{name: "missing placeholder variable", environment: "test", document: strings.Replace(validMySQLConfigYAML, "fixture-password", "${"+absentVariable+"}", 1), want: absentVariable + " required by Connections.primary.Write.Pass"},
		// 让变量存在但为空，验证不会把空字符串静默作为有效替换值。
		{name: "empty placeholder variable", environment: "test", document: strings.Replace(validMySQLConfigYAML, "fixture-password", "${MYSQL_CONFIG_TEST_EMPTY_PASS}", 1), want: "MYSQL_CONFIG_TEST_EMPTY_PASS required by Connections.primary.Write.Pass"},
		// 端口语法合法但超过范围，验证 YAML 解码后仍执行配置校验。
		{name: "invalid decoded config", environment: "test", document: strings.Replace(validMySQLConfigYAML, "Port: 3306", "Port: 65536", 1), want: "Connections.primary.Write.Port"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configDir := t.TempDir()
			writeMySQLConfigFixture(t, configDir, "dev", validMySQLConfigYAML)
			if test.document != "" {
				writeMySQLConfigFixture(t, configDir, test.environment, test.document)
			}
			config, err := mysql.LoadConfig(configDir, test.environment)
			assertMySQLLoadConfigError(t, config, err, test.want)
			if strings.Contains(err.Error(), malformedSecret) {
				t.Error("LoadConfig() error disclosed the fixture's raw value")
			}
		})
	}

	// 传入纯空白目录，验证加载器报告缺少目录而不从当前目录查找。
	t.Run("blank config directory", func(t *testing.T) {
		config, err := mysql.LoadConfig(" \t", "test")
		assertMySQLLoadConfigError(t, config, err, "MySQL configuration directory is required")
	})
}

type loadMySQLTLSCase struct {
	name        string
	environment string
	host        string
	want        string
}

func TestLoadConfigAppliesEnvironmentTLSPolicy(t *testing.T) {
	t.Setenv("MYSQL_CONFIG_TEST_UNUSED_CA", "")
	tests := []loadMySQLTLSCase{
		// 本机 Docker 地址只在开发环境可关闭 TLS，且关闭后跳过 CA 变量展开。
		{name: "development Docker host", environment: "dev", host: "host.docker.internal"},
		// 使用普通远端地址，即使处于开发环境也应要求 TLS。
		{name: "development remote host", environment: "dev", host: "mysql.example.test", want: "TLS may only be disabled for a loopback address"},
		// 测试环境连回环地址也要求 TLS，避免误用开发环境豁免。
		{name: "test loopback", environment: "test", host: "127.0.0.1", want: "TLS is required outside the development environment"},
		// 生产环境连回环地址也要求 TLS，验证配置文件的环境参与校验。
		{name: "production loopback", environment: "prod", host: "127.0.0.1", want: "TLS is required outside the development environment"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configDir := t.TempDir()
			document := strings.NewReplacer(
				"Host: mysql.example.test", "Host: "+test.host,
				"TLS:\n            Enabled: true", "TLS:\n            Enabled: false",
				"CAFile: certificates/ca.pem", "CAFile: ${MYSQL_CONFIG_TEST_UNUSED_CA}",
			).Replace(validMySQLConfigYAML)
			writeMySQLConfigFixture(t, configDir, test.environment, document)

			config, err := mysql.LoadConfig(configDir, test.environment)
			if test.want != "" {
				assertMySQLLoadConfigError(t, config, err, "Connections.primary.Write.TLS.Enabled: "+test.want)
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			tls := config.Connections["primary"].Write.TLS
			if tls.Enabled || tls.CAFile != "${MYSQL_CONFIG_TEST_UNUSED_CA}" {
				t.Errorf("disabled TLS config = %+v, want an unchanged CA placeholder", tls)
			}
		})
	}

	// 给启用 TLS 的端点传入绝对路径，验证该路径不被拼接或在加载阶段读取。
	t.Run("absolute CA file", func(t *testing.T) {
		configDir := t.TempDir()
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		document := strings.Replace(validMySQLConfigYAML, "CAFile: certificates/ca.pem", "CAFile: "+strconv.Quote(caFile), 1)
		writeMySQLConfigFixture(t, configDir, "prod", document)

		config, err := mysql.LoadConfig(configDir, "prod")
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if got := config.Connections["primary"].Write.TLS.CAFile; got != caFile {
			t.Errorf("CAFile = %q, want %q", got, caFile)
		}
	})
}

func writeMySQLConfigFixture(t *testing.T, configDir, environment, document string) string {
	t.Helper()
	filePath := filepath.Join(configDir, environment+".yml")
	if err := os.WriteFile(filePath, []byte(document), 0o600); err != nil {
		t.Fatalf("write fixture configuration: %v", err)
	}
	return filePath
}

func assertMySQLLoadConfigError(t *testing.T, config mysql.Config, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("LoadConfig() error = %v, want a message containing %q", err, want)
	}
	if !reflect.DeepEqual(config, mysql.Config{}) {
		t.Error("LoadConfig() returned partial configuration after a failure")
	}
}
