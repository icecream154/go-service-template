package mysql_test

import (
	"strconv"
	"strings"
	"testing"

	"go-service-template/internal/database/mysql"
)

const mysqlTestSQLModes = "ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION,NO_ZERO_DATE,NO_ZERO_IN_DATE,ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES"

type mysqlValidateConfigCase struct {
	name        string
	connections map[string]mysql.ConnectionConfig
	want        string
}

func TestConfigValidate(t *testing.T) {
	primary := mysqlValidationConnection()
	disabled := mysql.ConnectionConfig{IsOpenReadDb: true}
	badLevel := primary
	badLevel.LogLevel = "debug"
	badPort := primary
	badPort.Write.Port = 0
	tests := []mysqlValidateConfigCase{
		// 完整且启用的连接满足所有约束，应通过校验。
		{name: "valid enabled connection", connections: map[string]mysql.ConnectionConfig{"primary": primary}},
		// 空配置没有可用的数据库连接，验证不会创建隐式默认连接。
		{name: "no connections", want: "must enable at least one connection"},
		// 连接存在但全部禁用时，仍应报告没有启用的连接。
		{name: "only disabled connections", connections: map[string]mysql.ConnectionConfig{"archive": disabled}, want: "must enable at least one connection"},
		// 禁用项保留不支持的读写分离配置，验证运行参数只校验启用项。
		{name: "disabled connection settings skipped", connections: map[string]mysql.ConnectionConfig{"archive": disabled, "primary": primary}},
		// 即使连接被禁用，配置键也要遵守小写 snake_case。
		{name: "invalid disabled connection name", connections: map[string]mysql.ConnectionConfig{"ARCHIVE": disabled, "primary": primary}, want: "connection names must use lowercase snake_case"},
		// 两个启用项各有不同错误，验证稳定报告按连接名排序后的首个错误。
		{name: "first error in connection name order", connections: map[string]mysql.ConnectionConfig{"zeta": badPort, "alpha": badLevel}, want: "Connections.alpha.LogLevel"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := mysql.Config{Connections: test.connections}
			assertMySQLValidationError(t, config.Validate(), test.want)
		})
	}
}

type mysqlValidateConnectionCase struct {
	name   string
	change func(*mysql.ConnectionConfig)
	want   string
}

func TestConfigValidateConnectionOptions(t *testing.T) {
	tests := []mysqlValidateConnectionCase{
		// 现阶段只支持主库读写，启用读写分离应定位到对应字段。
		{name: "read write splitting", change: func(c *mysql.ConnectionConfig) { c.IsOpenReadDb = true }, want: "Connections.primary.IsOpenReadDb"},
		// 慢查询日志阈值必须为正，零不能被解释成关闭。
		{name: "zero slow threshold", change: func(c *mysql.ConnectionConfig) { c.SlowThreshold = 0 }, want: "Connections.primary.SlowThreshold"},
		// debug 不是 GORM 连接日志允许级别，应定位到 LogLevel。
		{name: "unsupported log level", change: func(c *mysql.ConnectionConfig) { c.LogLevel = "debug" }, want: "Connections.primary.LogLevel"},
		// 开启记录未找到抑制会改变仓储错误约定，当前应拒绝。
		{name: "record not found suppression", change: func(c *mysql.ConnectionConfig) { c.Behavior.SuppressRecordNotFound = true }, want: "Connections.primary.Behavior.SuppressRecordNotFound"},
		// 项目通过受控 SQL 维护表结构，启动时的自动迁移开关应被拒绝。
		{name: "automatic migration", change: func(c *mysql.ConnectionConfig) { c.Behavior.AutoMigrate = true }, want: "Connections.primary.Behavior.AutoMigrate"},
		// 关闭单数表名时违反表命名约定，应报告 Behavior。
		{name: "plural table names", change: func(c *mysql.ConnectionConfig) { c.Behavior.SingularTable = false }, want: "Connections.primary.Behavior: singular table names"},
		// 允许自动迁移外键也违反既定配置约束，单独验证该开关。
		{name: "foreign key migration", change: func(c *mysql.ConnectionConfig) { c.Behavior.DisableForeignKeyConstraintWhenMigrating = false }, want: "Connections.primary.Behavior: singular table names"},
		// 使用不同的时区但仍保留毫秒精度，验证时区约束独立生效。
		{name: "non UTC clock", change: func(c *mysql.ConnectionConfig) { c.Behavior.NowFunc.Location = "Local" }, want: "Connections.primary.Behavior.NowFunc"},
		// 使用 UTC 但降低精度，验证只能使用项目约定的毫秒精度。
		{name: "non millisecond clock", change: func(c *mysql.ConnectionConfig) { c.Behavior.NowFunc.Precision = "second" }, want: "Connections.primary.Behavior.NowFunc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := mysqlValidationConnection()
			test.change(&connection)
			config := mysql.Config{Connections: map[string]mysql.ConnectionConfig{"primary": connection}}
			assertMySQLValidationError(t, config.Validate(), test.want)
		})
	}
}

type mysqlValidateEndpointCase struct {
	name   string
	change func(*mysql.EndpointConfig)
	want   string
}

func TestConfigValidateEndpointOptions(t *testing.T) {
	tests := []mysqlValidateEndpointCase{
		// 主机只含空白时没有可用地址，应报告缺少连接参数。
		{name: "blank host", change: func(e *mysql.EndpointConfig) { e.Host = " \t" }, want: "Write: Host, User and Pass are required"},
		// 用户名缺失时应拒绝建立连接，即使主机和密码都有值。
		{name: "blank user", change: func(e *mysql.EndpointConfig) { e.User = " " }, want: "Write: Host, User and Pass are required"},
		// 空密码不符合连接凭据约定，应报告连接参数缺失。
		{name: "empty password", change: func(e *mysql.EndpointConfig) { e.Pass = "" }, want: "Write: Host, User and Pass are required"},
		// 跨库连接可用完整库表名，因此允许不声明默认数据库。
		{name: "no default database", change: func(e *mysql.EndpointConfig) { e.DataBase = "" }},
		// 已声明的数据库名仍须符合小写 snake_case。
		{name: "noncanonical database name", change: func(e *mysql.EndpointConfig) { e.DataBase = "App-DB" }, want: "Write.DataBase"},
		// 端口 1 是允许的下界，防止误用开区间比较。
		{name: "minimum port", change: func(e *mysql.EndpointConfig) { e.Port = 1 }},
		// 端口 65535 是允许的上界，防止误拒绝最后一个 TCP 端口。
		{name: "maximum port", change: func(e *mysql.EndpointConfig) { e.Port = 65535 }},
		// 端口 0 在下界之前，不能被当作自动选择端口。
		{name: "port below minimum", change: func(e *mysql.EndpointConfig) { e.Port = 0 }, want: "Write.Port"},
		// 端口 65536 紧邻上界，验证超出 TCP 端口范围会报错。
		{name: "port above maximum", change: func(e *mysql.EndpointConfig) { e.Port = 65536 }, want: "Write.Port"},
		// 旧的 utf8 字符集不能替代 utf8mb4，即使排序规则保持正确。
		{name: "different charset", change: func(e *mysql.EndpointConfig) { e.Charset = "utf8" }, want: "Write.Charset/Collation"},
		// 相同字符集下的其他排序规则会改变比较语义，应拒绝。
		{name: "different collation", change: func(e *mysql.EndpointConfig) { e.Collation = "utf8mb4_general_ci" }, want: "Write.Charset/Collation"},
		// 关闭驱动时间解析会改变 DATE、DATETIME 的值类型，应拒绝。
		{name: "disabled time parsing", change: func(e *mysql.EndpointConfig) { e.ParseTime = false }, want: "Write: UTC time parsing is required"},
		// 保留时间解析但改变时区，验证驱动的 UTC 要求。
		{name: "non UTC parsing", change: func(e *mysql.EndpointConfig) { e.Loc = "Local" }, want: "Write: UTC time parsing is required"},
		// 打开多语句选项会违反驱动配置约定，即使时间设置正确也应拒绝。
		{name: "multiple statements", change: func(e *mysql.EndpointConfig) { e.MultiStatements = true }, want: "Write: UTC time parsing is required"},
		// 会话时区与驱动解析时区分开设置，验证会话也必须使用 UTC。
		{name: "non UTC session", change: func(e *mysql.EndpointConfig) { e.Session.TimeZone = "+08:00" }, want: "Write.Session:"},
		// 变更事务隔离级别会影响读取语义，应拒绝。
		{name: "different isolation", change: func(e *mysql.EndpointConfig) { e.Session.TransactionIsolation = "REPEATABLE-READ" }, want: "Write.Session:"},
		// 手动关闭自动提交不符合普通会话的默认语义，应拒绝。
		{name: "disabled autocommit", change: func(e *mysql.EndpointConfig) { e.Session.Autocommit = 0 }, want: "Write.Session:"},
		// 打乱六种模式的顺序并使用小写和空格，验证按规范化后的集合比较。
		{name: "normalized SQL modes", change: func(e *mysql.EndpointConfig) {
			e.Session.SQLMode = " strict_trans_tables , only_full_group_by , no_zero_in_date , no_zero_date , no_engine_substitution , error_for_division_by_zero "
		}},
		// 删除最后一个模式，验证缺少必需项的配置会失败。
		{name: "missing SQL mode", change: func(e *mysql.EndpointConfig) {
			e.Session.SQLMode = strings.TrimSuffix(mysqlTestSQLModes, ",STRICT_TRANS_TABLES")
		}, want: "Write.Session.SQLMode"},
		// 六种模式都存在但有重复项，验证不会按去重后的集合放行。
		{name: "duplicate SQL mode", change: func(e *mysql.EndpointConfig) {
			e.Session.SQLMode = mysqlTestSQLModes + ",STRICT_TRANS_TABLES"
		}, want: "Write.Session.SQLMode"},
		// 在完整集合后追加额外模式，验证配置只接受项目基线。
		{name: "extra SQL mode", change: func(e *mysql.EndpointConfig) {
			e.Session.SQLMode = mysqlTestSQLModes + ",ANSI_QUOTES"
		}, want: "Write.Session.SQLMode"},
		// 最大连接数为零时，底层可能将其解释为不限连接数，配置校验应拒绝。
		{name: "unbounded pool", change: func(e *mysql.EndpointConfig) {
			e.SetMaxOpenConns = 0
			e.SetMaxIdleConns = 0
		}, want: "Write: pool limits"},
		// 空闲连接数为负数不合法，应报告连接池约束。
		{name: "negative idle limit", change: func(e *mysql.EndpointConfig) { e.SetMaxIdleConns = -1 }, want: "Write: pool limits"},
		// 空闲连接数可以为零，验证不强制保留空闲连接。
		{name: "zero idle limit", change: func(e *mysql.EndpointConfig) { e.SetMaxIdleConns = 0 }},
		// 空闲连接数与总连接数相等时仍符合闭区间上界。
		{name: "idle limit equals open limit", change: func(e *mysql.EndpointConfig) { e.SetMaxIdleConns = e.SetMaxOpenConns }},
		// 空闲连接数比总连接数多一项时应拒绝配置。
		{name: "idle limit above open limit", change: func(e *mysql.EndpointConfig) { e.SetMaxIdleConns = e.SetMaxOpenConns + 1 }, want: "Write: pool limits"},
		// 多个时长字段同时非法时，应按固定顺序先报告连接存活时间。
		{name: "first invalid duration", change: func(e *mysql.EndpointConfig) {
			e.SetConnMaxLifetime = 0
			e.SetConnMaxIdleTime = 0
		}, want: "Write.SetConnMaxLifetime"},
		// 只令空闲回收时长无效，验证它也接受正值校验。
		{name: "zero idle duration", change: func(e *mysql.EndpointConfig) { e.SetConnMaxIdleTime = 0 }, want: "Write.SetConnMaxIdleTime"},
		// 连接超时为负数会禁用底层超时，验证连接字段拒绝该值。
		{name: "negative connect timeout", change: func(e *mysql.EndpointConfig) { e.ConnectTimeout = -1 }, want: "Write.ConnectTimeout"},
		// 读取超时为零时应拒绝，避免将零解释为不限制。
		{name: "zero read timeout", change: func(e *mysql.EndpointConfig) { e.ReadTimeout = 0 }, want: "Write.ReadTimeout"},
		// 写入超时同样要求正数，验证最后一个时长字段也经过检查。
		{name: "zero write timeout", change: func(e *mysql.EndpointConfig) { e.WriteTimeout = 0 }, want: "Write.WriteTimeout"},
		// 关闭本机 TLS 后，即使保留空的 TLS 参数也不会再校验它们。
		{name: "localhost without TLS", change: func(e *mysql.EndpointConfig) {
			e.Host = "localhost"
			e.TLS = mysql.TLSConfig{}
		}},
		// IPv6 回环地址与 localhost 有相同的本机连接豁免。
		{name: "IPv6 loopback without TLS", change: func(e *mysql.EndpointConfig) {
			e.Host = "::1"
			e.TLS.Enabled = false
		}},
		// 手工构造的配置没有 dev 环境标记，Docker 地址应继续要求 TLS。
		{name: "Docker host without dev environment", change: func(e *mysql.EndpointConfig) {
			e.Host = "host.docker.internal"
			e.TLS.Enabled = false
		}, want: "Write.TLS.Enabled"},
		// 启用 TLS 但未指定信任文件时，应定位到 CAFile。
		{name: "missing CA file", change: func(e *mysql.EndpointConfig) { e.TLS.CAFile = " " }, want: "Write.TLS.CAFile"},
		// 启用 TLS 但未指定目标身份时，应定位到 ServerName。
		{name: "missing server name", change: func(e *mysql.EndpointConfig) { e.TLS.ServerName = " " }, want: "Write.TLS.ServerName"},
		// TLS 1.3 是允许的最低版本选项之一，应通过。
		{name: "TLS 1.3", change: func(e *mysql.EndpointConfig) { e.TLS.MinVersion = "1.3" }},
		// TLS 1.1 低于安全基线，即使证书选项有效也应拒绝。
		{name: "old TLS version", change: func(e *mysql.EndpointConfig) { e.TLS.MinVersion = "1.1" }, want: "Write.TLS: TLS 1.2 or newer"},
		// 启用加密但跳过身份验证时仍违反 TLS 约定，应拒绝。
		{name: "skipped certificate verification", change: func(e *mysql.EndpointConfig) { e.TLS.InsecureSkipVerify = true }, want: "Write.TLS: TLS 1.2 or newer"},
		// 明文回退会绕过必须加密的约定，应拒绝该开关。
		{name: "plaintext fallback", change: func(e *mysql.EndpointConfig) { e.TLS.AllowFallbackToPlaintext = true }, want: "Write.TLS: TLS 1.2 or newer"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := mysqlValidationConnection()
			test.change(&connection.Write)
			config := mysql.Config{Connections: map[string]mysql.ConnectionConfig{"primary": connection}}
			assertMySQLValidationError(t, config.Validate(), test.want)
			if config.Connections["primary"] != connection {
				t.Error("Validate() modified the input connection")
			}
		})
	}
}

type mysqlValidateDurationCase struct {
	name    string
	seconds int64
	want    string
}

func TestConfigValidateDurationBounds(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("duration overflow boundary is outside the platform int range")
	}
	tests := []mysqlValidateDurationCase{
		// 使用最小正秒数，验证允许配置一秒的读取超时。
		{name: "one second", seconds: 1},
		// 最大可用整秒数转为纳秒后仍在 time.Duration 范围内，应通过。
		{name: "largest whole duration in seconds", seconds: 9_223_372_036},
		// 最大整秒数再加一秒会溢出 time.Duration，应在配置校验阶段拒绝。
		{name: "overflowing duration", seconds: 9_223_372_037, want: "Connections.primary.Write.ReadTimeout"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := mysqlValidationConnection()
			connection.Write.ReadTimeout = int(test.seconds)
			config := mysql.Config{Connections: map[string]mysql.ConnectionConfig{"primary": connection}}
			assertMySQLValidationError(t, config.Validate(), test.want)
		})
	}
}

func mysqlValidationConnection() mysql.ConnectionConfig {
	return mysql.ConnectionConfig{
		Enabled:       true,
		SlowThreshold: 1,
		LogLevel:      "warn",
		Behavior: mysql.BehaviorConfig{
			SingularTable:                            true,
			DisableForeignKeyConstraintWhenMigrating: true,
			NowFunc: mysql.NowFuncConfig{
				Location:  "UTC",
				Precision: "millisecond",
			},
		},
		Write: mysql.EndpointConfig{
			Host:               "mysql.example.test",
			Port:               3306,
			DataBase:           "app_db",
			User:               "app_user",
			Pass:               "fixture-password",
			Charset:            "utf8mb4",
			Collation:          "utf8mb4_0900_bin",
			ParseTime:          true,
			Loc:                "UTC",
			SetMaxOpenConns:    10,
			SetMaxIdleConns:    2,
			SetConnMaxLifetime: 60,
			SetConnMaxIdleTime: 30,
			ConnectTimeout:     5,
			ReadTimeout:        5,
			WriteTimeout:       5,
			Session: mysql.SessionConfig{
				TimeZone:             "+00:00",
				TransactionIsolation: "READ-COMMITTED",
				SQLMode:              mysqlTestSQLModes,
				Autocommit:           1,
			},
			TLS: mysql.TLSConfig{
				Enabled:    true,
				CAFile:     "certificates/ca.pem",
				ServerName: "mysql.example.test",
				MinVersion: "1.2",
			},
		},
	}
}

func assertMySQLValidationError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Errorf("Validate() error = %v, want nil", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Validate() error = %v, want a message containing %q", err, want)
	}
}
