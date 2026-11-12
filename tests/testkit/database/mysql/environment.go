// Package mysqltest 创建仅供测试使用的真实 MySQL 环境，不在包初始化时启动资源。
package mysqltest

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	appmysql "go-service-template/internal/database/mysql"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// DefaultImage 固定 MySQL 小版本，避免相同代码在不同时间拉取到不同版本。
	DefaultImage  = "mysql:8.4.4"
	startupBudget = 3 * time.Minute
	cleanupBudget = 90 * time.Second
	mysqlPort     = nat.Port("3306/tcp")
)

//go:embed entrypoint.sh
var entrypoint []byte

// Options 只描述本轮创建条件，不接受已有数据库的地址或数据卷，避免操作共享环境。
type Options struct {
	// RepositoryRoot 指向含 database/ 正式 SQL 的项目根目录；为空时从工作目录向上查找。
	RepositoryRoot string
	// Image 可替换为镜像仓库中的固定版本或 digest；空值使用 DefaultImage。
	Image string
	// StartupTimeout 覆盖拉取镜像、启动、建库和真实连接验证的总预算。
	StartupTimeout time.Duration
}

// Environment 拥有一个容器、扫描初始化的业务库和测试连接，调用方必须先结束应用工作再 Reset/Close。
// DB 返回的连接归本对象所有；外部应用通过 ConfigDir 另行创建的连接须由应用自行关闭。
type Environment struct {
	mu          sync.Mutex
	runID       string
	dir         string
	configDir   string
	closed      bool
	container   *testcontainers.DockerContainer
	network     *testcontainers.DockerNetwork
	admin       *appmysql.Manager
	connections *appmysql.Manager
	databases   map[string]string // 业务域目录名到实际库名，供配置和重置共用。
}

// Create 使用全新的空数据目录创建实例；任一阶段失败都会尝试用独立预算清理已创建资源。
func Create(ctx context.Context, options Options) (_ *Environment, resultErr error) {
	if ctx == nil {
		return nil, errors.New("MySQL test environment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.StartupTimeout < 0 {
		return nil, errors.New("MySQL startup timeout must not be negative")
	}
	if options.StartupTimeout == 0 {
		options.StartupTimeout = startupBudget
	}
	if options.Image == "" {
		options.Image = DefaultImage
	}
	if !regexp.MustCompile(`^\S+(?::8\.4\.[0-9]+|@sha256:[0-9a-fA-F]{64})$`).MatchString(options.Image) {
		return nil, errors.New("MySQL test image must use an exact 8.4.x tag or sha256 digest")
	}
	root, err := repositoryRoot(options.RepositoryRoot)
	if err != nil {
		return nil, err
	}
	// 在创建 Docker 资源前发现有建库脚本的业务域并读取 SQL。
	plan, err := readSchema(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, options.StartupTimeout)
	defer cancel()
	if err := checkDocker(ctx); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "go-service-testkit-mysql-")
	if err != nil {
		return nil, fmt.Errorf("create MySQL test directory: %w", err)
	}
	environment := &Environment{runID: filepath.Base(dir), dir: dir}
	defer func() {
		if resultErr != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupBudget)
			defer cleanupCancel()
			resultErr = errors.Join(resultErr, environment.Close(cleanupCtx))
		}
	}()
	if err := writeCertificates(dir); err != nil {
		return nil, err
	}
	rootPassword, err := randomPassword()
	if err != nil {
		return nil, err
	}
	appPassword, err := randomPassword()
	if err != nil {
		return nil, err
	}
	labels := map[string]string{"go-service-template.testkit.run": environment.runID}
	environment.network, err = network.New(ctx, network.WithLabels(labels))
	if err != nil {
		return nil, fmt.Errorf("create MySQL test network: %w", err)
	}
	files := []testcontainers.ContainerFile{
		{Reader: bytes.NewReader(mysqlConfig()), ContainerFilePath: "/etc/mysql/conf.d/testkit.cnf", FileMode: 0o644},
		{Reader: bytes.NewReader(entrypoint), ContainerFilePath: "/testkit-entrypoint.sh", FileMode: 0o700},
	}
	for _, name := range []string{"ca.pem", "server-cert.pem", "server-key.pem"} {
		mode := int64(0o644)
		if name == "server-key.pem" {
			mode = 0o600
		}
		files = append(files, testcontainers.ContainerFile{
			HostFilePath: filepath.Join(dir, name), ContainerFilePath: "/etc/mysql/testkit/" + name, FileMode: mode,
		})
	}
	environment.container, err = testcontainers.Run(ctx, options.Image,
		testcontainers.WithLabels(labels), network.WithNetwork([]string{"mysql"}, environment.network),
		testcontainers.WithEnv(map[string]string{"MYSQL_ROOT_PASSWORD": rootPassword, "MYSQL_ROOT_HOST": "%"}),
		testcontainers.WithFiles(files...), testcontainers.WithEntrypoint("/testkit-entrypoint.sh"),
		testcontainers.WithCmd("mysqld"), testcontainers.WithExposedPorts(string(mysqlPort)),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			// 数据留在容器的临时内存文件系统；不给 Docker 创建持久化数据卷。
			config.Tmpfs = map[string]string{"/var/lib/mysql": "rw,nosuid,nodev,size=1073741824"}
			config.Resources.Memory = 2 * 1024 * 1024 * 1024
			// 空 HostPort 由 Docker 分配；只向 Docker 主机的回环地址发布。
			config.PortBindings = nat.PortMap{mysqlPort: {{HostIP: "127.0.0.1"}}}
		}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort(mysqlPort).WithStartupTimeout(options.StartupTimeout)),
	)
	if err != nil {
		return nil, fmt.Errorf("start MySQL test container (%s): %w", environment.runID, err)
	}
	host, err := environment.container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve MySQL test host: %w", err)
	}
	port, err := environment.container.MappedPort(ctx, mysqlPort)
	if err != nil {
		return nil, fmt.Errorf("resolve MySQL test port: %w", err)
	}
	portNumber, err := strconv.Atoi(port.Port())
	if err != nil {
		return nil, fmt.Errorf("parse MySQL test port: %w", err)
	}
	// 管理连接也复用项目真实的 TLS、每连接会话初始化和就绪校验，不另拼一个弱校验 DSN。
	// 初始化前还不知道实际业务库名，因此管理连接不选择默认库，也不依赖某个业务连接。
	adminConfig := appmysql.Config{Connections: map[string]appmysql.ConnectionConfig{
		"admin": testConnection(dir, host, portNumber, "root", rootPassword, ""),
	}}
	environment.admin, err = appmysql.Open(ctx, adminConfig)
	if err != nil {
		return nil, fmt.Errorf("open MySQL initialization connection: %w", err)
	}
	adminDB, err := sqlPool(environment.admin, "admin")
	if err != nil {
		return nil, err
	}
	environment.databases, err = initializeSchema(ctx, adminDB, plan, appPassword)
	if err != nil {
		return nil, err
	}
	if err := verifyInstance(ctx, adminDB); err != nil {
		return nil, err
	}
	environment.configDir, err = writeAppConfig(dir, host, portNumber, appPassword, environment.databases)
	if err != nil {
		return nil, err
	}
	config, err := appmysql.LoadConfig(filepath.Join(environment.configDir, "database", "mysql"), "test")
	if err != nil {
		return nil, err
	}
	environment.connections, err = appmysql.Open(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("verify MySQL application connections: %w", err)
	}
	return environment, nil
}

// RunID 返回本轮资源标识，适合用于失败报告，不含连接密码。
func (e *Environment) RunID() string { return e.runID }

// ConfigDir 返回仅含 database/mysql/test.yml 的配置根目录，不是完整应用配置。
func (e *Environment) ConfigDir() string { return e.configDir }

// DB 按 database/<name>/mysql 中的业务域目录名返回 SQL 连接池，供前置数据和结果查询使用。
func (e *Environment) DB(name string) (*sql.DB, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.connections == nil {
		return nil, errors.New("MySQL test environment is not open")
	}
	return sqlPool(e.connections, name)
}

// Reset 查询本次初始化的业务库，清空其中的实际表，不重建容器或删除表结构。
// 调用前必须确认业务、事务和后台工作已经结束；本方法不会替应用完成排空。
// 重置不是跨表事务，任一步失败后不能继续把本环境当作干净状态使用。
func (e *Environment) Reset(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx == nil {
		return errors.New("MySQL reset requires a context")
	}
	if e.closed || e.admin == nil {
		return errors.New("MySQL test environment is not open")
	}
	pool, err := sqlPool(e.admin, "admin")
	if err != nil {
		return err
	}
	return resetSchema(ctx, pool, e.databases)
}

// Close 关闭本对象的连接后依次移除容器、网络及临时文件；重复调用可重试未清理的资源。
// 调用方必须先排空并停止使用本环境的应用进程，清理预算不能复用已超时的请求 Context。
func (e *Environment) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("MySQL cleanup requires a context")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	var failures []error
	if e.connections != nil {
		failures = append(failures, e.connections.Close())
		e.connections = nil
	}
	if e.admin != nil {
		failures = append(failures, e.admin.Close())
		e.admin = nil
	}
	if e.container != nil {
		if err := e.container.Terminate(ctx); !cleanupSucceeded(err) {
			failures = append(failures, fmt.Errorf("remove MySQL container for %s: %w", e.runID, err))
		} else {
			e.container = nil
		}
	}
	if e.network != nil && e.container == nil {
		if err := e.network.Remove(ctx); !cleanupSucceeded(err) {
			failures = append(failures, fmt.Errorf("remove MySQL network for %s: %w", e.runID, err))
		} else {
			e.network = nil
		}
	}
	if e.dir != "" && e.container == nil {
		if err := os.RemoveAll(e.dir); err != nil {
			failures = append(failures, fmt.Errorf("remove MySQL test files for %s: %w", e.runID, err))
		} else {
			e.dir = ""
		}
	}
	return errors.Join(failures...)
}

// checkDocker 把 Testcontainers 在 Docker 探测阶段的 panic 转为明确的环境错误。
// 只保护连接探测，后续测试或业务中的 panic 不在此吞掉。
func checkDocker(ctx context.Context) (resultErr error) {
	defer func() {
		if failure := recover(); failure != nil {
			resultErr = fmt.Errorf("Docker is unavailable for MySQL tests: %v", failure)
		}
	}()
	client, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("connect to Docker for MySQL tests: %w", err)
	}
	defer client.Close()
	if _, err := client.Ping(ctx); err != nil {
		return fmt.Errorf("Docker is unavailable for MySQL tests: %w", err)
	}
	return nil
}

func randomPassword() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate MySQL test password: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func sqlPool(manager *appmysql.Manager, name string) (*sql.DB, error) {
	db, err := manager.Get(name)
	if err != nil {
		return nil, err
	}
	return db.DB()
}

// cleanupSucceeded 允许 Ryuk 已经删除资源，但不能让 Join 中的 NotFound 掩盖其他清理错误。
func cleanupSucceeded(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !cleanupSucceeded(child) {
				return false
			}
		}
		return true
	}
	// Docker 的 NotFound 标记可能自身包装其他原因，必须先识别标记再展开单错误链。
	if _, ok := err.(interface{ NotFound() }); ok {
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		cause := wrapped.Unwrap()
		return cause != nil && cleanupSucceeded(cause)
	}
	return false
}
