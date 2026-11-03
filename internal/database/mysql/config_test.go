package mysql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 使用合成配置验证 YAML 合并和敏感字符串处理，不连接实际数据库。
const testConfigYAML = `Gormv2:
  Mysql:
    Connections:
      user: &connection
        Enabled: true
        IsOpenReadDb: false
        Read:
          Pass: "${MYSQL_CONFIG_UNUSED_READ_PASSWORD}"
        SlowThreshold: 1
        LogLevel: warn
        Behavior:
          SkipDefaultTransaction: false
          PrepareStmt: false
          SuppressRecordNotFound: false
          TranslateError: false
          SingularTable: true
          DisableForeignKeyConstraintWhenMigrating: true
          AutoMigrate: false
          NowFunc:
            Location: UTC
            Precision: millisecond
        Write: &writer
          Host: 127.0.0.1
          Port: 3306
          DataBase: user_service
          User: test_user
          Pass: "${MYSQL_CONFIG_TEST_PASSWORD}"
          Charset: utf8mb4
          Collation: utf8mb4_0900_bin
          ParseTime: true
          Loc: UTC
          MultiStatements: false
          Session:
            TimeZone: "+00:00"
            TransactionIsolation: READ-COMMITTED
            Autocommit: 1
            SQLMode: ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION
          TLS:
            Enabled: false
            CAFile: "${MYSQL_CONFIG_UNUSED_CA}"
            ServerName: ""
            MinVersion: ""
            InsecureSkipVerify: false
            AllowFallbackToPlaintext: false
          SetMaxOpenConns: 10
          SetMaxIdleConns: 2
          SetConnMaxLifetime: 180
          SetConnMaxIdleTime: 60
          ConnectTimeout: 5
          ReadTimeout: 10
          WriteTimeout: 10
      post:
        <<: *connection
        Write:
          <<: *writer
          DataBase: post_service
      order:
        <<: *connection
        Write:
          <<: *writer
          DataBase: order_service
      disabled:
        Enabled: false
        Write:
          Pass: "${MYSQL_CONFIG_DISABLED_PASSWORD}"
`

// writeTestConfig 将合成配置写入独立目录，不改动真实环境文件或依赖本机 MySQL。
func writeTestConfig(t *testing.T, environment, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, environment+".yml"), []byte(content), 0600); err != nil {
		t.Fatal("cannot write synthetic configuration")
	}
	return dir
}

// TestLoadConfigResolvesAnchorsBeforeExpandingStrings 同时验证锚点继承和密码按字面替换。
// 密码故意包含换行、YAML 片段和另一占位符，防止实现退回原文替换或二次展开。
func TestLoadConfigResolvesAnchorsBeforeExpandingStrings(t *testing.T) {
	password := "literal:${DO_NOT_EXPAND}:#\"\n  Enabled: false"
	t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", password)
	dir := writeTestConfig(t, "dev", testConfigYAML)
	config, err := LoadConfig(dir, "dev")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !filepath.IsAbs(config.FilePath) {
		t.Error("configuration path must be absolute")
	}
	for _, name := range []string{"user", "post", "order"} {
		connection := config.Connections[name]
		if !connection.Enabled || connection.Write.DataBase != name+"_service" || connection.Write.Host == "" {
			t.Errorf("anchor merge failed for connection %s", name)
		}
		if connection.Write.Pass != password {
			t.Errorf("password was reparsed or recursively expanded for connection %s", name)
		}
	}
	if config.Connections["disabled"].Write.Pass != "${MYSQL_CONFIG_DISABLED_PASSWORD}" {
		t.Error("disabled connection must not expand its placeholders")
	}
	if config.Connections["user"].Write.TLS.CAFile != "${MYSQL_CONFIG_UNUSED_CA}" {
		t.Error("disabled TLS must not expand certificate placeholders")
	}
	if config.Connections["user"].Read.Pass != "${MYSQL_CONFIG_UNUSED_READ_PASSWORD}" {
		t.Error("unused read endpoint must not expand its placeholders")
	}
	connection := config.Connections["post"]
	connection.Write.Host = "changed.invalid"
	config.Connections["post"] = connection
	if config.Connections["user"].Write.Host == connection.Write.Host {
		t.Error("anchor decoding must not make endpoint values share mutable state")
	}
}

// TestLoadConfigMissingAndEmptyVariablesFailWithoutLeakingValues 区分未设置和显式空值。
// 两者都必须失败，错误需定位变量及字段，不能暴露配置值。
func TestLoadConfigMissingAndEmptyVariablesFailWithoutLeakingValues(t *testing.T) {
	for _, unset := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "missing"}[unset], func(t *testing.T) {
			t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", "")
			if unset {
				if err := os.Unsetenv("MYSQL_CONFIG_TEST_PASSWORD"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := LoadConfig(writeTestConfig(t, "dev", testConfigYAML), "dev")
			if err == nil || !strings.Contains(err.Error(), "MYSQL_CONFIG_TEST_PASSWORD") || !strings.Contains(err.Error(), ".Write.Pass") {
				t.Fatal("missing variable error must identify its name and field path")
			}
		})
	}
}

// TestLoadConfigRejectsInvalidSchemaWithoutLeakingInput 保留严格解码和敏感值保护的回归用例。
// 尤其不能把端口浮点数截断为整数，或将超大整数转换溢出后继续使用。
func TestLoadConfigRejectsInvalidSchemaWithoutLeakingInput(t *testing.T) {
	t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", "synthetic-password")
	const marker = "SENSITIVE_SCHEMA_MARKER"
	cases := map[string]string{
		"unknown field":         strings.Replace(testConfigYAML, "        LogLevel: warn", "        UnknownField: "+marker, 1),
		"wrong type":            strings.Replace(testConfigYAML, "          Port: 3306", "          Port: "+marker, 1),
		"string boolean":        strings.Replace(testConfigYAML, "        Enabled: true", "        Enabled: \"true\"", 1),
		"float integer":         strings.Replace(testConfigYAML, "          Port: 3306", "          Port: 3306.5", 1),
		"overflow integer":      strings.Replace(testConfigYAML, "          Port: 3306", "          Port: 18446744073709551615", 1),
		"invalid YAML":          "Gormv2: [" + marker + "\n",
		"unknown null":          strings.Replace(testConfigYAML, "        LogLevel: warn", "        UnknownField: null\n        LogLevel: warn", 1),
		"second document":       testConfigYAML + "\n---\nGormv2: {}\n",
		"empty second document": testConfigYAML + "\n---\n",
		"malformed placeholder": strings.Replace(testConfigYAML, "${MYSQL_CONFIG_TEST_PASSWORD}", "${INVALID-NAME}", 1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfig(writeTestConfig(t, "dev", content), "dev")
			if err == nil {
				t.Fatal("invalid YAML/schema was accepted")
			}
			if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "synthetic-password") {
				t.Fatal("configuration errors must not reveal input values")
			}
		})
	}
}

// TestLoadConfigExpandsNonCredentialFields 确认显式字段列表保留原有枚举、会话字符串插值能力。
// 展开后仍由统一校验检查值，不因精简实现而只支持账号和密码变量。
func TestLoadConfigExpandsNonCredentialFields(t *testing.T) {
	t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", "synthetic-password")
	content := testConfigYAML
	for _, field := range []struct {
		key, variable, value string
	}{
		{"LogLevel", "MYSQL_CONFIG_LOG_LEVEL", "warn"},
		{"Location", "MYSQL_CONFIG_LOCATION", "UTC"},
		{"Precision", "MYSQL_CONFIG_PRECISION", "millisecond"},
		{"Charset", "MYSQL_CONFIG_CHARSET", "utf8mb4"},
		{"Collation", "MYSQL_CONFIG_COLLATION", "utf8mb4_0900_bin"},
		{"Loc", "MYSQL_CONFIG_LOC", "UTC"},
		{"TransactionIsolation", "MYSQL_CONFIG_ISOLATION", "READ-COMMITTED"},
	} {
		t.Setenv(field.variable, field.value)
		content = strings.ReplaceAll(content, field.key+": "+field.value, field.key+": ${"+field.variable+"}")
	}
	if _, err := LoadConfig(writeTestConfig(t, "dev", content), "dev"); err != nil {
		t.Fatalf("non-credential field expansion failed: %v", err)
	}
}

// TestLoadConfigDoesNotFallBackToAnotherEnvironment 确认文件缺失、未知环境和路径穿越均失败。
// 即使目录中有有效 dev.yml，也不得拿它代替其他环境。
func TestLoadConfigDoesNotFallBackToAnotherEnvironment(t *testing.T) {
	t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", "synthetic-password")
	dir := writeTestConfig(t, "dev", testConfigYAML)
	for _, environment := range []string{"test", "stage", "../dev"} {
		if _, err := LoadConfig(dir, environment); err == nil {
			t.Errorf("environment %s must fail instead of using dev.yml", environment)
		}
	}
}

// TestLoadConfigResolvesRelativeCAPathAndRequiresTLSOutsideDev 核对环境策略和路径处理顺序。
// CA 的内容由客户端读取；此处只验证相对路径及空路径不会被错误变成目录。
func TestLoadConfigResolvesRelativeCAPathAndRequiresTLSOutsideDev(t *testing.T) {
	t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", "synthetic-password")
	tlsYAML := strings.Replace(testConfigYAML, "            Enabled: false", "            Enabled: true", 1)
	tlsYAML = strings.Replace(tlsYAML, "${MYSQL_CONFIG_UNUSED_CA}", "certs/ca.pem", 1)
	tlsYAML = strings.Replace(tlsYAML, "            ServerName: \"\"", "            ServerName: db.example.invalid", 1)
	tlsYAML = strings.Replace(tlsYAML, "            MinVersion: \"\"", "            MinVersion: \"1.2\"", 1)
	dir := writeTestConfig(t, "test", tlsYAML)
	config, err := LoadConfig(dir, "test")
	if err != nil {
		t.Fatalf("valid TLS configuration failed: %v", err)
	}
	if config.Connections["user"].Write.TLS.CAFile != filepath.Join(dir, "certs", "ca.pem") {
		t.Error("relative CA path must resolve from the YAML directory")
	}
	withoutTLS := writeTestConfig(t, "test", testConfigYAML)
	if _, err := LoadConfig(withoutTLS, "test"); err == nil || !strings.Contains(err.Error(), ".Write.TLS.Enabled") {
		t.Fatal("test environment must reject plaintext connections")
	}
	emptyCA := strings.Replace(tlsYAML, "certs/ca.pem", "", 1)
	if _, err := LoadConfig(writeTestConfig(t, "test", emptyCA), "test"); err == nil || !strings.Contains(err.Error(), ".Write.TLS.CAFile") {
		t.Fatal("empty CA path must not turn into the configuration directory")
	}
}

// TestLoadRepositoryConfigs 用合成环境变量加载仓库的三个环境文件，验证真实锚点结构。
// 只检查命名连接可用，不打印任何真实或替换后的凭据，也不建立网络连接。
func TestLoadRepositoryConfigs(t *testing.T) {
	for _, environment := range []string{"dev", "test", "prod"} {
		t.Run(environment, func(t *testing.T) {
			prefix := strings.ToUpper(environment) + "_MYSQL_"
			t.Setenv(prefix+"HOST", "db.example.invalid")
			t.Setenv(prefix+"CA_FILE", "certs/ca.pem")
			for _, name := range []string{"USER", "POST", "ORDER"} {
				t.Setenv(prefix+name+"_USERNAME", "synthetic_user")
				t.Setenv(prefix+name+"_PASSWORD", "synthetic-password")
			}
			config, err := LoadConfig(filepath.Join("..", "..", "..", "config", "database", "mysql"), environment)
			if err != nil {
				t.Fatalf("repository configuration failed to load: %v", err)
			}
			for _, name := range []string{"user", "post", "order"} {
				if !config.Connections[name].Enabled {
					t.Errorf("expected named connection %s", name)
				}
			}
		})
	}
}

// validTestConfig 复用有效合成配置，并改用任意业务名称，避免加载器写死三个现有连接。
func validTestConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv("MYSQL_CONFIG_TEST_PASSWORD", "synthetic-password")
	config, err := LoadConfig(writeTestConfig(t, "dev", testConfigYAML), "dev")
	if err != nil {
		t.Fatal(err)
	}
	return Config{Connections: map[string]ConnectionConfig{"analytics": config.Connections["user"]}}
}

// TestValidateRejectsUnsupportedBehaviorsAndUnsafeSettings 覆盖功能拒绝、连接预算和安全基线。
// 每个场景独立修改一处配置，保证失败源于该边界，而非依赖外部数据库状态。
func TestValidateRejectsUnsupportedBehaviorsAndUnsafeSettings(t *testing.T) {
	cases := map[string]func(*ConnectionConfig){
		"read splitting":      func(c *ConnectionConfig) { c.IsOpenReadDb = true },
		"record suppression":  func(c *ConnectionConfig) { c.Behavior.SuppressRecordNotFound = true },
		"automatic migration": func(c *ConnectionConfig) { c.Behavior.AutoMigrate = true },
		"foreign keys":        func(c *ConnectionConfig) { c.Behavior.DisableForeignKeyConstraintWhenMigrating = false },
		"time precision":      func(c *ConnectionConfig) { c.Behavior.NowFunc.Precision = "second" },
		"pool limits":         func(c *ConnectionConfig) { c.Write.SetMaxIdleConns = c.Write.SetMaxOpenConns + 1 },
		"negative timeout":    func(c *ConnectionConfig) { c.Write.ConnectTimeout = -1 },
		"zero timeout":        func(c *ConnectionConfig) { c.Write.ReadTimeout = 0 },
		"duration overflow": func(c *ConnectionConfig) {
			c.Write.SetConnMaxLifetime = int(int64(1<<63-1)/int64(time.Second) + 1)
		},
		"session timezone": func(c *ConnectionConfig) { c.Write.Session.TimeZone = "SYSTEM" },
		"SQL mode":         func(c *ConnectionConfig) { c.Write.Session.SQLMode = "STRICT_TRANS_TABLES" },
		"duplicate SQL mode": func(c *ConnectionConfig) {
			c.Write.Session.SQLMode += ",STRICT_TRANS_TABLES"
		},
		"multi statements": func(c *ConnectionConfig) { c.Write.MultiStatements = true },
		"remote plaintext": func(c *ConnectionConfig) { c.Write.Host = "db.example.invalid" },
		"skip verification": func(c *ConnectionConfig) {
			c.Write.TLS = TLSConfig{Enabled: true, CAFile: "ca.pem", ServerName: "db.example.invalid", MinVersion: "1.2", InsecureSkipVerify: true}
		},
		"plaintext fallback": func(c *ConnectionConfig) {
			c.Write.TLS = TLSConfig{Enabled: true, CAFile: "ca.pem", ServerName: "db.example.invalid", MinVersion: "1.2", AllowFallbackToPlaintext: true}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := validTestConfig(t)
			connection := config.Connections["analytics"]
			mutate(&connection)
			config.Connections["analytics"] = connection
			if err := config.Validate(); err == nil {
				t.Fatal("unsupported or unsafe configuration was accepted")
			}
		})
	}
	config := validTestConfig(t)
	if err := config.Validate(); err != nil {
		t.Fatalf("a valid arbitrary connection name must be supported: %v", err)
	}
	config.Connections["analytics"] = ConnectionConfig{Enabled: false}
	if err := config.Validate(); err == nil {
		t.Fatal("configuration with no enabled connection must be rejected")
	}
}
