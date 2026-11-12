package mysqltest

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appmysql "go-service-template/internal/database/mysql"
)

func TestWriteCertificates(t *testing.T) {
	// 同轮 CA 应信任匹配名称的服务端证书，私钥必须匹配，且不落盘 CA 私钥。
	dir := t.TempDir()
	if err := writeCertificates(dir); err != nil {
		t.Fatalf("writeCertificates() error = %v", err)
	}
	ca := readTestCertificate(t, filepath.Join(dir, "ca.pem"))
	server := readTestCertificate(t, filepath.Join(dir, "server-cert.pem"))
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := server.Verify(x509.VerifyOptions{Roots: roots, DNSName: "mysql.testkit.local"}); err != nil {
		t.Fatalf("verify generated server certificate: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, "server-cert.pem"), filepath.Join(dir, "server-key.pem")); err != nil {
		t.Errorf("server certificate and key do not match: %v", err)
	}
	if ca.SerialNumber.Sign() <= 0 || server.SerialNumber.Sign() <= 0 || ca.SerialNumber.Cmp(server.SerialNumber) == 0 {
		t.Error("CA and server certificates must have distinct positive serial numbers")
	}
	if !ca.IsCA || server.IsCA {
		t.Error("only the CA certificate may sign certificates")
	}
	info, err := os.Stat(filepath.Join(dir, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("server key permissions = %o, want 600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("generated file count = %d, want only CA certificate, server certificate and server key", len(entries))
	}

	// 即使证书由可信 CA 签发，连接使用不匹配的名称也必须被拒绝。
	if _, err := server.Verify(x509.VerifyOptions{Roots: roots, DNSName: "unexpected.testkit.local"}); err == nil {
		t.Error("server certificate unexpectedly accepted a different hostname")
	}

	// 第二轮生成的 CA 不能信任第一轮实例，防止测试环境共用固定私钥和信任根。
	otherDir := t.TempDir()
	if err := writeCertificates(otherDir); err != nil {
		t.Fatalf("writeCertificates() for another environment error = %v", err)
	}
	otherRoots := x509.NewCertPool()
	otherRoots.AddCert(readTestCertificate(t, filepath.Join(otherDir, "ca.pem")))
	if _, err := server.Verify(x509.VerifyOptions{Roots: otherRoots, DNSName: "mysql.testkit.local"}); err == nil {
		t.Error("server certificate unexpectedly trusted a different environment CA")
	}
}

func TestWriteAppConfig(t *testing.T) {
	// 连接集合完全由发现结果决定；使用不同数量和与目录名不同的库名，防止固定清单或命名猜测。
	cases := []struct {
		name      string
		databases map[string]string
	}{
		{name: "single domain", databases: map[string]string{"catalog": "product_inventory"}},
		{name: "expanded domains", databases: map[string]string{
			"catalog": "product_inventory", "audit": "event_archive", "billing": "invoices",
			"delivery": "parcel_tracking", "search": "index_catalog",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			password := "test: # quoted\ncredential"
			configRoot, err := writeAppConfig(dir, "127.0.0.1", 49173, password, tc.databases)
			if err != nil {
				t.Fatalf("writeAppConfig() error = %v", err)
			}
			if configRoot != filepath.Join(dir, "config") {
				t.Errorf("config root = %q, want %q", configRoot, filepath.Join(dir, "config"))
			}
			configDir := filepath.Join(configRoot, "database", "mysql")
			config, err := appmysql.LoadConfig(configDir, "test")
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if len(config.Connections) != len(tc.databases) {
				t.Fatalf("connection count = %d, want %d", len(config.Connections), len(tc.databases))
			}
			for name, database := range tc.databases {
				connection, ok := config.Connections[name]
				if !ok || !connection.Enabled || connection.Write.DataBase != database {
					t.Errorf("expected enabled connection %q to database %q", name, database)
					continue
				}
				endpoint := connection.Write
				if endpoint.Host != "127.0.0.1" || endpoint.Port != 49173 || endpoint.User != "test_app" || endpoint.Pass != password {
					t.Errorf("connection %q did not preserve the supplied endpoint or credential", name)
				}
				if !endpoint.TLS.Enabled || endpoint.TLS.InsecureSkipVerify || endpoint.TLS.AllowFallbackToPlaintext || endpoint.TLS.MinVersion != "1.2" {
					t.Errorf("connection %q must require TLS 1.2 or newer with certificate verification", name)
				}
				if endpoint.TLS.CAFile != filepath.Join(dir, "ca.pem") || endpoint.TLS.ServerName != "mysql.testkit.local" {
					t.Errorf("connection %q has incorrect certificate identity settings", name)
				}
				if endpoint.MultiStatements || !endpoint.ParseTime || endpoint.Loc != "UTC" || endpoint.Session.TimeZone != "+00:00" || endpoint.Session.TransactionIsolation != "READ-COMMITTED" {
					t.Errorf("connection %q does not retain the production time and transaction baseline", name)
				}
				if connection.Behavior.AutoMigrate || !connection.Behavior.DisableForeignKeyConstraintWhenMigrating {
					t.Errorf("connection %q must not automatically create schema", name)
				}
			}
			info, err := os.Stat(filepath.Join(configDir, "test.yml"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("configuration permissions = %o, want 600", info.Mode().Perm())
			}
		})
	}
}

func TestWriteAppConfigRejectsInvalidConfiguration(t *testing.T) {
	const secret = "must-not-appear-in-errors"
	cases := []struct {
		name      string
		host      string
		port      int
		databases map[string]string
	}{
		// 缺少宿主地址时必须在生成文件前拒绝，避免产生会回退到默认地址的配置。
		{name: "missing host", host: "", port: 49173, databases: map[string]string{"catalog": "inventory"}},
		// 未分配映射端口时必须拒绝，不能把容器内部端口或零值静默作为目标。
		{name: "unallocated port", host: "127.0.0.1", port: 0, databases: map[string]string{"catalog": "inventory"}},
		// 扫描或初始化结果为空时不能回退到旧的业务库清单，也不能生成没有连接的可用配置。
		{name: "no databases", host: "127.0.0.1", port: 49173},
		// 业务连接必须明确指向初始化后的库，不能借用管理连接允许不指定库的规则。
		{name: "missing database name", host: "127.0.0.1", port: 49173, databases: map[string]string{"catalog": ""}},
		// 扫描结果仍须通过项目实际配置加载器的连接名、库名约束。
		{name: "invalid connection name", host: "127.0.0.1", port: 49173, databases: map[string]string{"Catalog": "inventory"}},
		{name: "invalid database name", host: "127.0.0.1", port: 49173, databases: map[string]string{"catalog": "Invalid-Database"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			configRoot, err := writeAppConfig(dir, tc.host, tc.port, secret, tc.databases)
			if err == nil || configRoot != "" {
				t.Fatal("invalid endpoint must not produce a usable config root")
			}
			if strings.Contains(err.Error(), secret) {
				t.Error("configuration failure exposed the credential")
			}
			if _, err := os.Stat(filepath.Join(dir, "config")); !os.IsNotExist(err) {
				t.Error("invalid configuration should fail before creating files")
			}
		})
	}
}

func readTestCertificate(t *testing.T, file string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("expected one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
