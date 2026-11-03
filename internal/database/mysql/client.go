package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Manager 持有命名连接及各自的底层连接池，由应用启动和退出流程管理生命周期。
// 每个启用的命名连接拥有独立的 *sql.DB 连接池，即使它们使用相同的地址或账号。
// *gorm.DB 是该连接池上的 ORM 入口，不代表一个独立物理连接或一个已开启的事务。
//
// Get 返回的连接可供多个业务协程使用；仓储必须明确选择连接名称。
// 调用方不应自行关闭底层连接池，也不能假设不同命名连接上的操作属于同一事务。
// 退出时应先结束 HTTP 请求和后台任务，再由持有 Manager 的应用运行时调用 Close。
type Manager struct {
	// mu 保护连接注册表、关闭标记及关闭结果。
	// Get 返回之后即释放读锁，锁不覆盖调用方随后执行 SQL 的整个生命周期。
	mu sync.RWMutex
	// connections 保存已完成连通性及会话校验的 GORM 入口。
	// 只有 Open 全部成功后，外部调用方才会获得持有这份注册表的 Manager。
	connections map[string]*gorm.DB
	// pools 保存 Manager 拥有的底层连接池，关闭由本组件统一负责。
	// 启动期间它还可能包含校验失败的当前池，确保失败清理不遗漏已分配资源。
	pools map[string]*sql.DB
	// closed 表示已进入关闭流程。设置后拒绝新的 Get，即使某个池关闭返回错误。
	closed bool
	// closeErr 聚合首次关闭时的全部错误，重复 Close 返回同一结果而不重复关闭池。
	closeErr error
}

// connectionOpener 将单个命名连接的创建与 Manager 的整体初始化分开。
// 生产环境使用 openConnection；测试可注入无网络的假实现来覆盖中途失败及资源释放。
// 一旦实现创建了池，即使同时返回 error，也必须把池返回给 openWith 接管并关闭。
type connectionOpener func(context.Context, ConnectionConfig) (*gorm.DB, *sql.DB, error)

// Open 创建所有启用的连接，验证连接与会话配置后才返回可用的管理器。
//
// 调用方应在启动入口为 ctx 设置总超时；同一个 ctx 贯穿各连接的建立、Ping 和会话检查，
// 而不是为每个连接重新分配一份完整启动预算。配置中的网络超时仍独立约束单次网络 I/O。
// ctx 不能为 nil；取消或超时会作为初始化错误向上传递，供启动入口决定退出。
//
// 参数会再次 Validate，防止调用方手动构造 Config 绕过加载阶段的约束。
// 任一连接失败时返回 nil Manager，并关闭本次已创建的所有连接池，避免业务接触部分初始化结果。
// 此函数不创建数据库、不迁移表结构、不发布全局变量，也不自行打印初始化失败日志。
func Open(ctx context.Context, config Config) (*Manager, error) {
	return openWith(ctx, config, openConnection)
}

// openWith 负责多连接初始化的统一顺序和失败回滚，单连接细节由 open 参数负责。
// 命名返回值 err 供 defer 判断最终结果，并把清理错误与原始初始化错误一起返回。
// 该函数只在启动阶段组装本地 Manager；组装期间没有其他协程访问它，因此不持有 Manager 锁。
func openWith(ctx context.Context, config Config, open connectionOpener) (_ *Manager, err error) {
	if ctx == nil {
		return nil, errors.New("database initialization context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("initialize database connections: %w", err)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// 在局部变量中收集资源，全部成功后才将 Manager 交给外部业务。
	manager := &Manager{
		connections: make(map[string]*gorm.DB),
		pools:       make(map[string]*sql.DB),
	}
	// 清理不复用已经超时的 ctx，保证失败路径仍会尝试关闭所有已创建的池。
	// errors.Join 保留初始化原因及清理原因，避免后一项覆盖前一项。
	defer func() {
		if err != nil {
			err = errors.Join(err, manager.Close())
		}
	}()

	// Go map 没有稳定遍历顺序，因此先筛出启用项再排序。
	// 固定顺序便于定位“哪个连接先失败”，也让回滚验证结果可重现。
	names := make([]string, 0, len(config.Connections))
	for name, connection := range config.Connections {
		if connection.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		// 每个连接共享剩余启动预算；已取消时不再为后续连接分配资源。
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("initialize database connection %q: %w", name, err)
		}
		db, pool, openErr := open(ctx, config.Connections[name])
		// 单连接创建可能在池已分配后才失败，例如 Ping、会话检查或 GORM 初始化失败。
		// 先登记非 nil 池，再处理错误，确保当前池和此前成功的池都进入 defer 清理。
		if pool != nil {
			manager.pools[name] = pool
		}
		if openErr != nil {
			return nil, fmt.Errorf("initialize database connection %q: %w", name, openErr)
		}
		// 只有当前连接完整成功，才登记其业务访问入口。
		manager.connections[name] = db
	}
	return manager, nil
}

// Get 按配置名称获取连接；未知、未启用或已关闭的连接均返回错误，不回退到其他库。
//
// 本函数允许并发调用，并与 Close 共享关闭状态检查，避免关闭后继续分发入口。
// 返回的是共享的 GORM 根入口；业务查询应通过 WithContext 等链式调用传入各自上下文。
// 读锁只保护本次查找，不保证返回后连接池始终开放；应用仍须先完成业务收尾再关闭 Manager。
func (m *Manager) Get(name string) (*gorm.DB, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, errors.New("database manager is closed")
	}
	db, ok := m.connections[name]
	if !ok {
		return nil, fmt.Errorf("database connection %q is not available", name)
	}
	return db, nil
}

// Close 关闭所有底层连接池。可重复调用，并返回首次关闭时收集到的错误。
//
// 写锁使并发 Close 只执行一轮释放，其余调用等待后读取同一结果。
// closed 在释放资源之前设置，保证即使某个池关闭失败，也不会重新开放 Manager。
// 某个池失败不会阻止其他池的关闭，所有错误会使用 errors.Join 聚合。
//
// 调用方必须先结束仍可能使用 Get 返回连接的请求或任务；该锁不能代替 HTTP 收尾机制。
// 这里不接收 ctx，因为即使启动或请求上下文已经取消，资源释放仍必须执行。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.closeErr
	}
	m.closed = true
	// 关闭顺序与 map 遍历无关，使多个关闭错误的排列稳定、便于定位。
	names := make([]string, 0, len(m.pools))
	for name := range m.pools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := m.pools[name].Close(); err != nil {
			m.closeErr = errors.Join(m.closeErr, fmt.Errorf("close database connection %q: %w", name, err))
		}
	}
	return m.closeErr
}

// openConnection 为一个命名连接创建主库池并构造 GORM 入口。
// 配置校验已经保证当前仅启用 Write；不在这里静默回退或自动打开 Read。
// 返回的 pool 归 openWith/Manager 所有，出现错误时也返回已分配的池供统一回滚。
func openConnection(ctx context.Context, config ConnectionConfig) (*gorm.DB, *sql.DB, error) {
	endpoint := config.Write
	driverConfig, err := makeDriverConfig(endpoint)
	if err != nil {
		return nil, nil, err
	}
	connector, err := driver.NewConnector(driverConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("configure MySQL connector: %w", err)
	}
	// OpenDB 创建的是可复用的连接池，不代表此刻已经与服务器成功通信。
	// 连接数、连接寿命和空闲时间分别应用；配置中秒数已由 Validate 检查合法范围。
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(endpoint.SetMaxOpenConns)
	pool.SetMaxIdleConns(endpoint.SetMaxIdleConns)
	pool.SetConnMaxLifetime(time.Duration(endpoint.SetConnMaxLifetime) * time.Second)
	pool.SetConnMaxIdleTime(time.Duration(endpoint.SetConnMaxIdleTime) * time.Second)
	// 主动连通性检查暴露地址、账号、TLS、默认数据库等启动错误。
	// Ping 和后面的会话检查共享启动 ctx，不使用无期限的后台上下文。
	if err := pool.PingContext(ctx); err != nil {
		return nil, pool, fmt.Errorf("ping MySQL: %w", err)
	}
	if err := verifySession(ctx, pool, endpoint); err != nil {
		return nil, pool, err
	}

	// LogLevel 允许值已在配置阶段验证，避免未知文本被映射成意外的默认级别。
	level := map[string]logger.LogLevel{
		"silent": logger.Silent,
		"error":  logger.Error,
		"warn":   logger.Warn,
		"info":   logger.Info,
	}[config.LogLevel]
	// 把已验证的池交给 GORM，而不是让 ORM 再根据 DSN 创建另一套池。
	// 数据库结构通过受控 SQL 维护，本流程没有 AutoMigrate 或自动建库入口。
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn: pool,
		// MySQL 8.4 是本项目基线；避免 GORM 额外执行不带 ctx 的版本探测查询。
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		// 默认事务仅覆盖 GORM 单次写操作，不能代替多步骤业务流程的显式事务。
		SkipDefaultTransaction:                   config.Behavior.SkipDefaultTransaction,
		PrepareStmt:                              config.Behavior.PrepareStmt,
		TranslateError:                           config.Behavior.TranslateError,
		DisableForeignKeyConstraintWhenMigrating: config.Behavior.DisableForeignKeyConstraintWhenMigrating,
		// 前面已执行带 ctx 的 Ping，关闭 GORM 默认的无 ctx Ping，避免突破启动预算。
		DisableAutomaticPing: true,
		NamingStrategy:       schema.NamingStrategy{SingularTable: config.Behavior.SingularTable},
		// 只负责 ORM 自动生成的时间字段；业务显式传入的事件时间仍需自行转为 UTC 并对齐精度。
		NowFunc: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) },
		Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			// 慢查询阈值用于日志判定，不会自动取消 SQL；执行期限由业务查询 ctx 控制。
			SlowThreshold: time.Duration(config.SlowThreshold) * time.Second,
			LogLevel:      level,
			// SQL 参数保留为占位符，避免日志主动展开业务参数；日志模板沿用英文。
			ParameterizedQueries: true,
			// 是否记录“未找到”的日志与是否抑制业务 ErrRecordNotFound 是不同概念。
			IgnoreRecordNotFoundError: false,
			Colorful:                  false,
		}),
	})
	if err != nil {
		return nil, pool, fmt.Errorf("initialize GORM: %w", err)
	}
	return db, pool, nil
}

// makeDriverConfig 使用结构化参数传递账号和密码，不手工拼接或记录 DSN。
// 调用前已完成 EndpointConfig 校验，因此这里按当前支持的 UTC、TLS 和会话语义构造驱动配置。
// 字符集和 Params 由驱动在每次建立物理连接时应用，不是仅在池启动时任取一个连接执行 SET。
func makeDriverConfig(endpoint EndpointConfig) (*driver.Config, error) {
	tlsConfig, err := makeTLSConfig(endpoint.TLS)
	if err != nil {
		return nil, err
	}
	config := driver.NewConfig()
	config.Net = "tcp"
	// JoinHostPort 同时支持普通主机名、IPv4 和需要方括号的 IPv6 地址。
	config.Addr = net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
	config.User = endpoint.User
	config.Passwd = endpoint.Pass
	config.DBName = endpoint.DataBase
	// Charset 专用选项负责每个新连接上的 SET NAMES，不能放进通用 Params。
	// MySQL 握手的比较规则 ID 只有一个字节，utf8mb4_0900_bin 不在驱动的握手映射中。
	// 因此握手不指定 Collation，在 SET NAMES 之后用会话参数设置目标比较规则。
	if err := config.Apply(driver.Charset(endpoint.Charset, "")); err != nil {
		return nil, fmt.Errorf("configure MySQL character set: %w", err)
	}
	config.ParseTime = endpoint.ParseTime
	// Loc 控制驱动对 Go time.Time 的解释；MySQL 会话 time_zone 仍需在 Params 中单独设置。
	config.Loc = time.UTC
	config.MultiStatements = endpoint.MultiStatements
	// 直接挂载连接私有 TLS 配置，避免全局 RegisterTLSConfig 的名称冲突和注册项清理问题。
	config.TLS = tlsConfig
	config.AllowFallbackToPlaintext = false
	config.Timeout = time.Duration(endpoint.ConnectTimeout) * time.Second
	config.ReadTimeout = time.Duration(endpoint.ReadTimeout) * time.Second
	config.WriteTimeout = time.Duration(endpoint.WriteTimeout) * time.Second
	// 通用 Params 的值是 MySQL SET 语句右侧表达式，不是已经 URL 编码的 DSN 文本。
	// 字符串值需要 SQL 单引号；autocommit 是数值。它们在 SET NAMES 之后执行。
	// 此处只使用配置校验允许的会话字段，不能接收任意 SQL 表达式作为新增配置。
	config.Params = map[string]string{
		"collation_connection":  quoteSystemValue(endpoint.Collation),
		"time_zone":             quoteSystemValue(endpoint.Session.TimeZone),
		"transaction_isolation": quoteSystemValue(endpoint.Session.TransactionIsolation),
		"autocommit":            strconv.Itoa(endpoint.Session.Autocommit),
		"sql_mode":              quoteSystemValue(endpoint.Session.SQLMode),
	}
	return config, nil
}

// quoteSystemValue 为已经过白名单校验的会话字符串添加 SQL 单引号。
// 这里的引号处理服务于驱动 Params 的 SET 表达式，不用于拼接业务 SQL 或替代参数绑定。
func quoteSystemValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// makeTLSConfig 创建本连接专用的 TLS 信任和服务端身份校验配置。
// Enabled=false 时不读取 CA 文件，适用于配置校验允许的本地开发例外。
// Enabled=true 时必须提供可解析的 CA，并保留 ServerName、证书链及有效期校验。
// 不设置 InsecureSkipVerify，也不允许 TLS 失败后退回明文。
func makeTLSConfig(config TLSConfig) (*tls.Config, error) {
	if !config.Enabled {
		return nil, nil
	}
	certificate, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read MySQL CA certificate: %w", err)
	}
	// 只信任该配置明确提供的 CA 集合，不依赖当前机器上可能不同的系统证书池。
	certificates := x509.NewCertPool()
	if !certificates.AppendCertsFromPEM(certificate) {
		return nil, errors.New("MySQL CA file does not contain a valid certificate")
	}
	// 允许值已由配置校验限定为 1.2/1.3，因此默认分支明确表示最低 TLS 1.2。
	minVersion := uint16(tls.VersionTLS12)
	if config.MinVersion == "1.3" {
		minVersion = tls.VersionTLS13
	}
	return &tls.Config{
		RootCAs:    certificates,
		ServerName: config.ServerName,
		MinVersion: minVersion,
	}, nil
}

// sessionQuery 检查应用池中的实际默认库与会话状态。
// 三个字符集分别对应客户端、连接解释和结果传输；Loc=UTC 不能替代这里的 MySQL 时区检查。
// 该查询只有一条 SQL，保持 MultiStatements=false 的业务连接边界。
const sessionQuery = `SELECT DATABASE(), @@SESSION.character_set_client, @@SESSION.character_set_connection, @@SESSION.character_set_results, @@SESSION.collation_connection, @@SESSION.time_zone, @@SESSION.transaction_isolation, @@SESSION.autocommit, @@SESSION.sql_mode`

// verifySession 在同一物理连接上检查会话参数及 TLS，避免混用不同池连接的检查结果。
// 启动抽查用于确认配置实际生效；后续新连接仍依赖 makeDriverConfig 的每连接初始化参数。
// 连接从本次检查中释放时回到连接池，连接池本身仍归 Manager 管理。
func verifySession(ctx context.Context, pool *sql.DB, endpoint EndpointConfig) error {
	connection, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire MySQL connection for session verification: %w", err)
	}
	defer connection.Close()
	// 未设置默认库时 DATABASE() 返回 SQL NULL，不能直接扫描到 string。
	// NullString 保留其空值状态，确保配置留空时实际也没有选中任何默认库。
	var database sql.NullString
	var clientCharset, connectionCharset, resultsCharset, collation, zone, isolation, mode string
	var autocommit int
	if err := connection.QueryRowContext(ctx, sessionQuery).Scan(&database, &clientCharset, &connectionCharset, &resultsCharset, &collation, &zone, &isolation, &autocommit, &mode); err != nil {
		return fmt.Errorf("verify MySQL session: %w", err)
	}
	// 空配置要求返回 SQL NULL；非空配置要求返回有效且一致的库名，不能把 NULL 当作普通字符串。
	if database.Valid != (endpoint.DataBase != "") || database.String != endpoint.DataBase {
		return errors.New("MySQL session database does not match configured value")
	}
	// 除 SQLMode 按集合比较外，其他字段要求与已校验配置一致。
	// 错误只标出字段，不把配置值或查询结果整段写入日志。
	checks := []struct {
		name, actual, expected string
	}{
		{"character_set_client", clientCharset, endpoint.Charset},
		{"character_set_connection", connectionCharset, endpoint.Charset},
		{"character_set_results", resultsCharset, endpoint.Charset},
		{"collation_connection", collation, endpoint.Collation},
		{"time_zone", zone, endpoint.Session.TimeZone},
		{"transaction_isolation", isolation, endpoint.Session.TransactionIsolation},
		{"autocommit", strconv.Itoa(autocommit), strconv.Itoa(endpoint.Session.Autocommit)},
		{"sql_mode", normalizedSQLMode(mode), normalizedSQLMode(endpoint.Session.SQLMode)},
	}
	for _, check := range checks {
		if check.actual != check.expected {
			return fmt.Errorf("MySQL session %s does not match configured value", check.name)
		}
	}
	// 本地明文例外不要求 TLS 会话状态，不能把它与“TLS 握手失败后回退”混为一谈。
	if !endpoint.TLS.Enabled {
		return nil
	}
	// 继续使用同一个 sql.Conn 检查加密套件与协商协议。
	// 单凭非空 cipher 不能证明证书身份可信，CA/ServerName 校验在握手 TLS 配置中执行。
	rows, err := connection.QueryContext(ctx, "SHOW SESSION STATUS WHERE Variable_name IN ('Ssl_cipher', 'Ssl_version')")
	if err != nil {
		return fmt.Errorf("verify MySQL TLS session: %w", err)
	}
	defer rows.Close()
	status := make(map[string]string, 2)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return fmt.Errorf("read MySQL TLS status: %w", err)
		}
		status[name] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read MySQL TLS status: %w", err)
	}
	// 缺行、空套件、过旧协议或低于配置最低版本的协商结果都应拒绝。
	version := status["Ssl_version"]
	if status["Ssl_cipher"] == "" || (version != "TLSv1.2" && version != "TLSv1.3") || (endpoint.TLS.MinVersion == "1.3" && version != "TLSv1.3") {
		return errors.New("MySQL TLS session does not meet configured requirements")
	}
	return nil
}

// normalizedSQLMode 把 SQL 模式文本转换为可比较的规范集合。
// MySQL 返回顺序和字母大小写不必与配置文本一致；重复项也不改变模式含义。
// 本函数只负责比较，允许哪些模式以及是否缺少必需模式仍由配置校验负责。
func normalizedSQLMode(mode string) string {
	set := make(map[string]struct{})
	for _, part := range strings.Split(mode, ",") {
		set[strings.ToUpper(strings.TrimSpace(part))] = struct{}{}
	}
	parts := make([]string, 0, len(set))
	for part := range set {
		parts = append(parts, part)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
