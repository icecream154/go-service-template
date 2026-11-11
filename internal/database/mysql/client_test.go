package mysql

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"database/sql/driver"
	"encoding/pem"
	"errors"
	"io/fs"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/gorm"
)

type openCase struct {
	name        string
	ctx         context.Context
	config      Config
	wantErr     error
	wantMessage string
}

func TestOpen(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	badCA := clientConnection("example_db")
	badCA.Write.TLS = TLSConfig{
		Enabled: true, CAFile: filepath.Join(t.TempDir(), "absent.pem"), ServerName: "localhost", MinVersion: "1.2",
	}
	tests := []openCase{
		// 缺少上下文时应拒绝启动，避免用隐式上下文建立连接。
		{name: "nil context", wantMessage: "context is required"},
		// 启动前已经取消的上下文应保留取消原因，并且优先于配置校验。
		{name: "canceled context", ctx: canceled, wantErr: context.Canceled},
		// 没有任何连接的配置应在创建连接池前被拒绝。
		{name: "invalid config", ctx: context.Background(), wantMessage: "at least one connection"},
		// 开启 TLS 但 CA 不存在时应保留文件错误，不进入网络建连。
		{name: "unreadable CA", ctx: context.Background(), config: Config{Connections: map[string]ConnectionConfig{"db": badCA}}, wantErr: fs.ErrNotExist},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, err := Open(test.ctx, test.config)
			if manager != nil {
				t.Errorf("Open() manager = %v, want nil", manager)
			}
			assertClientError(t, err, test.wantErr, test.wantMessage)
		})
	}
}

func TestOpenWith(t *testing.T) {
	t.Run("opens enabled connections in name order", func(t *testing.T) {
		// 两个启用项和一个禁用项验证按名称初始化、跳过禁用项并复用同一个启动上下文。
		ctx := t.Context()
		config := Config{Connections: map[string]ConnectionConfig{
			"second": clientConnection("second"),
			"off":    {},
			"first":  clientConnection("first"),
		}}
		first, second := &gorm.DB{Config: &gorm.Config{}}, &gorm.DB{Config: &gorm.Config{}}
		wantConnections := map[string]*gorm.DB{"first": first, "second": second}
		var opened []string
		manager, err := openWith(ctx, config, func(gotCtx context.Context, connection ConnectionConfig) (*gorm.DB, *sql.DB, error) {
			if gotCtx != ctx {
				t.Error("opener received a different context")
			}
			name := connection.Write.DataBase
			opened = append(opened, name)
			pool, mock := newClientMock(t)
			mock.ExpectClose()
			return wantConnections[name], pool, nil
		})
		if err != nil || manager == nil {
			t.Fatalf("openWith() = %v, %v, want manager and nil error", manager, err)
		}
		if !slices.Equal(opened, []string{"first", "second"}) {
			t.Errorf("opened = %v, want [first second]", opened)
		}
		for name, want := range wantConnections {
			got, err := manager.Get(name)
			if err != nil || got != want {
				t.Errorf("Get(%q) = %p, %v, want %p, nil", name, got, err, want)
			}
		}
		if err := manager.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	t.Run("rolls back after allocating a failing pool", func(t *testing.T) {
		// 第二项建池后失败，验证两池都会关闭，且返回原始错误和清理错误。
		firstPool, firstMock := newClientMock(t)
		closeErr := errors.New("first pool close failed")
		firstMock.ExpectClose().WillReturnError(closeErr)
		secondPool, secondMock := newClientMock(t)
		secondMock.ExpectClose()
		openErr := errors.New("second pool initialization failed")
		config := Config{Connections: map[string]ConnectionConfig{
			"first": clientConnection("first"), "second": clientConnection("second"), "third": clientConnection("third"),
		}}
		var opened []string
		manager, err := openWith(t.Context(), config, func(_ context.Context, connection ConnectionConfig) (*gorm.DB, *sql.DB, error) {
			name := connection.Write.DataBase
			opened = append(opened, name)
			if name == "first" {
				return &gorm.DB{}, firstPool, nil
			}
			return nil, secondPool, openErr
		})
		if manager != nil {
			t.Errorf("openWith() manager = %v, want nil", manager)
		}
		assertClientError(t, err, openErr, "second")
		assertClientError(t, err, closeErr, "first")
		if !slices.Equal(opened, []string{"first", "second"}) {
			t.Errorf("opened = %v, want [first second]", opened)
		}
	})

	t.Run("rolls back on cancellation between connections", func(t *testing.T) {
		// 第一项初始化完成时取消共享上下文，验证下一项不再建池且第一池仍会被关闭。
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pool, mock := newClientMock(t)
		mock.ExpectClose()
		config := Config{Connections: map[string]ConnectionConfig{
			"second": clientConnection("second"), "first": clientConnection("first"),
		}}
		var opened []string
		manager, err := openWith(ctx, config, func(_ context.Context, connection ConnectionConfig) (*gorm.DB, *sql.DB, error) {
			opened = append(opened, connection.Write.DataBase)
			cancel()
			return &gorm.DB{}, pool, nil
		})
		if manager != nil {
			t.Errorf("openWith() manager = %v, want nil", manager)
		}
		assertClientError(t, err, context.Canceled, "second")
		if !slices.Equal(opened, []string{"first"}) {
			t.Errorf("opened = %v, want [first]", opened)
		}
	})
}

type getConnectionCase struct {
	name        string
	connection  string
	closed      bool
	want        *gorm.DB
	wantMessage string
}

func TestManagerGet(t *testing.T) {
	db := &gorm.DB{}
	tests := []getConnectionCase{
		// 查找已注册名称时应取得原有 GORM 入口。
		{name: "registered", connection: "user", want: db},
		// 缺少名称时应报告该连接不可用，避免回退到已有的连接。
		{name: "unregistered", connection: "order", wantMessage: `connection "order" is not available`},
		// 关闭后的管理器即使仍有注册项也应拒绝分发。
		{name: "closed", connection: "user", closed: true, wantMessage: "manager is closed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{connections: map[string]*gorm.DB{"user": db}, closed: test.closed}
			got, err := manager.Get(test.connection)
			assertClientError(t, err, nil, test.wantMessage)
			if got != test.want {
				t.Errorf("Get(%q) = %p, want %p", test.connection, got, test.want)
			}
		})
	}
}

func TestManagerClose(t *testing.T) {
	// 两个池关闭失败且中间池成功，验证关闭会继续、保留两项原因，并复用首次结果。
	first, firstMock := newClientMock(t)
	middle, middleMock := newClientMock(t)
	last, lastMock := newClientMock(t)
	firstErr, lastErr := errors.New("first pool close failed"), errors.New("last pool close failed")
	firstMock.ExpectClose().WillReturnError(firstErr)
	middleMock.ExpectClose()
	lastMock.ExpectClose().WillReturnError(lastErr)
	manager := &Manager{
		connections: map[string]*gorm.DB{"first": {}},
		pools:       map[string]*sql.DB{"last": last, "middle": middle, "first": first},
	}

	err := manager.Close()
	assertClientError(t, err, firstErr, `close database connection "first"`)
	assertClientError(t, err, lastErr, `close database connection "last"`)
	if err != nil && strings.Index(err.Error(), "first") > strings.Index(err.Error(), "last") {
		t.Errorf("Close() reported errors out of connection name order: %v", err)
	}
	if again := manager.Close(); again != err {
		t.Errorf("repeated Close() = %v, want original error %v", again, err)
	}
	db, getErr := manager.Get("first")
	if db != nil {
		t.Errorf("Get() after failed Close() = %p, want nil", db)
	}
	assertClientError(t, getErr, nil, "manager is closed")
}

func TestMakeDriverConfig(t *testing.T) {
	// IPv6、含标点的密码和各不相同的秒数验证驱动配置保留原值、正确拼接地址并转换超时单位。
	endpoint := clientConnection("user_db").Write
	endpoint.Host = "::1"
	endpoint.Pass = "fixture@pass/?word"
	endpoint.ConnectTimeout = 2
	endpoint.ReadTimeout = 3
	endpoint.WriteTimeout = 5
	config, err := makeDriverConfig(endpoint)
	if err != nil {
		t.Fatalf("makeDriverConfig() = %v", err)
	}
	if config.Net != "tcp" || config.Addr != "[::1]:3306" {
		t.Errorf("network/address = %q/%q, want tcp/[::1]:3306", config.Net, config.Addr)
	}
	if config.User != endpoint.User || config.Passwd != endpoint.Pass || config.DBName != "user_db" {
		t.Error("driver account or default database did not preserve endpoint values")
	}
	if !config.ParseTime || config.Loc != time.UTC || config.MultiStatements {
		t.Errorf("parse time/location/multiple statements = %v/%v/%v, want true/UTC/false", config.ParseTime, config.Loc, config.MultiStatements)
	}
	if config.TLS != nil || config.AllowFallbackToPlaintext {
		t.Error("development exception unexpectedly configures TLS or plaintext fallback")
	}
	if config.Timeout != 2*time.Second || config.ReadTimeout != 3*time.Second || config.WriteTimeout != 5*time.Second {
		t.Errorf("timeouts = %v/%v/%v, want 2s/3s/5s", config.Timeout, config.ReadTimeout, config.WriteTimeout)
	}
	wantParams := map[string]string{
		"collation_connection":  "'utf8mb4_0900_bin'",
		"time_zone":             "'+00:00'",
		"transaction_isolation": "'READ-COMMITTED'",
		"autocommit":            "1",
		"sql_mode":              "'" + clientSQLMode + "'",
	}
	if !maps.Equal(config.Params, wantParams) || config.Collation != "" {
		t.Errorf("session params/collation = %v/%q, want %v/empty handshake collation", config.Params, config.Collation, wantParams)
	}
}

type tlsConfigCase struct {
	name        string
	config      TLSConfig
	wantVersion uint16
	wantErr     error
	wantMessage string
}

func TestMakeTLSConfig(t *testing.T) {
	dir := t.TempDir()
	caFile, roots := writeClientCA(t, dir)
	badFile := filepath.Join(dir, "not-a-ca.pem")
	if err := os.WriteFile(badFile, []byte("invalid PEM"), 0o600); err != nil {
		t.Fatalf("write invalid CA: %v", err)
	}
	missing := filepath.Join(dir, "missing.pem")
	tests := []tlsConfigCase{
		// 明文开发例外给出不存在的 CA，验证关闭 TLS 时不会读取证书文件。
		{name: "disabled", config: TLSConfig{CAFile: missing}},
		// 启用 TLS 后使用同一个不存在的路径，验证错误保留底层文件原因。
		{name: "missing CA", config: TLSConfig{Enabled: true, CAFile: missing}, wantErr: fs.ErrNotExist},
		// 文件可读但没有 PEM 证书，验证拒绝空信任池。
		{name: "invalid PEM", config: TLSConfig{Enabled: true, CAFile: badFile}, wantMessage: "valid certificate"},
		// 用显式 CA 和主机名构造 1.2 配置，验证信任池与服务端校验设置。
		{name: "TLS 1.2", config: TLSConfig{Enabled: true, CAFile: caFile, ServerName: "mysql.example.test", MinVersion: "1.2"}, wantVersion: tls.VersionTLS12},
		// 将最低版本改为 1.3，验证该设置传递到 TLS 配置。
		{name: "TLS 1.3", config: TLSConfig{Enabled: true, CAFile: caFile, ServerName: "mysql.example.test", MinVersion: "1.3"}, wantVersion: tls.VersionTLS13},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := makeTLSConfig(test.config)
			assertClientError(t, err, test.wantErr, test.wantMessage)
			if test.wantVersion == 0 {
				if got != nil {
					t.Errorf("makeTLSConfig() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("makeTLSConfig() returned nil configuration")
			}
			if got.MinVersion != test.wantVersion || got.ServerName != test.config.ServerName || got.InsecureSkipVerify {
				t.Errorf("version/server name/skip verification = %x/%q/%v, want %x/%q/false",
					got.MinVersion, got.ServerName, got.InsecureSkipVerify, test.wantVersion, test.config.ServerName)
			}
			if got.RootCAs == nil || !got.RootCAs.Equal(roots) {
				t.Error("root CA pool differs from the certificate supplied in CAFile")
			}
		})
	}
}

type verifySessionCase struct {
	name            string
	withoutDatabase bool
	column          string
	value           driver.Value
	queryErr        error
	cancel          bool
	wantErr         error
	wantMessage     string
}

func TestVerifySession(t *testing.T) {
	queryErr := errors.New("session query failed")
	tests := []verifySessionCase{
		// 使用正确的库名并重排、混用大小写及重复 SQL 模式，验证按集合比较模式。
		{name: "matching session"},
		// 未配置默认库时用 SQL NULL，验证这一状态可以被读取和接受。
		{name: "no default database", withoutDatabase: true},
		// 未配置默认库时返回空字符串，验证空字符串不能替代 SQL NULL。
		{name: "empty database is not NULL", withoutDatabase: true, column: "database", value: "", wantMessage: "session database"},
		// 已配置默认库时返回 SQL NULL，验证实际未选择默认库会被拒绝。
		{name: "expected database is missing", column: "database", wantMessage: "session database"},
		// 返回另一个有效库名，验证具体名称仍须与配置一致。
		{name: "different database", column: "database", value: "other_db", wantMessage: "session database"},
		// 分别改变客户端、连接、结果字符集，验证各自的错误定位。
		{name: "client character set", column: "character_set_client", value: "latin1", wantMessage: "session character_set_client"},
		// 仅改变连接字符集，验证不会把客户端字符集的匹配当作通过。
		{name: "connection character set", column: "character_set_connection", value: "latin1", wantMessage: "session character_set_connection"},
		// 仅改变结果字符集，验证结果传输也受校验。
		{name: "result character set", column: "character_set_results", value: "latin1", wantMessage: "session character_set_results"},
		// 字符集仍正确但排序规则不同，验证排序规则单独检查。
		{name: "collation", column: "collation_connection", value: "utf8mb4_general_ci", wantMessage: "session collation_connection"},
		// 使用非 UTC 偏移，验证会话时区独立校验。
		{name: "time zone", column: "time_zone", value: "+08:00", wantMessage: "session time_zone"},
		// 返回另一种合法隔离级别，验证会话必须按配置选择隔离级别。
		{name: "isolation level", column: "transaction_isolation", value: "REPEATABLE-READ", wantMessage: "session transaction_isolation"},
		// 将 autocommit 从 1 改为 0，验证关闭自动提交会被拒绝。
		{name: "autocommit", column: "autocommit", value: int64(0), wantMessage: "session autocommit"},
		// 仅保留一种 SQL 模式，验证集合比较仍要求所有模式一致。
		{name: "missing SQL modes", column: "sql_mode", value: "STRICT_TRANS_TABLES", wantMessage: "session sql_mode"},
		// autocommit 返回非整数字符串，验证扫描错误与字段不匹配可以区分。
		{name: "invalid scanned value", column: "autocommit", value: "not an integer", wantMessage: "verify MySQL session"},
		// 让查询直接返回错误，验证原始原因仍能由 errors.Is 找到。
		{name: "query fails", queryErr: queryErr, wantErr: queryErr, wantMessage: "verify MySQL session"},
		// 取得连接前取消上下文，验证错误出现在获取连接阶段，且不会查询。
		{name: "canceled before connection", cancel: true, wantErr: context.Canceled, wantMessage: "acquire MySQL connection"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, mock := newClientMock(t)
			endpoint := clientConnection("user_db").Write
			if test.withoutDatabase {
				endpoint.DataBase = ""
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch {
			case test.cancel:
				cancel()
			case test.queryErr != nil:
				mock.ExpectQuery(sessionQuery).WillReturnError(test.queryErr)
			default:
				expectClientSession(t, mock, endpoint, test.column, test.value)
			}
			assertClientError(t, verifySession(ctx, pool, endpoint), test.wantErr, test.wantMessage)
		})
	}
}

type verifyTLSCase struct {
	name        string
	minimum     string
	rows        *sqlmock.Rows
	queryErr    error
	wantErr     error
	wantMessage string
}

func TestVerifySessionTLS(t *testing.T) {
	queryErr := errors.New("TLS query failed")
	rowErr := errors.New("TLS status row failed")
	const statusQuery = "SHOW SESSION STATUS WHERE Variable_name IN ('Ssl_cipher', 'Ssl_version')"
	const requirementError = "TLS session does not meet configured requirements"
	tests := []verifyTLSCase{
		// 先返回版本再返回套件，验证状态行顺序不影响最低版本判断。
		{name: "matching TLS 1.2", minimum: "1.2", rows: clientTLSRows().AddRow("Ssl_version", "TLSv1.2").AddRow("Ssl_cipher", "fixture-cipher")},
		// 使用 TLS 1.3 满足显式 1.3 配置，验证更高的最低版本被接受。
		{name: "matching TLS 1.3", minimum: "1.3", rows: clientTLSRows().AddRow("Ssl_cipher", "fixture-cipher").AddRow("Ssl_version", "TLSv1.3")},
		// 返回受支持的 1.2 但要求至少 1.3，验证协商版本还需满足配置。
		{name: "below configured minimum", minimum: "1.3", rows: clientTLSRows().AddRow("Ssl_cipher", "fixture-cipher").AddRow("Ssl_version", "TLSv1.2"), wantMessage: requirementError},
		// 返回过旧版本，验证非空套件不能使不支持的版本通过。
		{name: "obsolete TLS version", minimum: "1.2", rows: clientTLSRows().AddRow("Ssl_cipher", "fixture-cipher").AddRow("Ssl_version", "TLSv1.1"), wantMessage: requirementError},
		// 只有版本没有套件状态，验证缺失行会导致校验失败。
		{name: "missing cipher row", minimum: "1.2", rows: clientTLSRows().AddRow("Ssl_version", "TLSv1.3"), wantMessage: requirementError},
		// 套件字段是 SQL NULL，验证无法扫描的结果会作为读取错误返回。
		{name: "invalid status column", minimum: "1.2", rows: clientTLSRows().AddRow("Ssl_cipher", nil), wantMessage: "read MySQL TLS status"},
		// 模拟读取第二行时失败，验证迭代错误仍可在错误链中找到。
		{name: "status iteration fails", minimum: "1.2", rows: clientTLSRows().AddRow("Ssl_cipher", "fixture-cipher").AddRow("Ssl_version", "TLSv1.3").RowError(1, rowErr), wantErr: rowErr, wantMessage: "read MySQL TLS status"},
		// 会话查询成功而 TLS 状态查询失败，验证错误定位到 TLS 阶段。
		{name: "status query fails", minimum: "1.2", queryErr: queryErr, wantErr: queryErr, wantMessage: "verify MySQL TLS session"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, mock := newClientMock(t)
			endpoint := clientConnection("user_db").Write
			endpoint.TLS = TLSConfig{Enabled: true, MinVersion: test.minimum}
			expectClientSession(t, mock, endpoint, "", nil)
			query := mock.ExpectQuery(statusQuery)
			if test.queryErr != nil {
				query.WillReturnError(test.queryErr)
			} else {
				query.WillReturnRows(test.rows).RowsWillBeClosed()
			}
			assertClientError(t, verifySession(t.Context(), pool, endpoint), test.wantErr, test.wantMessage)
		})
	}
}

const clientSQLMode = "ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION,NO_ZERO_DATE,NO_ZERO_IN_DATE,ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES"

func clientConnection(database string) ConnectionConfig {
	return ConnectionConfig{
		Enabled: true, SlowThreshold: 1, LogLevel: "silent",
		Behavior: BehaviorConfig{
			SingularTable: true, DisableForeignKeyConstraintWhenMigrating: true,
			NowFunc: NowFuncConfig{Location: "UTC", Precision: "millisecond"},
		},
		Write: EndpointConfig{
			Host: "localhost", Port: 3306, User: "fixture_user", Pass: "fixture_password", DataBase: database,
			Charset: "utf8mb4", Collation: "utf8mb4_0900_bin", ParseTime: true, Loc: "UTC",
			Session: SessionConfig{
				TimeZone: "+00:00", TransactionIsolation: "READ-COMMITTED", Autocommit: 1, SQLMode: clientSQLMode,
			},
			SetMaxOpenConns: 2, SetMaxIdleConns: 1, SetConnMaxLifetime: 60, SetConnMaxIdleTime: 20,
			ConnectTimeout: 2, ReadTimeout: 3, WriteTimeout: 4,
		},
	}
}

func newClientMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	pool, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New() = %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet MySQL mock expectations: %v", err)
		}
		// 测试未委托管理器关闭连接池时，由清理步骤释放替身。
		_ = pool.Close()
	})
	return pool, mock
}

func writeClientCA(t *testing.T, dir string) (string, *x509.CertPool) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "MySQL unit-test CA"},
		NotBefore:             time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2040, time.January, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	if err != nil {
		t.Fatalf("create fixture CA: %v", err)
	}
	file := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write fixture CA: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse fixture CA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return file, roots
}

func expectClientSession(t *testing.T, mock sqlmock.Sqlmock, endpoint EndpointConfig, column string, value driver.Value) {
	t.Helper()
	columns := []string{
		"database", "character_set_client", "character_set_connection", "character_set_results",
		"collation_connection", "time_zone", "transaction_isolation", "autocommit", "sql_mode",
	}
	var database driver.Value
	if endpoint.DataBase != "" {
		database = endpoint.DataBase
	}
	values := []driver.Value{
		database, "utf8mb4", "utf8mb4", "utf8mb4", "utf8mb4_0900_bin", "+00:00", "READ-COMMITTED", int64(1),
		"strict_trans_tables, NO_ZERO_IN_DATE, only_full_group_by, NO_ZERO_DATE, no_engine_substitution, ERROR_FOR_DIVISION_BY_ZERO, STRICT_TRANS_TABLES",
	}
	if column != "" {
		index := slices.Index(columns, column)
		if index < 0 {
			t.Fatalf("unknown mock session column %q", column)
		}
		values[index] = value
	}
	mock.ExpectQuery(sessionQuery).WillReturnRows(sqlmock.NewRows(columns).AddRow(values...)).RowsWillBeClosed()
}

func clientTLSRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"Variable_name", "Value"})
}

func assertClientError(t *testing.T, err, wantErr error, wantMessage string) {
	t.Helper()
	if wantErr == nil && wantMessage == "" {
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		return
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want cause %v", err, wantErr)
	}
	if wantMessage != "" && (err == nil || !strings.Contains(err.Error(), wantMessage)) {
		t.Errorf("error = %v, want message containing %q", err, wantMessage)
	}
}
