package mysql

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"database/sql/driver"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// TestOpenClosesAllCreatedPoolsOnFailure 模拟第二个连接在池已创建后校验失败。
// 保护三项行为：初始化顺序稳定、此前及当前池均释放、不向调用方返回半初始化 Manager。
func TestOpenClosesAllCreatedPoolsOnFailure(t *testing.T) {
	config := clientFixture()
	backend := make(map[string]*clientFakeBackend)
	var opened []string
	opener := func(_ context.Context, connection ConnectionConfig) (*gorm.DB, *sql.DB, error) {
		name := strings.TrimSuffix(connection.Write.DataBase, "_service")
		opened = append(opened, name)
		pool, state := clientFakePool(t)
		backend[name] = state
		// 故意同时返回 pool 和 error，覆盖“不能只清理此前成功连接”的故障边界。
		if name == "post" {
			return nil, pool, errors.New("session verification failed")
		}
		return &gorm.DB{}, pool, nil
	}
	manager, err := openWith(context.Background(), config, opener)
	if err == nil || manager != nil || !strings.Contains(err.Error(), `"post"`) {
		t.Fatalf("expected a post connection failure, got manager=%v err=%v", manager, err)
	}
	if !reflect.DeepEqual(opened, []string{"order", "post"}) {
		t.Fatalf("unexpected initialization order: %v", opened)
	}
	for name, state := range backend {
		if got := state.closes.Load(); got != 1 {
			t.Errorf("connection %s was closed %d times, expected once", name, got)
		}
	}
}

// TestManagerLookupAndConcurrentClose 验证只创建启用的连接，查找失败不回退其他库。
// 并发查找与重复关闭覆盖 Manager 状态锁及关闭幂等性，配合 -race 检查共享状态访问。
// 这里不执行业务 SQL，不能据此省略真实服务退出前的请求收尾。
func TestManagerLookupAndConcurrentClose(t *testing.T) {
	config := clientFixture()
	connection := config.Connections["post"]
	connection.Enabled = false
	config.Connections["post"] = connection
	var backend []*clientFakeBackend
	manager, err := openWith(context.Background(), config, func(_ context.Context, _ ConnectionConfig) (*gorm.DB, *sql.DB, error) {
		pool, state := clientFakePool(t)
		backend = append(backend, state)
		return &gorm.DB{}, pool, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend) != 2 {
		t.Fatalf("expected two enabled connections, got %d", len(backend))
	}
	for _, name := range []string{"post", "unknown"} {
		if db, err := manager.Get(name); db != nil || err == nil {
			t.Errorf("unexpected lookup result for %s", name)
		}
	}
	if db, err := manager.Get("user"); db == nil || err != nil {
		t.Fatalf("enabled connection unavailable: %v", err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 50 {
				_, _ = manager.Get("user")
			}
			if err := manager.Close(); err != nil {
				t.Errorf("close failed: %v", err)
			}
		})
	}
	workers.Wait()
	if db, err := manager.Get("user"); db != nil || err == nil {
		t.Fatal("closed manager returned a connection")
	}
	for _, state := range backend {
		if state.closes.Load() != 1 {
			t.Fatal("close did not release each pool exactly once")
		}
	}
}

// TestOpenHonorsCanceledContext 确认已取消或缺失的启动 ctx 不会继续创建连接资源。
// 使用 errors.Is 检查取消原因仍可被调用方识别，而不只检查错误文案。
func TestOpenHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if manager, err := Open(ctx, clientFixture()); manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled initialization did not fail immediately: %v", err)
	}
	if manager, err := Open(nil, clientFixture()); manager != nil || err == nil {
		t.Fatal("nil initialization context was accepted")
	}
}

// TestDriverConfigurationAppliesSessionAndNetworkOptions 检查配置到实际驱动对象的转换。
// 保护特殊字符密码原样传递、IPv6 地址、秒转超时、每连接会话参数以及明文例外不读取 CA。
// 同时通过真实 NewConnector 的连接前回调验证字符集与比较规则的握手兼容方案，完全不联网。
func TestDriverConfigurationAppliesSessionAndNetworkOptions(t *testing.T) {
	endpoint := clientFixture().Connections["user"].Write
	// 密码中的占位符样式此时已经是解码结果，驱动层不得再次展开或按 DSN 分隔符切分。
	endpoint.Pass = "reserved:@/${NOT_AN_ENV_PLACEHOLDER}'"
	endpoint.Host = "::1"
	endpoint.TLS.CAFile = "/does/not/exist"
	config, err := makeDriverConfig(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if config.Passwd != endpoint.Pass {
		t.Fatal("structured driver configuration changed the password")
	}
	if config.Addr != "[::1]:3306" || config.DBName != "user_service" || config.Loc != time.UTC || !config.ParseTime || config.MultiStatements || config.TLS != nil || config.AllowFallbackToPlaintext {
		t.Fatal("driver connection options do not match the endpoint")
	}
	if config.Timeout != 5*time.Second || config.ReadTimeout != 10*time.Second || config.WriteTimeout != 10*time.Second {
		t.Fatal("network timeouts were not converted from seconds")
	}
	expected := map[string]string{
		"collation_connection": "'utf8mb4_0900_bin'",
		"time_zone":            "'+00:00'", "transaction_isolation": "'READ-COMMITTED'",
		"autocommit": "1", "sql_mode": "'" + endpoint.Session.SQLMode + "'",
	}
	if !reflect.DeepEqual(config.Params, expected) {
		t.Fatal("per-connection session initialization parameters are incomplete")
	}
	// 在真正的 NewConnector 克隆配置后检查 Charset 专用选项。
	// 连接前回调主动返回哨兵错误，因此能验证驱动实际保留参数，同时保证不会触发网络访问。
	stop := errors.New("stop before network connection")
	checked := false
	if err := config.Apply(mysqldriver.BeforeConnect(func(_ context.Context, actual *mysqldriver.Config) error {
		checked = true
		if _, exists := actual.Params["charset"]; exists || actual.Collation != "" || actual.Params["collation_connection"] != "'utf8mb4_0900_bin'" || strings.Count(actual.FormatDSN(), "charset=utf8mb4") != 1 {
			t.Error("connector did not separate handshake charset and session collation")
		}
		return stop
	})); err != nil {
		t.Fatal(err)
	}
	connector, err := mysqldriver.NewConnector(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.Connect(context.Background()); !errors.Is(err, stop) || !checked {
		t.Fatal("connector configuration was not checked before network access")
	}
}

// TestTLSConfigurationChecksCAAndRetainsVerification 使用临时生成的测试 CA，避免依赖外部证书文件。
// 验证最低协议、服务端名称和 CA 信任池均被保留，并确认损坏的 CA 内容会导致失败。
// 测试仅构造 TLS 配置，不模拟真实握手，实际协议及证书校验仍由 crypto/tls 执行。
func TestTLSConfigurationChecksCAAndRetainsVerification(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := makeTLSConfig(TLSConfig{Enabled: true, CAFile: path, ServerName: "db.example", MinVersion: "1.3"})
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS13 || config.InsecureSkipVerify || config.ServerName != "db.example" || config.RootCAs == nil {
		t.Fatal("TLS identity verification was not preserved")
	}
	if err := os.WriteFile(path, []byte("invalid certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := makeTLSConfig(TLSConfig{Enabled: true, CAFile: path}); err == nil {
		t.Fatal("invalid CA file was accepted")
	}
}

// TestVerifySessionUsesActualConnectionValues 将预设的数据库会话行交给实际 verifySession 扫描。
// 有效的 SQLMode 顺序、大小写、重复项差异应被接受；默认库、时区或模式集合不一致应拒绝。
// 物理连接计数同时保护“同一会话内完成检查”，而不是依赖管理客户端或另一池连接的状态。
func TestVerifySessionUsesActualConnectionValues(t *testing.T) {
	endpoint := clientFixture().Connections["user"].Write
	for _, test := range []struct {
		name      string
		change    func([]driver.Value)
		wantError string
	}{
		{name: "valid session with reordered SQL mode", change: func(values []driver.Value) {
			parts := strings.Split(values[8].(string), ",")
			for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
				parts[i], parts[j] = parts[j], parts[i]
			}
			values[8] = strings.Join(parts, ",")
		}},
		{name: "valid session with case differences and repeated mode", change: func(values []driver.Value) {
			values[8] = strings.ToLower(values[8].(string)) + ",strict_trans_tables"
		}},
		{name: "wrong database", change: func(values []driver.Value) { values[0] = "different_service" }, wantError: "database"},
		{name: "wrong timezone", change: func(values []driver.Value) { values[5] = "+08:00" }, wantError: "time_zone"},
		{name: "missing SQL mode", change: func(values []driver.Value) { values[8] = "STRICT_TRANS_TABLES" }, wantError: "sql_mode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := clientSessionValues(endpoint)
			test.change(values)
			pool, state := clientFakePool(t)
			state.query = func(query string) (driver.Rows, error) {
				if query != sessionQuery {
					return nil, errors.New("unexpected session query")
				}
				return &clientFakeRows{columns: make([]string, len(values)), data: [][]driver.Value{values}}, nil
			}
			err := verifySession(context.Background(), pool, endpoint)
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			} else if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("expected %s verification failure, got %v", test.wantError, err)
			}
			if state.connects.Load() != 1 {
				t.Fatal("session checks did not reuse one physical connection")
			}
		})
	}
}

// TestVerifySessionRejectsMissingOrWeakTLS 在同一假物理连接上返回会话行和 TLS 状态行。
// 覆盖 TLS 1.2/1.3 正常结果、空套件、旧协议及低于配置最低版本的结果。
// 该测试验证启动后的协商状态检查，不替代 makeTLSConfig 对证书身份校验的约束。
func TestVerifySessionRejectsMissingOrWeakTLS(t *testing.T) {
	endpoint := clientFixture().Connections["user"].Write
	endpoint.TLS.Enabled = true
	for _, test := range []struct {
		name, cipher, version, minimum string
		valid                          bool
	}{
		{"TLS 1.2", "AES256", "TLSv1.2", "1.2", true},
		{"TLS 1.3", "AES256", "TLSv1.3", "1.3", true},
		{"no cipher", "", "TLSv1.3", "1.2", false},
		{"old protocol", "AES256", "TLSv1.1", "1.2", false},
		{"below configured minimum", "AES256", "TLSv1.2", "1.3", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint.TLS.MinVersion = test.minimum
			pool, state := clientFakePool(t)
			state.query = func(query string) (driver.Rows, error) {
				if query == sessionQuery {
					values := clientSessionValues(endpoint)
					return &clientFakeRows{columns: make([]string, len(values)), data: [][]driver.Value{values}}, nil
				}
				return &clientFakeRows{columns: []string{"Variable_name", "Value"}, data: [][]driver.Value{{"Ssl_cipher", test.cipher}, {"Ssl_version", test.version}}}, nil
			}
			if err := verifySession(context.Background(), pool, endpoint); (err == nil) != test.valid {
				t.Fatalf("TLS validation mismatch: %v", err)
			}
			if state.connects.Load() != 1 {
				t.Fatal("TLS and session checks used different physical connections")
			}
		})
	}
}

// clientFixture 构造不依赖部署环境变量和用户密码的有效客户端配置。
// 三个连接只在数据库名上区分，以便验证命名查找和稳定初始化顺序。
// 测试注入假连接实现，因此这些地址和账号不会被用于实际网络访问。
func clientFixture() Config {
	connections := make(map[string]ConnectionConfig)
	for _, name := range []string{"user", "post", "order"} {
		connections[name] = ConnectionConfig{
			Enabled: true, SlowThreshold: 1, LogLevel: "warn",
			Behavior: BehaviorConfig{SingularTable: true, DisableForeignKeyConstraintWhenMigrating: true, NowFunc: NowFuncConfig{Location: "UTC", Precision: "millisecond"}},
			Write: EndpointConfig{
				Host: "127.0.0.1", Port: 3306, DataBase: name + "_service", User: "test", Pass: "test",
				Charset: "utf8mb4", Collation: "utf8mb4_0900_bin", ParseTime: true, Loc: "UTC",
				Session: SessionConfig{TimeZone: "+00:00", TransactionIsolation: "READ-COMMITTED", Autocommit: 1,
					SQLMode: "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION"},
				TLS:             TLSConfig{ServerName: "127.0.0.1", MinVersion: "1.2"},
				SetMaxOpenConns: 10, SetMaxIdleConns: 2, SetConnMaxLifetime: 180, SetConnMaxIdleTime: 60,
				ConnectTimeout: 5, ReadTimeout: 10, WriteTimeout: 10,
			},
		}
	}
	return Config{Connections: connections}
}

// clientSessionValues 按 sessionQuery 的列顺序构造一行数据库驱动返回值。
// 保持 database/sql 的真实 Scan 路径，便于单独替换一个字段测试基线偏差。
func clientSessionValues(endpoint EndpointConfig) []driver.Value {
	return []driver.Value{endpoint.DataBase, endpoint.Charset, endpoint.Charset, endpoint.Charset,
		endpoint.Collation, endpoint.Session.TimeZone, endpoint.Session.TransactionIsolation,
		int64(endpoint.Session.Autocommit), endpoint.Session.SQLMode}
}

// clientFakeBackend 保存一组假物理连接共享的观测状态，不实现 MySQL 线协议。
// 它用于验证连接池的生命周期和会话查询，不声称覆盖真实服务端握手行为。
type clientFakeBackend struct {
	// connects 记录实际创建的假物理连接数，用原子计数避免并发测试自身产生数据竞争。
	connects atomic.Int32
	// closes 记录物理连接 Close 次数，区分 Manager 的重复关闭和真正的重复资源释放。
	closes atomic.Int32
	// query 是每个测试在发起查询前设置的固定响应函数；测试期间不并发修改它。
	query func(string) (driver.Rows, error)
}

// clientFakePool 返回一个真正的 database/sql 池，但底层由内存假 Connector 提供连接。
// 预先 Ping 保证物理连接已经创建，使之后的池关闭能通过 closes 计数被明确观察。
// Cleanup 是测试兜底，不影响断言时对 Manager 是否主动释放资源的判断。
func clientFakePool(t *testing.T) (*sql.DB, *clientFakeBackend) {
	t.Helper()
	backend := &clientFakeBackend{}
	pool := sql.OpenDB(clientFakeConnector{backend})
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool, backend
}

// clientFakeConnector 实现 driver.Connector，让 database/sql 可以直接使用假连接，
// 无需注册全局驱动名称，也不会解析 DSN 或连接用户的数据库。
type clientFakeConnector struct{ state *clientFakeBackend }

// Connect 每次分配一个假物理连接，并共享该测试的后端状态。
func (c clientFakeConnector) Connect(context.Context) (driver.Conn, error) {
	c.state.connects.Add(1)
	return clientFakeConnection{c.state}, nil
}

// Driver 满足 Connector 接口；实际连接始终通过 Connect 创建。
func (clientFakeConnector) Driver() driver.Driver { return clientFakeDriver{} }

// clientFakeDriver 只满足 sql.OpenDB 要求的驱动接口，禁止测试误用传统驱动名称入口。
type clientFakeDriver struct{}

// Open 主动拒绝调用，防止假驱动意外绕过 Connector 观测状态。
func (clientFakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector required")
}

// clientFakeConnection 提供本测试需要的 Ping、查询和关闭能力。
// 未实现的预处理及事务操作显式失败，避免假实现悄悄掩盖生产流程新增的行为。
type clientFakeConnection struct{ state *clientFakeBackend }

// Prepare 在这些启动测试中不应被调用；实际查询使用 QueryContext。
func (c clientFakeConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}

// Close 记录资源真正释放的次数，供失败回滚和幂等关闭测试使用。
func (c clientFakeConnection) Close() error { c.state.closes.Add(1); return nil }

// Begin 拒绝事务操作，因为这里验证的是初始化及会话读取，而非业务事务。
func (clientFakeConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction unsupported")
}

// Ping 允许 database/sql 打开并复用该假连接，不产生网络 I/O。
func (clientFakeConnection) Ping(context.Context) error { return nil }

// QueryContext 把查询交给当前测试的响应函数，保留 database/sql 的真实行扫描行为。
func (c clientFakeConnection) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.state.query == nil {
		return nil, errors.New("query unsupported")
	}
	return c.state.query(query)
}

// clientFakeRows 是有限的内存结果集，用于模拟单行会话查询和多行 TLS 状态查询。
type clientFakeRows struct {
	// columns 提供 Scan 所需的列数量及 TLS 状态查询列名。
	columns []string
	// data 按 driver.Value 保存返回行，保持与真实驱动相同的值转换边界。
	data [][]driver.Value
	// position 是下一行位置；所有读取由同一测试查询顺序执行，不需要并发访问。
	position int
}

// Columns 让 database/sql 确认结果列数，再执行实际的 Scan 类型转换。
func (r *clientFakeRows) Columns() []string { return r.columns }

// Close 不持有外部资源；连接池是否释放由 clientFakeConnection.Close 独立观测。
func (*clientFakeRows) Close() error { return nil }

// Next 按顺序复制一行，到达末尾返回 io.EOF，以覆盖生产代码的 rows.Next/rows.Err 流程。
func (r *clientFakeRows) Next(values []driver.Value) error {
	if r.position >= len(r.data) {
		return io.EOF
	}
	copy(values, r.data[r.position])
	r.position++
	return nil
}
