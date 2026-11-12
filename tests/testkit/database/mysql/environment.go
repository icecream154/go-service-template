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
	mu          sync.Mutex                      // 串行化 DB、Reset 和 Close，保护关闭状态和资源句柄。
	runID       string                          // 本轮环境的唯一标识，取自临时目录名，用于 Docker 标签和错误定位。
	dir         string                          // 存放 TLS 证书、私钥和生成配置的临时目录，由 Close 清理。
	configDir   string                          // 被测应用读取 database/mysql/test.yml 时使用的配置根目录。
	closed      bool                            // Close 开始清理时置为 true，后续 DB 和 Reset 会返回错误。
	container   *testcontainers.DockerContainer // 运行本轮 MySQL 实例的容器，由 Close 终止。
	network     *testcontainers.DockerNetwork   // 本轮容器使用的独立 Docker 网络，由 Close 移除。
	admin       *appmysql.Manager               // root 账号的连接管理器，用于初始化业务库、校验实例配置和重置数据。
	connections *appmysql.Manager               // 测试应用账号的连接管理器，供 DB 按业务域取连接池。
	databases   map[string]string               // 业务域目录名到实际库名，供配置和重置共用。
}

// Create 根据 options 扫描项目 SQL，创建独立 MySQL 容器，完成建库建表、配置生成和连接验证。
// 成功时返回可供用例读写的 Environment，其资源由调用方在用例结束后调用 Close 释放。
// 未发现符合条件的 MySQL 业务域时返回 (nil, nil)，不访问 Docker 或创建临时资源。
// 调用方须先检查错误，再检查返回的环境是否为 nil，以决定是否装配 MySQL 依赖。
// 创建失败时返回 nil 和错误，并尝试用独立的清理时限回收已创建的资源；清理错误会合并返回。
//
// 扫描完 SQL 后，Create 从调用方的 ctx 派生一个 Context，供 Docker 和 MySQL 初始化使用。
// 派生 Context 继承调用方的取消信号，并从此时起应用 StartupTimeout（默认 3 分钟）。
// 如果调用方也设有截止时间，以两者中较早到期的为准。
func Create(ctx context.Context, options Options) (_ *Environment, resultErr error) {
	// 调用方必须提供父 Context，供后续启动操作继承取消信号和截止时间。
	if ctx == nil {
		return nil, errors.New("MySQL test environment requires a context")
	}

	// 调用方已取消或超时时，直接返回对应的错误，避免继续扫描 SQL 或访问 Docker。
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 负的启动时限会使派生 Context 立即过期，按无效参数处理。
	if options.StartupTimeout < 0 {
		return nil, errors.New("MySQL startup timeout must not be negative")
	}

	// 零值表示未设置启动时限，使用默认的 3 分钟。
	if options.StartupTimeout == 0 {
		options.StartupTimeout = startupBudget
	}

	// 未指定镜像时，使用项目固定的 MySQL 小版本。
	if options.Image == "" {
		options.Image = DefaultImage
	}

	// 自定义镜像也须通过精确的 8.4.x 标签或 SHA-256 digest 指定版本。
	if !regexp.MustCompile(`^\S+(?::8\.4\.[0-9]+|@sha256:[0-9a-fA-F]{64})$`).MatchString(options.Image) {
		return nil, errors.New("MySQL test image must use an exact 8.4.x tag or sha256 digest")
	}

	// 定位正式 SQL 所在的仓库。未设置 RepositoryRoot 时，从工作目录向上查找本项目的 go.mod。
	root, err := repositoryRoot(options.RepositoryRoot)
	if err != nil {
		return nil, err
	}

	// 在创建 Docker 资源前发现有建库脚本的业务域并读取 SQL。
	plan, err := readSchema(root)
	if err != nil {
		return nil, err
	}
	// 文件扫描不接收 Context；扫描结束后再次检查取消状态，避免继续初始化已取消的请求。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 没有声明 MySQL 依赖时正常结束；此分支不探测 Docker，也不生成证书、配置或容器。
	if len(plan.databases) == 0 {
		return nil, nil
	}

	// 从此处开始限制 Docker 探测、镜像拉取、容器启动和数据库初始化的总时间。
	// 返回时释放计时器；Context 到期不会自动销毁已创建的容器，仍需显式清理。
	ctx, cancel := context.WithTimeout(ctx, options.StartupTimeout)
	defer cancel()

	// 先确认 Docker 可连接，避免在依赖不可用时生成证书或创建临时目录。
	if err := checkDocker(ctx); err != nil {
		return nil, err
	}

	// 临时目录存放本轮证书和配置，目录名同时用作资源标识；MySQL 数据保存在容器的 tmpfs。第一个参数 "" 表示使用 os.TempDir()。
	dir, err := os.MkdirTemp("", "go-service-testkit-mysql-")
	if err != nil {
		return nil, fmt.Errorf("create MySQL test directory: %w", err)
	}
	environment := &Environment{runID: filepath.Base(dir), dir: dir}

	// 取得首个资源后立即登记失败清理。命名返回值 resultErr 让 defer 能判断创建是否失败。
	// 使用独立的 Background Context，保证启动 Context 已取消时仍能尝试回收资源。
	defer func() {
		if resultErr != nil {
			// 给清理工作单独设置 cleanupBudget 的超时时间，不受之前操作已经超时的影响。
			// cleanupCtx 把超时通知传给清理函数；cleanupCancel 可以提前发出停止通知。
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupBudget)
			// 等下面的清理操作结束后，再调用 cleanupCancel，释放计时器等资源。
			defer cleanupCancel()
			// 调用 Close 清理资源，再把创建错误和清理错误合并到 resultErr，供 Create 返回。
			// 如果清理成功，Close 返回 nil，合并后只保留原来的创建错误。
			resultErr = errors.Join(resultErr, environment.Close(cleanupCtx))
		}
	}()

	// 每轮生成独立的测试 CA、服务端证书及管理账号、应用账号密码，不复用部署环境凭证。
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

	// 建立本轮容器使用的独立网络，并附上运行标识，便于按归属查找和清理资源。
	environment.network, err = network.New(ctx, network.WithLabels(labels))
	if err != nil {
		return nil, fmt.Errorf("create MySQL test network: %w", err)
	}

	// 准备文件清单，后续通过 WithFiles 交给 Testcontainers，在容器创建后、启动前复制进去。
	files := []testcontainers.ContainerFile{
		// MySQL 实例的服务端配置。
		{
			// 把 mysqlConfig 生成的配置字节包装为 io.Reader，供 Testcontainers 读取内容。
			Reader: bytes.NewReader(mysqlConfig()),
			// 放到镜像的 MySQL 配置目录，mysqld 在启动时加载这个 .cnf 文件。
			ContainerFilePath: "/etc/mysql/conf.d/testkit.cnf",
			// 0o644 允许属主读写，属组和其他用户只读，容器内的 MySQL 用户因此可以读取配置。
			FileMode: 0o644,
		},
		// 自定义入口脚本先设置服务端私钥的属主和权限，再调用镜像自带的 MySQL 入口。
		{
			// entrypoint 保存编译时通过 go:embed 嵌入程序的 entrypoint.sh 内容。
			Reader: bytes.NewReader(entrypoint),
			// 放在容器根目录，与下方 WithEntrypoint 指定的路径对应。
			ContainerFilePath: "/testkit-entrypoint.sh",
			// 0o700 仅允许属主读、写和执行。执行位使 Docker 可以直接运行这个入口脚本。
			FileMode: 0o700,
		},
	}

	// 按文件名遍历 writeCertificates 生成的 PEM 文件，依次处理 CA 证书、服务端证书和配套私钥。
	for _, name := range []string{"ca.pem", "server-cert.pem", "server-key.pem"} {
		// 两份证书沿用 0o644，供容器内的 MySQL 用户读取。FileMode 字段使用 int64。
		mode := int64(0o644)
		// 仅在处理服务端私钥时收紧权限。
		if name == "server-key.pem" {
			// 0o600 仅允许属主读写。入口脚本再把属主和属组改为 mysql，使 mysqld 能读取私钥。
			mode = 0o600
		}

		// 把当前 PEM 文件追加到同一个清单，记录它的复制来源、目标和权限。
		files = append(files, testcontainers.ContainerFile{
			// dir 是本轮宿主机临时目录，拼接文件名得到 Testcontainers 要读取的本地路径。
			HostFilePath: filepath.Join(dir, name),
			// 在容器内保留原文件名，复制到 testkit.cnf 引用的 TLS 文件目录。
			ContainerFilePath: "/etc/mysql/testkit/" + name,
			// 将上面选定的权限应用到容器内的文件副本。
			FileMode: mode,
		})
	}

	// 启动固定版本的 MySQL，注入本轮配置、证书和 root 密码，并等待数据库端口可以连接。
	// 端口就绪只是初步检查，后续还要验证数据库认证、TLS、会话及库表初始化。
	environment.container, err = testcontainers.Run(ctx, options.Image,
		testcontainers.WithLabels(labels), network.WithNetwork([]string{"mysql"}, environment.network),
		testcontainers.WithEnv(map[string]string{"MYSQL_ROOT_PASSWORD": rootPassword, "MYSQL_ROOT_HOST": "%"}),
		testcontainers.WithFiles(files...), testcontainers.WithEntrypoint("/testkit-entrypoint.sh"),
		testcontainers.WithCmd("mysqld"), testcontainers.WithExposedPorts(string(mysqlPort)),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			// 数据留在容器的临时内存文件系统；不给 Docker 创建持久化数据卷。
			config.Tmpfs = map[string]string{"/var/lib/mysql": "rw,nosuid,nodev,size=1073741824"}
			// 限制整个容器的内存使用，tmpfs 中的数据也会占用内存。
			config.Resources.Memory = 2 * 1024 * 1024 * 1024
			// mysqlPort 是 MySQL 在容器内使用的 3306/tcp。
			// HostIP 将映射端口绑定到 Docker 宿主机的回环地址 127.0.0.1。
			// HostPort 留空，由 Docker 分配可用的宿主机端口，避免多个测试实例争用固定端口。
			// 本机 Go 测试连接 127.0.0.1 上的这个端口，Docker 将 TCP 连接转发到容器内的 3306。
			config.PortBindings = nat.PortMap{mysqlPort: {{HostIP: "127.0.0.1"}}}
		}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort(mysqlPort).WithStartupTimeout(options.StartupTimeout)),
	)
	if err != nil {
		return nil, fmt.Errorf("start MySQL test container (%s): %w", environment.runID, err)
	}

	// environment.container 是上面 Run 返回的容器句柄。接下来的两个查询沿用本轮启动的 ctx。
	// Host 根据 Testcontainers 的 Docker 连接配置解析发布端口所在的主机名或 IP。
	// Go 测试在宿主机上使用本地 Docker 套接字时，默认得到 localhost。
	host, err := environment.container.Host(ctx)
	if err != nil {
		// 无法确定主机地址时结束初始化，并保留 Testcontainers 返回的错误供定位。
		return nil, fmt.Errorf("resolve MySQL test host: %w", err)
	}

	// MappedPort 查询 Docker 记录的容器端口映射，用 mysqlPort（3306/tcp）找到实际分配的宿主机端口。
	// port 是包含端口号和协议的 nat.Port，例如 49173/tcp。
	port, err := environment.container.MappedPort(ctx, mysqlPort)
	if err != nil {
		// 容器信息读取失败或找不到端口映射时，报告端口查询错误并结束初始化。
		return nil, fmt.Errorf("resolve MySQL test port: %w", err)
	}

	// port.Port() 取出端口号文本，例如 "49173"。strconv.Atoi 将它转为连接配置要求的 int。
	// host 和 portNumber 一起确定 MySQL 的 TCP 连接地址，供后面的管理连接和应用配置共用。
	portNumber, err := strconv.Atoi(port.Port())
	if err != nil {
		// 端口文本无法转成整数时返回解析错误，由 Create 已登记的失败清理流程回收资源。
		return nil, fmt.Errorf("parse MySQL test port: %w", err)
	}

	// 用项目的 MySQL 配置描述本轮管理连接，沿用 testConnection 提供的 TLS、会话和连接池设置。
	adminConfig := appmysql.Config{
		// Connections 按名称登记连接配置，供 Open 创建对应的命名连接。
		Connections: map[string]appmysql.ConnectionConfig{
			// "admin" 是程序内的连接名，下面的 sqlPool 用这个名字从管理器中查找连接。
			"admin": testConnection(
				// dir 是 os.MkdirTemp 创建的临时目录，里面的 ca.pem 由 writeCertificates 生成。
				// testConnection 将 CA 路径写入 TLS 配置，供 Open 建连时校验服务端证书。
				dir,
				// host 来自上面的 Host(ctx)，是 Docker 发布 MySQL 端口的主机地址。
				host,
				// portNumber 来自 MappedPort 查询结果，转为 int 后用作 TCP 连接的宿主机端口。
				portNumber,
				// root 是容器初始化时配置的 MySQL 管理员账号，用于建库、建表、创建 test_app 并授权。
				"root",
				// rootPassword 由上面的 randomPassword 为本轮生成，启动容器时通过 MYSQL_ROOT_PASSWORD 传给镜像。
				rootPassword,
				// 业务库要等 initializeSchema 执行建库 SQL 后才存在。传空串让连接登录时不选择默认库。
				// 建库后，initializeSchema 会先用 USE 选中对应的库，再执行该业务域的建表脚本。
				"",
			),
		},
	}

	// Open 校验 adminConfig，并沿用本轮启动的 ctx 建立连接、Ping MySQL 和检查会话参数。
	// 返回的 *appmysql.Manager 保存在 environment.admin 中，其连接池由 Environment.Close 统一释放。
	environment.admin, err = appmysql.Open(ctx, adminConfig)
	if err != nil {
		// 标记管理连接的初始化阶段并保留底层错误，由 Create 已登记的失败清理流程回收资源。
		return nil, fmt.Errorf("open MySQL initialization connection: %w", err)
	}

	// sqlPool 从管理器按 "admin" 取得 GORM 入口，再通过 DB() 取出 Open 创建的同一个 *sql.DB 连接池。
	// initializeSchema 和 verifyInstance 接收标准库的 *sql.DB，用它执行初始化 SQL 和查询实例设置。
	adminDB, err := sqlPool(environment.admin, "admin")
	if err != nil {
		// 查找命名连接或取得底层连接池失败时，保留原错误并结束本轮初始化。
		return nil, err
	}

	// 执行建库和建表脚本，再创建 test_app 并授予它对业务库的读写权限。
	// environment.databases 的键是业务域目录名，也是应用连接名。值是比较建库前后库列表得到的实际库名。
	// writeAppConfig 据此给每个应用连接指定默认库。Reset 按实际库名查询各库的 BASE TABLE 并清空数据。
	environment.databases, err = initializeSchema(
		// Create 用 StartupTimeout 从调用方上下文派生的 ctx，控制初始化 SQL 的取消和超时。
		ctx,
		// 上面通过 sqlPool 取得的 root 连接池，具备建库、建表、创建账号和授权所需的权限。
		adminDB,
		// readSchema(root) 扫描仓库的 database/<业务域>/mysql 得到的计划，包含建库 SQL 和其余脚本。
		plan,
		// 上面用 randomPassword 生成的本轮应用密码，创建 test_app 时设置，随后写入应用连接配置。
		appPassword,
	)
	if err != nil {
		// 将初始化错误传给调用方，由 Create 已登记的失败清理流程回收本轮资源。
		return nil, err
	}

	// 生成应用配置前，检查初始化结束时的实例状态。用例依赖项目约定的字符串比较、时间处理和事务行为。
	// verifyInstance 查询 VERSION() 和 @@GLOBAL 变量，确认版本、字符集、排序规则、时区、SQL 模式、
	// 事务隔离级别、安全传输和日志等实际生效的设置符合项目基线。
	if err := verifyInstance(
		// 实例校验沿用同一个启动 ctx，查询耗时计入本轮启动的总时限。
		ctx,
		// 复用执行初始化 SQL 的管理连接池，从刚建好库表的实例读取版本和全局变量。
		adminDB,
	); err != nil {
		// 查询失败或实际设置偏离基线时，返回校验错误并触发失败清理。
		return nil, err
	}

	// 将本轮应用连接写入临时目录下的 config/database/mysql/test.yml。
	// 返回的 config 目录绝对路径存入 environment.configDir，供调用方通过 ConfigDir 定位配置文件。
	environment.configDir, err = writeAppConfig(
		// dir 是 os.MkdirTemp 创建的临时目录，既用于定位 writeCertificates 写入的 ca.pem，也用于存放生成的配置。
		dir,
		// host 来自容器的 Host(ctx)，是测试进程访问 Docker 发布端口时使用的主机地址。
		host,
		// portNumber 是 MappedPort 查到并转成 int 的宿主机端口，Docker 将该端口转发到容器内的 3306。
		portNumber,
		// appPassword 由 randomPassword 为本轮生成，initializeSchema 已用它创建 test_app。
		appPassword,
		// initializeSchema 返回的连接名到实际库名的映射，用于填充 Connections 的键和各连接的默认库。
		environment.databases,
	)
	if err != nil {
		// 配置生成或写入失败时保留原错误，由 Create 已登记的失败清理流程回收本轮资源。
		return nil, err
	}

	// writeAppConfig 在写入前校验内存配置。这里通过项目提供给应用的加载器从磁盘读回 test.yml，
	// 确认文件能按约定路径找到、能解码为 Gormv2.Mysql 配置，并能通过 test 环境的校验。
	config, err := appmysql.LoadConfig(
		// 在 writeAppConfig 返回的配置根目录下定位直接存放 test.yml 的 database/mysql 子目录。
		filepath.Join(environment.configDir, "database", "mysql"),
		// test 是本轮配置的环境名，用来选择 test.yml 和该环境的校验规则。
		"test",
	)
	if err != nil {
		// 文件读取、YAML 解码或配置校验失败时，返回加载器的错误并触发失败清理。
		return nil, err
	}

	// 为每个业务连接用 test_app 实际建连并执行 Ping，验证映射地址可达、应用账号能认证、服务端证书通过 TLS 校验。
	// 随后检查实际选中的默认库、字符集、时区、事务等会话设置，以及协商的 TLS 版本和加密状态。
	// Open 返回的管理器保存在 environment.connections 中，供 DB 按连接名查找，由 Close 统一释放。
	environment.connections, err = appmysql.Open(
		// ctx 是 Create 派生的启动上下文，各连接的建连、Ping 和会话检查共用剩余启动时限。
		ctx,
		// config 来自上面重新加载的 test.yml，包含各连接的地址、应用账号、默认库、TLS 和会话设置。
		config,
	)
	if err != nil {
		// 任一连接失败时，Open 会关闭已创建的业务连接池。保留具体错误，交给 Create 清理其余资源。
		return nil, fmt.Errorf("verify MySQL application connections: %w", err)
	}

	// 所有应用连接通过真实建连和会话检查后，才把已就绪的环境交给测试用例。
	return environment, nil
}

// RunID 返回创建时分配的资源标识，可用于测试日志和清理故障定位，不包含账号密码。
// 环境关闭后该标识仍保留，便于关联本轮资源和错误记录。
func (e *Environment) RunID() string {
	// 返回创建时保存的标识，不重新查询 Docker，也不生成新的标识。
	return e.runID
}

// ConfigDir 返回测试数据库配置的根目录路径，其中包含 database/mysql/test.yml。
// 启动完整应用所需的其他配置由调用方准备；Close 清理后，不应继续使用该目录。
func (e *Environment) ConfigDir() string {
	// 返回已有配置的位置，不创建或复制文件；调用方通过该路径加载本轮 MySQL 配置。
	return e.configDir
}

// DB 按业务域连接名返回本轮 test_app 的 SQL 连接池，供用例准备数据和查询结果。
// name 是 database/<name>/mysql 中的业务域目录名，对应生成的 test.yml 中的连接名。
// 连接池由 Environment.Close 统一关闭。调用方须在 Reset 或 Close 前结束通过该池发起的业务操作。
// 环境不可用、连接名不存在或底层连接池获取失败时返回错误。
func (e *Environment) DB(name string) (*sql.DB, error) {
	// 与 Reset、Close 使用同一把锁，保护从检查 e.closed 到从 e.connections 取池的整个过程。
	// Close 取得这把锁后才能关闭业务连接管理器并清空句柄。
	e.mu.Lock()
	// 返回连接池或错误时都释放锁。用例随后通过该池执行的 SQL 在环境锁之外运行。
	defer e.mu.Unlock()

	// 检查 Close 设置的关闭标记，以及 Create 是否已保存业务连接管理器。
	if e.closed || e.connections == nil {
		// 环境尚未就绪或已进入关闭流程时，返回环境不可用的错误。
		return nil, errors.New("MySQL test environment is not open")
	}

	// sqlPool 用 Manager.Get 查找已打开的 GORM 连接，再用 DB() 取出它已有的 *sql.DB。
	// 查找或取池出错时，直接将错误返回给调用方。
	return sqlPool(
		// Create 读取生成的应用配置后，用 appmysql.Open 建立并保存的 test_app 连接管理器。
		e.connections,
		// 将调用方传入的业务域名作为管理器中的查找键。
		name,
	)
}

// Reset 复用当前 MySQL 实例，清空本轮初始化的业务库中现有的基础表数据。
// ctx 由调用方提供且须为非 nil，用于控制表清单查询与清表操作的取消和超时。
// 通过已取得的连接池执行的 SQL 在环境锁之外运行，调用前须等业务请求、事务和后台工作结束。
// 清空逐表生效。返回错误时可能只完成了部分清表，调用方应结束该环境的后续用例。
func (e *Environment) Reset(ctx context.Context) error {
	// 从状态检查、取管理池到逐表清空都持有 e.mu，期间 DB 和另一次 Reset 会等待。
	// Close 也要取得这把锁才能关闭管理池或移除容器。
	e.mu.Lock()
	// defer 保证成功或出错返回时都释放环境锁。
	defer e.mu.Unlock()

	// 查询表清单和执行 TRUNCATE 都要传入 ctx，先确认调用方提供了上下文。
	if ctx == nil {
		// 在访问数据库前返回参数错误。
		return errors.New("MySQL reset requires a context")
	}

	// 在锁内检查关闭标记和管理器句柄，确认当前环境允许继续取池。
	if e.closed || e.admin == nil {
		// 关闭标记已设置或缺少管理器时，报告环境不可用。
		return errors.New("MySQL test environment is not open")
	}

	// MySQL 的 TRUNCATE TABLE 需要 DROP 权限。test_app 的授权范围是 SELECT、INSERT、UPDATE 和 DELETE。
	// 因此清表复用本轮实例的 root 管理连接池。
	pool, err := sqlPool(
		// e.admin 来自 Create 使用 root 配置调用 appmysql.Open 的结果。
		e.admin,
		// admin 是当时登记的管理连接名，sqlPool 据此取得该连接的底层池。
		"admin",
	)
	if err != nil {
		// 查找管理连接或取得底层池失败时，直接返回原错误。
		return err
	}

	// resetSchema 按映射中的实际库名查询 information_schema.TABLES，只选各库当前的 BASE TABLE。
	// 逐张用完整库表名执行 TRUNCATE TABLE。查询或清空失败时，将错误直接传给调用方。
	return resetSchema(
		// 调用方传入的 ctx 贯穿表清单查询和每条清表语句。
		ctx,
		// 上面取出的 root 连接池，负责读取表清单和执行 TRUNCATE。
		pool,
		// initializeSchema 返回的业务域连接名到实际库名映射，映射的值确定清表范围。
		e.databases,
	)
}

// Close 关闭本对象的连接后依次移除容器、网络及临时文件；重复调用可重试未清理的资源。
// ctx 控制 Docker 清理请求的取消和截止时间，不能为 nil；建议使用独立且有超时的 Context。
// 调用方须先等待业务工作结束、停止应用，并关闭应用自行创建的连接，不能复用已超时的请求 Context。
// 清理失败时合并返回各阶段错误；已处理的连接管理器不会重复关闭，未删除的容器、网络和目录可重试。
func (e *Environment) Close(ctx context.Context) error {
	// 拒绝无效 Context，尚未改变环境状态，调用方可以补充 Context 后重新清理。
	if ctx == nil {
		return errors.New("MySQL cleanup requires a context")
	}

	// 串行执行关闭并立即标记为已关闭：即使后续删除失败，也不再允许 DB 或 Reset。
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	var failures []error

	// 先关闭本对象持有的业务连接和管理连接，收集错误后继续尝试释放 Docker 资源。
	// 连接管理器的 Close 不接收 Context，这一步不受传入 ctx 的超时控制。
	if e.connections != nil {
		failures = append(failures, e.connections.Close())
		e.connections = nil
	}
	if e.admin != nil {
		failures = append(failures, e.admin.Close())
		e.admin = nil
	}

	// 容器删除成功或确认已不存在时清除句柄；其余错误保留句柄，供下次 Close 重试。
	if e.container != nil {
		if err := e.container.Terminate(ctx); !cleanupSucceeded(err) {
			failures = append(failures, fmt.Errorf("remove MySQL container for %s: %w", e.runID, err))
		} else {
			e.container = nil
		}
	}

	// 只有容器已经移除，才尝试删除其网络；网络删除失败也保留句柄。
	if e.network != nil && e.container == nil {
		if err := e.network.Remove(ctx); !cleanupSucceeded(err) {
			failures = append(failures, fmt.Errorf("remove MySQL network for %s: %w", e.runID, err))
		} else {
			e.network = nil
		}
	}

	// 容器移除后即可删除本轮证书和配置，不要求网络删除也已成功。
	// 本地文件删除不受 ctx 控制；失败时保留目录路径，供下次 Close 重试。
	if e.dir != "" && e.container == nil {
		if err := os.RemoveAll(e.dir); err != nil {
			failures = append(failures, fmt.Errorf("remove MySQL test files for %s: %w", e.runID, err))
		} else {
			e.dir = ""
		}
	}

	// 无错误时 Join 返回 nil；有多个错误时一并保留，不用后一个错误覆盖前一个。
	return errors.Join(failures...)
}

// checkDocker 使用 Testcontainers 创建临时 Docker 客户端，并通过 Ping 确认守护进程可访问。
// ctx 由 Create 传入，控制客户端探测和 Ping 的取消、超时；本方法不创建容器。
// Testcontainers 在探测 Docker 地址时可能 panic，此处将其转为错误，供 Create 按正常失败流程返回。
// recover 只作用于本方法当前 goroutine 的调用过程，不捕获后续容器初始化、测试或业务代码中的 panic。
func checkDocker(ctx context.Context) (resultErr error) {
	// 在调用 Testcontainers 前登记 recover；命名返回值允许 defer 写入转换后的错误。
	defer func() {
		if failure := recover(); failure != nil {
			resultErr = fmt.Errorf("Docker is unavailable for MySQL tests: %v", failure)
		}
	}()

	// 由 Testcontainers 按当前运行环境解析 Docker 连接配置，创建本次探测使用的客户端。
	client, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return fmt.Errorf("connect to Docker for MySQL tests: %w", err)
	}

	// 方法结束时释放探测客户端的连接资源，不停止 Docker；此处不处理客户端关闭返回的错误。
	defer client.Close()

	// 客户端创建成功并不代表服务可达，需要实际发送 Ping；失败时保留原始错误供调用方判断。
	if _, err := client.Ping(ctx); err != nil {
		return fmt.Errorf("Docker is unavailable for MySQL tests: %w", err)
	}
	return nil
}

// randomPassword 读取 24 字节密码学随机数，生成 48 个十六进制字符组成的测试密码。
// 随机源读取失败时返回错误，不回退到固定密码；每个账号分别调用本方法生成密码。
func randomPassword() (string, error) {
	// 使用系统提供的密码学随机源，不使用面向模拟计算的普通伪随机数生成器。
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate MySQL test password: %w", err)
	}
	// 转为只包含 0-9 和 a-f 的文本，供账号创建 SQL 和连接配置使用。
	return hex.EncodeToString(value[:]), nil
}

// sqlPool 按 name 从项目数据库管理器取得 GORM 连接，并返回其底层 *sql.DB 连接池。
// manager 必须已创建；名称不存在、管理器已关闭或底层连接无法取得时返回错误。
// 本方法不创建新池、不转移资源所有权，返回的池仍由传入的管理器负责关闭。
func sqlPool(manager *appmysql.Manager, name string) (*sql.DB, error) {
	// 通过管理器查找命名连接，沿用其名称检查和关闭状态检查。
	db, err := manager.Get(name)
	if err != nil {
		return nil, err
	}
	// 从已有的 GORM 连接取得标准库连接池，供直接执行 SQL、准备数据和独立断言使用。
	return db.DB()
}

// cleanupSucceeded 判断一次 Docker 资源清理能否视为完成，本方法本身不删除资源。
// 没有错误或错误明确表示资源已不存在时返回 true；后者可能由 Ryuk 提前回收资源造成。
// 包装错误会递归检查原因，多个合并错误必须全部表示完成，不能用 NotFound 掩盖其他失败。
func cleanupSucceeded(err error) bool {
	// 没有错误表示本次清理已经成功。
	if err == nil {
		return true
	}

	// errors.Join 等合并错误暴露多个原因；逐项判断，任何一个真实故障都需要报告或重试。
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

	// 对单层包装继续检查原始原因；非 nil 错误即使包装了 nil，也不能当作没有错误。
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		cause := wrapped.Unwrap()
		return cause != nil && cleanupSucceeded(cause)
	}

	// 不属于以上情况的错误保留为失败，例如权限不足、连接故障或操作超时。
	return false
}
