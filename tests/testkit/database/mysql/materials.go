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

// writeCertificates 为本轮 MySQL 实例生成自签名 CA，并用它签发服务端证书。
// dir 是调用方已创建的临时目录。成功时写出 ca.pem、server-cert.pem 和 server-key.pem。
// 客户端通过 ca.pem 建立信任，Create 将三个 PEM 文件复制到容器供 mysqld 加载。
// CA 私钥仅在内存中用于签发。任一步骤失败时返回错误，由调用方清理临时目录和已写出的文件。
func writeCertificates(dir string) error {
	// 生成 2048 位 RSA CA 密钥，用于给根证书和服务端证书签名。
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		// 后续签发依赖 CA 私钥，生成失败时标记出错阶段并返回。
		return fmt.Errorf("generate test CA key: %w", err)
	}

	// 为服务端单独生成 RSA 密钥，供 mysqld 在 TLS 握手时证明持有证书对应的私钥。
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		// 服务端密钥生成失败时终止签发，保留底层错误。
		return fmt.Errorf("generate test server key: %w", err)
	}

	// 为自签名根证书抽取一个随机序列号。
	caSerial, err := certificateSerial()
	if err != nil {
		// certificateSerial 已附上序列号生成阶段的错误信息。
		return err
	}

	// 为同一轮的服务端证书单独取样。
	serverSerial, err := certificateSerial()
	if err != nil {
		// 服务端证书同样依赖随机序列号，取样失败时终止签发。
		return err
	}

	// 两张证书共用一个时间基准。有效期从基准前 1 分钟到基准后 24 小时。
	// 提前起算可容纳测试进程与容器间的轻微时钟偏差。
	now := time.Now()

	// 声明根 CA 的证书和吊销列表签名用途，将其下可出现的中间 CA 数量限制为 0。
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

	// 同时把 CA 模板作为待签证书和签发者，使用 CA 私钥自签并得到 DER 编码。
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		// 根证书创建失败时，直接报告错误并结束签发。
		return fmt.Errorf("create test CA certificate: %w", err)
	}

	// 在服务端证书的 DNS SAN 中写入客户端校验的 serverName。
	// 证书沿用 CA 的有效期，声明服务端认证、数字签名和密钥加密用途。
	server := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: serverName},
		DNSNames:     []string{serverName},
		NotBefore:    ca.NotBefore,
		NotAfter:     ca.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// 使用 CA 私钥签发包含服务端公钥的证书，使客户端可以用本轮 CA 验证证书链。
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		// 服务端证书签发失败时在写出 PEM 文件之前返回错误。
		return fmt.Errorf("create test server certificate: %w", err)
	}

	// 为待写文件准备 PEM 类型、内容和创建权限。两张证书使用 DER 内容，服务端私钥使用 PKCS#1 编码。
	files := []struct {
		name  string
		block pem.Block
		mode  os.FileMode
	}{
		// 证书可供客户端或容器内的 MySQL 进程读取。
		{"ca.pem", pem.Block{Type: "CERTIFICATE", Bytes: caDER}, 0o644},
		{"server-cert.pem", pem.Block{Type: "CERTIFICATE", Bytes: serverDER}, 0o644},
		// 服务端私钥文件在宿主机上只允许属主读写。
		{"server-key.pem", pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)}, 0o600},
	}

	// 逐个将证书和私钥编码为 PEM，按预定权限写入临时目录。
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.name), pem.EncodeToMemory(&file.block), file.mode); err != nil {
			// 报告写入失败的文件名，由持有临时目录的调用方统一清理。
			return fmt.Errorf("write test certificate file %s: %w", file.name, err)
		}
	}

	// 三个文件全部写入后，调用方即可将它们装入容器。
	return nil
}

// writeAppConfig 根据本轮实际创建的数据库生成供被测应用加载的 MySQL 配置。
// rootDir 指向存放本轮 CA 的临时目录，host 和 port 是测试进程访问容器使用的地址和端口。
// databases 将业务域连接名映射到实际库名，所有连接使用 test_app 账号和传入的 password。
// 成功时将配置写入 rootDir/config/database/mysql/test.yml，并返回 config 的绝对路径。
// 路径解析、校验、编码或文件操作失败时返回空路径和错误。
func writeAppConfig(rootDir, host string, port int, password string, databases map[string]string) (string, error) {
	// 将临时目录转为绝对路径，使应用加载 YAML 时能直接定位保存在该目录下的 ca.pem。
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		// 绝对路径解析失败时终止配置生成，并保留路径解析错误。
		return "", fmt.Errorf("resolve test environment directory: %w", err)
	}

	// 按本轮业务库数量预分配连接映射。
	config := appmysql.Config{Connections: make(map[string]appmysql.ConnectionConfig, len(databases))}

	// 遍历初始化结果，使用业务域名称作为应用查找连接的键。
	for name, database := range databases {
		// 应用连接必须选择实际业务库。库名为空时报告对应的业务域。
		if database == "" {
			return "", fmt.Errorf("generated application connection %q requires a database", name)
		}
		// 使用本轮应用账号、容器地址和实际库名填充连接，并复用统一的测试连接参数。
		config.Connections[name] = testConnection(rootDir, host, port, appUser, password, database)
	}

	// 写入前用应用的配置校验检查连接命名、启用项和端点参数。
	if err := config.Validate(); err != nil {
		return "", fmt.Errorf("validate generated test MySQL configuration: %w", err)
	}

	// 包装成加载器要求的 Gormv2.Mysql 结构，命名连接位于其中的 Connections 字段。
	var document struct {
		Gormv2 struct {
			Mysql appmysql.Config `yaml:"Mysql"`
		} `yaml:"Gormv2"`
	}
	document.Gormv2.Mysql = config

	// 先编码出完整的 YAML，成功后再准备输出目录。
	data, err := yaml.Marshal(document)
	if err != nil {
		// 编码失败时报告 YAML 生成错误，停止后续文件操作。
		return "", fmt.Errorf("encode test MySQL configuration: %w", err)
	}

	// 按应用加载器约定构造 config/database/mysql 路径，并将新建目录的权限限制为属主可访问。
	configRoot := filepath.Join(rootDir, "config")
	configDir := filepath.Join(configRoot, "database", "mysql")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		// 目录准备失败时报告创建错误，供调用方定位失败阶段。
		return "", fmt.Errorf("create test MySQL configuration directory: %w", err)
	}

	// 配置包含本轮应用账号密码，创建 test.yml 时只授予文件属主读写权限。
	if err := os.WriteFile(filepath.Join(configDir, "test.yml"), data, 0o600); err != nil {
		// 写入失败时向调用方返回文件操作错误，由其清理本轮临时目录。
		return "", fmt.Errorf("write test MySQL configuration: %w", err)
	}

	// 返回配置根目录，应用据此定位 database/mysql/test.yml。
	return configRoot, nil
}

// testConnection 为本轮 MySQL 的管理连接和应用连接构造统一的连接配置。
// rootDir 用于定位本轮 CA，host 和 port 用于建立 TCP 连接，user 和 password 用于数据库认证。
// 管理连接可传空 database，用于建库前连接实例。应用连接将实际库名作为默认数据库。
// 返回的配置供调用方加入命名连接集合，再交给项目的 MySQL 组件校验和建立连接池。
func testConnection(rootDir, host string, port int, user, password, database string) appmysql.ConnectionConfig {
	// 启用连接，使用静默的 GORM 日志级别，并将慢 SQL 日志阈值设为 1 秒。
	return appmysql.ConnectionConfig{
		Enabled:       true,
		SlowThreshold: 1,
		LogLevel:      "silent",

		// 使用项目约定的单数表名，关闭 GORM 迁移时的外键生成，并统一用 UTC 毫秒时间填充自动时间字段。
		Behavior: appmysql.BehaviorConfig{
			SingularTable:                            true,
			DisableForeignKeyConstraintWhenMigrating: true,
			NowFunc:                                  appmysql.NowFuncConfig{Location: "UTC", Precision: "millisecond"},
		},

		// 每个命名连接的读写共用其 Write 端点。
		Write: appmysql.EndpointConfig{
			// 使用 Docker 映射的地址和端口，按调用方提供的账号登录并选择默认库。
			Host: host, Port: port, DataBase: database, User: user, Pass: password,
			// 连接使用 utf8mb4 和项目规定的二进制排序规则，驱动将 DATE 和 DATETIME 解析为 UTC 的 Go 时间。
			Charset: "utf8mb4", Collation: "utf8mb4_0900_bin", ParseTime: true, Loc: "UTC",

			// 为每条物理连接应用 UTC 时区、读已提交隔离级别、SQL 模式和自动提交设置。
			Session: appmysql.SessionConfig{
				TimeZone: "+00:00", TransactionIsolation: "READ-COMMITTED", SQLMode: baselineSQLMode, Autocommit: 1,
			},

			// 信任本轮 CA，用固定的 serverName 核对证书的 DNS SAN，最低允许 TLS 1.2。
			TLS: appmysql.TLSConfig{
				Enabled: true, CAFile: filepath.Join(rootDir, "ca.pem"), ServerName: serverName, MinVersion: "1.2",
			},

			// 限制每个命名连接的打开连接数和空闲连接数，控制测试实例的连接占用。
			SetMaxOpenConns: 5, SetMaxIdleConns: 1,
			// 分别限制连接的总存活时长和连续空闲时长，单位为秒。
			SetConnMaxLifetime: 60, SetConnMaxIdleTime: 30,
			// 建连、网络读取和网络写入分别使用 5 秒超时。
			ConnectTimeout: 5, ReadTimeout: 5, WriteTimeout: 5,
		},
	}
}

// mysqlConfig 返回 mysqld 实例配置的字节内容，供 Create 写入容器的 /etc/mysql/conf.d/testkit.cnf。
// 配置在首次初始化数据目录前加载，其中表名大小写规则须在初始化时确定。
// TLS 配置引用 Create 复制到容器内的 CA 证书、服务端证书和私钥。
func mysqlConfig() []byte {
	// [mysqld] 段中的选项在服务端启动时加载。
	return []byte("[mysqld]\n" +
		// 统一新建表的默认存储引擎，以及实例默认字符集和排序规则。
		"default-storage-engine=InnoDB\n" +
		"character-set-server=utf8mb4\n" +
		"collation-server=utf8mb4_0900_bin\n" +

		// 将实例的默认时区设为 UTC，供新会话继承。
		"default-time-zone=+00:00\n" +
		// 库表名以小写存储，查找时忽略大小写。该规则在初始化数据目录时确定。
		"lower-case-table-names=1\n" +

		// 让实例默认的事务隔离级别、自动提交和 SQL 模式与应用连接的会话设置一致。
		"transaction-isolation=READ-COMMITTED\n" +
		"autocommit=1\n" +
		"sql-mode=" + baselineSQLMode + "\n" +

		// 每次事务提交时同步 InnoDB redo 日志，每个提交组都同步二进制日志。
		"innodb-flush-log-at-trx-commit=1\n" +
		"sync-binlog=1\n" +
		// 开启二进制日志，使用 ROW 格式记录行级变更。
		"log-bin=mysql-bin\n" +
		"binlog-format=ROW\n" +

		// 要求安全传输，使通过 TCP 访问测试实例的客户端使用 TLS。
		"require-secure-transport=ON\n" +
		// mysqld 从容器内的固定目录加载 TLS 文件，路径与 Create 复制文件时使用的路径一致。
		"ssl-ca=/etc/mysql/testkit/ca.pem\n" +
		"ssl-cert=/etc/mysql/testkit/server-cert.pem\n" +
		"ssl-key=/etc/mysql/testkit/server-key.pem\n")
}

// certificateSerial 使用 crypto/rand 为一张 X.509 证书生成范围为 [1, 2^128-1] 的随机序列号。
// writeCertificates 分别调用它为 CA 证书和服务端证书取样。随机源出错时返回 nil 和错误。
func certificateSerial() (*big.Int, error) {
	// 构造 2^128-1 作为 rand.Int 的排他上界，为后续加 1 留出一个值。
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	limit.Sub(limit, big.NewInt(1))

	// 使用系统随机源在 [0, 2^128-2] 内均匀取样。
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		// 保留随机源错误，并标明失败发生在证书序列号生成阶段。
		return nil, fmt.Errorf("generate test certificate serial: %w", err)
	}

	// 将取样区间整体上移 1，得到严格为正且最多 128 位的序列号。
	return serial.Add(serial, big.NewInt(1)), nil
}
