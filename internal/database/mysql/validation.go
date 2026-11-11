package mysql

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

// identifierPattern 对齐项目业务连接名、数据库名的小写 snake_case 约定。
// 失败时仅报告字段，不回显不可信名称或其他配置值。
var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate 只检查已解码并展开的配置，不读取文件、环境变量或证书，也不改动配置。
// 所有连接统一在这里校验；Open 也调用它，保护手工构造 Config 的入口。
// 禁用连接跳过运行参数检查，至少需要一个启用项，且没有隐式默认连接。
func (c Config) Validate() error {
	enabled := 0
	for _, name := range sortedConnectionNames(c.Connections) {
		if !identifierPattern.MatchString(name) {
			return fmt.Errorf("MySQL connection names must use lowercase snake_case")
		}
		connection := c.Connections[name]
		if !connection.Enabled {
			continue
		}
		enabled++
		path := "Connections." + name
		if connection.IsOpenReadDb {
			return invalidField(path+".IsOpenReadDb", "read/write splitting is not supported")
		}
		if err := validateSeconds(path+".SlowThreshold", connection.SlowThreshold); err != nil {
			return err
		}
		switch connection.LogLevel {
		case "silent", "error", "warn", "info":
		default:
			return invalidField(path+".LogLevel", "expected silent, error, warn or info")
		}
		behavior := connection.Behavior
		if behavior.SuppressRecordNotFound {
			return invalidField(path+".Behavior.SuppressRecordNotFound", "record-not-found suppression is not supported")
		}
		if behavior.AutoMigrate {
			return invalidField(path+".Behavior.AutoMigrate", "automatic schema migration is not supported; use reviewed SQL migrations")
		}
		if !behavior.SingularTable || !behavior.DisableForeignKeyConstraintWhenMigrating {
			return invalidField(path+".Behavior", "singular table names and disabled foreign-key migration are required")
		}
		if behavior.NowFunc.Location != "UTC" || behavior.NowFunc.Precision != "millisecond" {
			return invalidField(path+".Behavior.NowFunc", "only UTC with millisecond precision is supported")
		}
		if err := connection.Write.validate(path+".Write", c.environment); err != nil {
			return err
		}
	}
	if enabled == 0 {
		return fmt.Errorf("MySQL configuration must enable at least one connection")
	}
	return nil
}

// validate 保留项目明确要求的端点约束，地址和证书的具体可用性由驱动、TLS 校验。
// 各组选项按实际生效位置检查，错误只含字段名和约束，不包含账号、密码或连接地址。
func (e EndpointConfig) validate(path, environment string) error {
	if strings.TrimSpace(e.Host) == "" || strings.TrimSpace(e.User) == "" || e.Pass == "" {
		return invalidField(path, "Host, User and Pass are required")
	}
	// 允许不选择默认数据库，供同一实例上的通用跨库连接使用。
	// 留空时每条业务 SQL 都须指定完整库表名；有值时仍严格校验，不把空白当作空配置。
	if e.DataBase != "" && !identifierPattern.MatchString(e.DataBase) {
		return invalidField(path+".DataBase", "must be empty or use lowercase snake_case")
	}
	if e.Port < 1 || e.Port > 65535 {
		return invalidField(path+".Port", "must be between 1 and 65535")
	}
	if e.Charset != "utf8mb4" || e.Collation != "utf8mb4_0900_bin" {
		return invalidField(path+".Charset/Collation", "must use the project utf8mb4 baseline")
	}
	if !e.ParseTime || e.Loc != "UTC" || e.MultiStatements {
		return invalidField(path, "UTC time parsing is required and multiple statements must be disabled")
	}
	if e.Session.TimeZone != "+00:00" || e.Session.TransactionIsolation != "READ-COMMITTED" || e.Session.Autocommit != 1 {
		return invalidField(path+".Session", "must use UTC, READ-COMMITTED and autocommit=1")
	}
	if !validSQLMode(e.Session.SQLMode) {
		return invalidField(path+".Session.SQLMode", "must contain exactly the six project baseline modes")
	}
	if e.SetMaxOpenConns <= 0 || e.SetMaxIdleConns < 0 || e.SetMaxIdleConns > e.SetMaxOpenConns {
		return invalidField(path, "pool limits must satisfy 0 <= idle <= open and open > 0")
	}
	// 固定字段顺序，多个时长同时非法时，优先报告的字段保持可预测。
	for _, setting := range []struct {
		name    string
		seconds int
	}{
		{"SetConnMaxLifetime", e.SetConnMaxLifetime},
		{"SetConnMaxIdleTime", e.SetConnMaxIdleTime},
		{"ConnectTimeout", e.ConnectTimeout},
		{"ReadTimeout", e.ReadTimeout},
		{"WriteTimeout", e.WriteTimeout},
	} {
		if err := validateSeconds(path+"."+setting.name, setting.seconds); err != nil {
			return err
		}
	}
	if !e.TLS.Enabled {
		if environment != "" && environment != "dev" {
			return invalidField(path+".TLS.Enabled", "TLS is required outside the development environment")
		}
		ip := net.ParseIP(e.Host)
		loopback := e.Host == "localhost" || (ip != nil && ip.IsLoopback())
		dockerHost := environment == "dev" && e.Host == "host.docker.internal"
		if !loopback && !dockerHost {
			return invalidField(path+".TLS.Enabled", "TLS may only be disabled for a loopback address or host.docker.internal in the development environment")
		}
		return nil // 关闭 TLS 后不读取和校验未使用的 CA、主机名等参数。
	}
	if strings.TrimSpace(e.TLS.CAFile) == "" {
		return invalidField(path+".TLS.CAFile", "is required when TLS is enabled")
	}
	if strings.TrimSpace(e.TLS.ServerName) == "" {
		return invalidField(path+".TLS.ServerName", "is required when TLS is enabled")
	}
	if (e.TLS.MinVersion != "1.2" && e.TLS.MinVersion != "1.3") || e.TLS.InsecureSkipVerify || e.TLS.AllowFallbackToPlaintext {
		return invalidField(path+".TLS", "TLS 1.2 or newer with certificate verification and no plaintext fallback is required")
	}
	return nil
}

// validateSeconds 在整数乘 time.Second 前检查上下界，避免 time.Duration 溢出。
// 当前所有时长均要求正数，防止意外禁用网络超时或连接回收。
func validateSeconds(path string, seconds int) error {
	const maxSeconds = int64(1<<63-1) / int64(time.Second)
	if seconds <= 0 || int64(seconds) > maxSeconds {
		return invalidField(path, "must be a positive number of seconds within the time.Duration range")
	}
	return nil
}

// validSQLMode 将顺序、空格和大小写规范化后比较完整集合，同时拒绝重复或额外模式。
// 只核对会话模式声明，不通过修改实例全局参数实现这一要求。
func validSQLMode(value string) bool {
	modes := strings.Split(value, ",")
	for i := range modes {
		modes[i] = strings.ToUpper(strings.TrimSpace(modes[i]))
	}
	sort.Strings(modes)
	return strings.Join(modes, ",") == "ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION,NO_ZERO_DATE,NO_ZERO_IN_DATE,ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES"
}

// invalidField 统一错误格式，调用方只传字段路径和固定约束，不传实际配置值。
func invalidField(path, reason string) error {
	return fmt.Errorf("invalid MySQL configuration field %s: %s", path, reason)
}

// sortedConnectionNames 保证加载和校验顺序固定，多个错误存在时先报告相同连接。
func sortedConnectionNames(connections map[string]ConnectionConfig) []string {
	names := make([]string, 0, len(connections))
	for name := range connections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
