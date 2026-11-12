package mysqltest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	appmysql "go-service-template/internal/database/mysql"

	"gopkg.in/yaml.v3"
)

const (
	serverName      = "mysql.testkit.local"
	appUser         = "test_app"
	baselineSQLMode = "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION"
)

// writeCertificates 为本轮实例签发独立证书；CA 私钥只保存在内存，不写入磁盘或交给容器。
// 调用方持有临时目录，并负责初始化失败时删除已经写出的文件。
func writeCertificates(dir string) error {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate test CA key: %w", err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate test server key: %w", err)
	}
	caSerial, err := certificateSerial()
	if err != nil {
		return err
	}
	serverSerial, err := certificateSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "MySQL testkit temporary CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create test CA certificate: %w", err)
	}
	server := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: serverName},
		DNSNames:     []string{serverName},
		NotBefore:    ca.NotBefore,
		NotAfter:     ca.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create test server certificate: %w", err)
	}
	files := []struct {
		name  string
		block pem.Block
		mode  os.FileMode
	}{
		{"ca.pem", pem.Block{Type: "CERTIFICATE", Bytes: caDER}, 0o644},
		{"server-cert.pem", pem.Block{Type: "CERTIFICATE", Bytes: serverDER}, 0o644},
		{"server-key.pem", pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)}, 0o600},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.name), pem.EncodeToMemory(&file.block), file.mode); err != nil {
			return fmt.Errorf("write test certificate file %s: %w", file.name, err)
		}
	}
	return nil
}

// writeAppConfig 根据本轮实际创建的数据库生成命名连接，不维护业务域或库名清单。
// 这里只生成数据库配置，其他应用组件的配置由后续应用测试设施补齐。
func writeAppConfig(rootDir, host string, port int, password string, databases map[string]string) (string, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve test environment directory: %w", err)
	}
	config := appmysql.Config{Connections: make(map[string]appmysql.ConnectionConfig, len(databases))}
	for name, database := range databases {
		if database == "" {
			return "", fmt.Errorf("generated application connection %q requires a database", name)
		}
		config.Connections[name] = testConnection(rootDir, host, port, appUser, password, database)
	}
	if err := config.Validate(); err != nil {
		return "", fmt.Errorf("validate generated test MySQL configuration: %w", err)
	}
	var document struct {
		Gormv2 struct {
			Mysql appmysql.Config `yaml:"Mysql"`
		} `yaml:"Gormv2"`
	}
	document.Gormv2.Mysql = config
	data, err := yaml.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode test MySQL configuration: %w", err)
	}
	configRoot := filepath.Join(rootDir, "config")
	configDir := filepath.Join(configRoot, "database", "mysql")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("create test MySQL configuration directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "test.yml"), data, 0o600); err != nil {
		return "", fmt.Errorf("write test MySQL configuration: %w", err)
	}
	return configRoot, nil
}

// testConnection 统一管理连接和应用连接使用的 TLS、会话与连接池设置。
// 管理连接可以不指定默认数据库；应用连接必须由 writeAppConfig 提供实际库名。
func testConnection(rootDir, host string, port int, user, password, database string) appmysql.ConnectionConfig {
	return appmysql.ConnectionConfig{
		Enabled:       true,
		SlowThreshold: 1,
		LogLevel:      "silent",
		Behavior: appmysql.BehaviorConfig{
			SingularTable:                            true,
			DisableForeignKeyConstraintWhenMigrating: true,
			NowFunc:                                  appmysql.NowFuncConfig{Location: "UTC", Precision: "millisecond"},
		},
		Write: appmysql.EndpointConfig{
			Host: host, Port: port, DataBase: database, User: user, Pass: password,
			Charset: "utf8mb4", Collation: "utf8mb4_0900_bin", ParseTime: true, Loc: "UTC",
			Session: appmysql.SessionConfig{
				TimeZone: "+00:00", TransactionIsolation: "READ-COMMITTED", SQLMode: baselineSQLMode, Autocommit: 1,
			},
			TLS: appmysql.TLSConfig{
				Enabled: true, CAFile: filepath.Join(rootDir, "ca.pem"), ServerName: serverName, MinVersion: "1.2",
			},
			SetMaxOpenConns: 5, SetMaxIdleConns: 1,
			SetConnMaxLifetime: 60, SetConnMaxIdleTime: 30,
			ConnectTimeout: 5, ReadTimeout: 5, WriteTimeout: 5,
		},
	}
}

// mysqlConfig 在初始化数据目录前设置实例基线，尤其是不能运行时修改的表名大小写规则。
func mysqlConfig() []byte {
	return []byte("[mysqld]\n" +
		"default-storage-engine=InnoDB\n" +
		"character-set-server=utf8mb4\n" +
		"collation-server=utf8mb4_0900_bin\n" +
		"default-time-zone=+00:00\n" +
		"lower-case-table-names=1\n" +
		"transaction-isolation=READ-COMMITTED\n" +
		"autocommit=1\n" +
		"sql-mode=" + baselineSQLMode + "\n" +
		"innodb-flush-log-at-trx-commit=1\n" +
		"sync-binlog=1\n" +
		"log-bin=mysql-bin\n" +
		"binlog-format=ROW\n" +
		"require-secure-transport=ON\n" +
		"ssl-ca=/etc/mysql/testkit/ca.pem\n" +
		"ssl-cert=/etc/mysql/testkit/server-cert.pem\n" +
		"ssl-key=/etc/mysql/testkit/server-key.pem\n")
}

// certificateSerial 产生正的随机序列号，不复用不同证书或不同运行的固定编号。
func certificateSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	limit.Sub(limit, big.NewInt(1))
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate test certificate serial: %w", err)
	}
	return serial.Add(serial, big.NewInt(1)), nil
}
