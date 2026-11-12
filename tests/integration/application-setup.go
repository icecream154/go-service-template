//go:build integration || business

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	mysqltest "go-service-template/tests/testkit/database/mysql"
)

// ApplicationOptions 描述本轮使用的 MySQL 环境、服务可执行文件和应用配置准备方式。
type ApplicationOptions struct {
	// MySQL 控制 SQL 扫描位置、容器镜像和数据库启动预算。
	MySQL mysqltest.Options
	// Executable 指向调用方预先用 integration_app 标签构建的 cmd/web 可执行文件。
	// 文件应匹配运行测试的宿主机系统与架构，启动器通过 exec 直接运行它。
	Executable string
	// PrepareConfig 在 MySQL 启动之后、服务进程启动之前调用。ctx 来自测试用例，configDir 是本轮临时配置根目录。
	// database/mysql/test.yml 已由 MySQL testkit 生成，包含本轮容器的地址、账号、默认库和 CA 路径，供服务直接读取。
	// 回调在同一根目录下补齐以下 test.yml 文件，并返回准备过程中遇到的错误。
	//
	//   - logger/test.yml 提供测试服务名称、日志级别和可选的本轮日志路径。
	//   - authn/jwt/test.yml 提供测试 issuer、audience、签名密钥和 Token 有效期。
	//   - snowflake/test.yml 提供本轮应用的 machine_id。
	//   - integrations/company/test.yml 将 python-service-template 指向本轮模拟端点，配置连接超时及 image_edit、image_gen 的请求超时。
	//
	// 公司服务模拟端点由调用方先启动。子进程使用启动器显式设置的环境变量，配置文件应写入本轮已经准备好的值。
	PrepareConfig func(ctx context.Context, configDir string) error
	// StartupTimeout 限制从准备启动服务进程到 HTTP 就绪的等待时间，零值取 30 秒。
	// 服务进程以相同的时长作为自身依赖初始化预算，MySQL 使用 MySQL 选项中的独立预算。
	StartupTimeout time.Duration
}

// Application 提供被测服务的 HTTP 地址、用于数据准备和断言的数据库环境，以及进程日志位置。
// 这些资源由 StartApplication 注册的测试清理回收。
type Application struct {
	BaseURL        string
	Database       *mysqltest.Environment
	ProcessLogPath string
}

// StartApplication 用 Testcontainers 创建本轮 MySQL 环境，补齐测试配置，再启动宿主机上的服务子进程。
// t 提供用例上下文、诊断目录和清理钩子。options 提供预构建的服务二进制与本轮配置准备回调。
// 成功时返回已经通过 HTTP 就绪检查的服务地址、MySQL 环境和日志路径。任一步失败都会终止当前用例。
// 测试结束时先让服务排空请求和内部工作、关闭它持有的数据库连接。确认进程退出后再回收 MySQL。
// 调用方可先登记模拟服务的清理。t.Cleanup 逆序执行，模拟端点可用到服务停机完成。
func StartApplication(t testing.TB, options ApplicationOptions) *Application {
	// 将启动失败定位到调用方用例，便于直接找到是哪一组测试环境启动失败。
	t.Helper()

	// 先校验启动参数，避免配置缺失或可执行文件路径错误时创建容器。
	if strings.TrimSpace(options.Executable) == "" || options.PrepareConfig == nil {
		t.Fatal("integration application requires an executable and a configuration preparer")
	}
	// 启动时会切换子进程工作目录，先按测试进程的工作目录将可执行文件路径转为绝对路径。
	executable, err := filepath.Abs(options.Executable)
	if err != nil {
		// 路径解析失败时报告发生错误的阶段和底层原因。
		t.Fatalf("resolve integration application executable: %v", err)
	}
	// 提前读取文件信息，用于确认文件存在并检查类型和执行位。
	info, err := os.Stat(executable)
	if err != nil {
		// 将文件不存在、权限不足等错误交给用例，方便检查传入的二进制路径。
		t.Fatalf("inspect integration application executable: %v", err)
	}
	// 启动入口要求普通文件具有执行权限，实际能否执行由随后 cmd.Start 判定。
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("integration application executable is not an executable file: %s", executable)
	}
	// 负值会使启动上下文立即过期，按无效的启动预算处理。
	if options.StartupTimeout < 0 {
		t.Fatal("integration application startup timeout must be nonnegative")
	}
	// 零值表示采用服务默认的 30 秒启动预算。
	if options.StartupTimeout == 0 {
		options.StartupTimeout = 30 * time.Second
	}
	// 先取得日志目录。临时目录的清理较早登记，因此会在服务与数据库清理之后执行。
	logDir := t.ArtifactDir()

	// 数据库初始化、配置准备和服务启动都继承用例的取消信号。
	ctx := t.Context()
	// Create 根据正式 SQL 初始化独立的 MySQL 容器，并生成应用使用的 test.yml。
	database, err := mysqltest.Create(ctx, options.MySQL)
	if err != nil {
		// 由 MySQL testkit 处理创建中途的资源清理，这里向用例报告数据库启动失败。
		t.Fatalf("create integration MySQL environment: %v", err)
	}
	// Create 在没有匹配的建库脚本时会返回 nil。本启动器需要数据库及其生成的配置。
	if database == nil {
		t.Fatal("integration application requires MySQL schema for its database configuration")
	}

	// process 在 cmd.Start 成功后赋值，更早阶段失败时清理仅涉及已经取得的数据库环境。
	var process *applicationProcess
	// 取得数据库后立即登记清理，覆盖后续配置失败、进程启动失败、HTTP 检查失败及用例结束。
	t.Cleanup(func() {
		// 子进程仍可能在处理请求或访问数据库。先停机，让它在 MySQL 仍可用时完成内部工作和自身连接池收尾。
		if process != nil {
			// stop 优先发送 SIGTERM 触发应用的正常停机流程，必要时强制终止并等待进程退出。
			if err := process.stop(); err != nil {
				// 停机异常会使测试失败，仍继续确认进程是否已退出，以决定能否安全回收数据库。
				t.Errorf("stop integration application (%s): %v", database.RunID(), err)
			}
			// 由负责 Wait 的协程确认子进程已经回收后，才可关闭 MySQL 的连接池、容器和网络。
			select {
			case <-process.exited:
				// 子进程的生命周期已经结束，可以继续清理数据库。
			default:
				// 仍无法确认退出时保留 MySQL，避免尚在运行的服务失去数据库依赖。
				t.Errorf("could not confirm application exit (%s), MySQL retained", database.RunID())
				return
			}
		}

		// t.Context 在 Cleanup 前已取消，从 Background 派生独立的 90 秒 MySQL 回收预算。
		closeCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		// 数据库清理返回时释放超时上下文的计时器。
		defer cancel()
		// 关闭 testkit 持有的连接并回收本轮容器、网络与临时目录。清理错误附上运行标识。
		if err := database.Close(closeCtx); err != nil {
			t.Errorf("close integration MySQL environment (%s): %v", database.RunID(), err)
		}
	})

	// 从 MySQL 环境取出生成的配置根目录，随后把同一个目录传给子进程的 -config-dir。
	configDir := database.ConfigDir()
	// 回调在保留生成的 MySQL 配置的基础上写入日志、测试 JWT、雪花和公司服务模拟端点的配置。
	if err := options.PrepareConfig(ctx, configDir); err != nil {
		// 配置准备失败时在启动服务前终止用例，已登记的清理负责回收数据库。
		t.Fatalf("prepare integration application configuration (%s): %v", database.RunID(), err)
	}
	// 应用启动和 HTTP 探测从此处开始计时，MySQL 与配置准备已完成。
	startupCtx, cancel := context.WithTimeout(ctx, options.StartupTimeout)
	// StartApplication 返回后释放启动计时器。服务子进程的停机由测试清理负责。
	defer cancel()
	// 配置准备结束后再检查用例的取消状态，避免在已结束的用例中启动进程。
	if err := startupCtx.Err(); err != nil {
		t.Fatalf("start integration application: %v", err)
	}

	// 保留私有进程日志用于定位启动失败，并从中取得 cmd/web 报告的实际监听地址。
	output, err := os.CreateTemp(logDir, "web-*.log")
	if err != nil {
		// 日志也用于发现动态端口。无法创建时，在启动子进程前结束用例。
		t.Fatalf("create integration application log: %v", err)
	}
	// 保存日志路径供就绪检查读取，也在启动失败时用于定位诊断文件。
	logPath := output.Name()
	// 直接执行调用方传入的宿主机二进制。MySQL 容器已由前面的 mysqltest.Create 启动。
	cmd := exec.Command(executable,
		// 指定本轮应用配置根目录，服务按 test 环境读取其中的文件。
		"-config-dir", configDir,
		// 只在本机回环地址监听。端口 0 由操作系统分配实际端口，随后由 cmd/web 写入日志。
		"-addr", "127.0.0.1:0",
		// 将配置的启动预算也传给子进程，用于限制它自身的依赖初始化。
		"-startup-timeout", options.StartupTimeout.String(),
		// 服务接到停机信号后，用 10 秒的预算停止 HTTP 接入并等待内部请求结束。
		"-shutdown-timeout", "10s",
	)
	// 子进程从本轮配置目录启动，相对日志路径等也以该目录为基准。
	cmd.Dir = configDir
	// 仅向子进程传入这三个环境变量。test 选择 test.yml，测试装配标记要求使用 integration_app 构建。
	cmd.Env = []string{"APP_ENV=test", "APP_REGION=cn", "APP_INTEGRATION_TEST=1"}
	// 将标准输出和标准错误写入同一个日志，保留监听地址与启动诊断。
	cmd.Stdout, cmd.Stderr = output, output
	// 只启动进程，交由独立的 Wait 协程等待它退出。
	err = cmd.Start()
	// Start 返回后关闭父进程的文件句柄，成功启动的子进程继续通过自己的文件描述符写日志。
	_ = output.Close()
	// 进程启动失败时保留 RunID 和底层错误，清理流程会回收数据库。
	if err != nil {
		t.Fatalf("start integration application (%s): %v", database.RunID(), err)
	}

	// Wait 只由这个协程调用。关闭通道后，检查和清理流程可以安全读取退出结果。
	process = &applicationProcess{cmd: cmd, exited: make(chan struct{})}
	go process.wait()

	// 确认服务真实 HTTP 响应符合就绪条件，再将端口和数据库交给用例。
	baseURL, err := process.waitForHTTP(startupCtx, logPath)
	if err != nil {
		// 报告失败阶段与日志位置，已登记的清理会先停止进程再回收数据库。
		t.Fatalf("wait for integration application (%s, log %s): %v", database.RunID(), logPath, err)
	}
	// 测试通过 BaseURL 发请求，通过 Database 准备数据与核对结果，ProcessLogPath 用于诊断。
	return &Application{BaseURL: baseURL, Database: database, ProcessLogPath: logPath}
}

type applicationProcess struct {
	cmd     *exec.Cmd
	exited  chan struct{}
	waitErr error // exited 关闭后可读。
}

// wait 等待已启动的子进程结束并回收其进程资源，记录 Wait 返回的退出结果。
// StartApplication 在 cmd.Start 成功后只启动一个协程调用此方法。就绪检查和清理通过 exited 获取退出通知。
func (p *applicationProcess) wait() {
	// 阻塞到进程退出，保存退出状态或等待过程中的错误。
	p.waitErr = p.cmd.Wait()
	// 先写入结果再关闭通道。收到退出通知的协程此时可以安全读取 waitErr。
	close(p.exited)
}

// waitForHTTP 从 logPath 对应的子进程日志读取动态分配的回环端口，再探测真实的 HTTP 入口。
// ctx 是 StartApplication 为应用启动和就绪检查派生的上下文，限制整个等待过程。
// 探测要求未匹配路由返回 HTTP 404 和 NOT_FOUND，并在成功时再次确认进程状态和启动预算。
// 就绪时返回可供用例发请求的基础 URL。进程提前退出、等待被取消或地址读取失败时返回相应错误。
func (p *applicationProcess) waitForHTTP(ctx context.Context, logPath string) (string, error) {
	// 建立专用探测客户端，直接连接本机回环地址，每次请求都受客户端超时和启动上下文约束。
	client := &http.Client{
		// 每次探测使用独立连接，单次请求最多等待 1 秒。
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// 保留原始重定向响应，交给探测逻辑按预期状态码判断。
			return http.ErrUseLastResponse
		},
	}
	// 每隔 50 毫秒产生一次检查机会。返回时停止计时器。
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	// 地址一旦从日志解析成功便用于后续探测。probeErr 记录最近一次探测失败的原因。
	var address string
	var probeErr error
	for {
		// 等待下一次检查机会，同时响应进程退出以及启动超时或用例取消。
		select {
		case <-p.exited:
			// Wait 已经发布退出结果，附上它帮助定位服务为何未能完成启动。
			return "", errors.Join(errors.New("application exited before HTTP readiness"), p.waitErr)
		case <-ctx.Done():
			// 返回取消或超时原因，并保留最近一次 HTTP 探测的错误。
			return "", errors.Join(ctx.Err(), probeErr)
		case <-ticker.C:
			// 到达本轮检查时间，继续获取端口或发送 HTTP 探测请求。
		}
		// 子进程使用端口 0 启动，首次探测前需要从监听日志取得实际端口。
		if address == "" {
			var err error
			address, err = readApplicationAddress(logPath)
			if err != nil {
				// 日志读取失败或地址不符合约定时，向用例返回具体错误。
				return "", err
			}
			if address == "" {
				// 当前日志还没有完整的监听记录，等下一轮继续读取。
				continue
			}
		}
		// 监听日志用于发现地址。实际发送 HTTP 请求，确认请求已由服务的路由入口处理。
		baseURL := "http://" + address
		probeErr = probeApplicationHTTP(ctx, client, baseURL)
		// 探测失败时留在循环中等待重试。成功时再检查服务是否恰好退出或启动上下文是否已结束。
		if probeErr == nil {
			select {
			case <-p.exited:
				// 进程在探测期间退出，返回该次启动的退出结果。
				return "", errors.Join(errors.New("application exited during HTTP readiness"), p.waitErr)
			default:
				// 进程尚未发布退出通知，只有 ctx 仍有效时用例才会接受该地址。
				return baseURL, ctx.Err()
			}
		}
	}
}

// readApplicationAddress 在 logPath 指向的子进程日志中查找 cmd/web 输出的监听记录。
// 成功时返回包含实际端口的 IPv4 回环地址。读到当前文件末尾仍未找到完整记录时返回空字符串和 nil，供调用方重试。
// 文件打开或读取失败，以及日志中的监听地址不符合约定时返回错误。
func readApplicationAddress(logPath string) (string, error) {
	// 每轮从头读取当前文件，确保下一轮能够读到子进程随后追加的完整日志。
	file, err := os.Open(logPath)
	if err != nil {
		// 打开失败时标记读取日志这一阶段，并保留文件系统错误。
		return "", fmt.Errorf("open application process log: %w", err)
	}
	// 本轮检查结束后关闭读取句柄。子进程仍通过自己的句柄写入日志。
	defer file.Close()
	// 逐行查找监听记录，只处理以换行结束的完整日志行。
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			// 子进程可能还在写最后一行，留待下一轮重新读取完整记录。
			return "", nil
		}
		if err != nil {
			// 文件读取错误会结束本轮启动检查，由调用方报告。
			return "", fmt.Errorf("read application process log: %w", err)
		}
		// 用 cmd/web 的固定日志标记找到实际监听地址，跳过其他启动日志。
		_, value, found := strings.Cut(line, "HTTP server listening: address=")
		if !found {
			continue
		}
		// 拆分日志中的主机和端口，确保探测目标是本机 IPv4 回环地址。
		address := strings.TrimSpace(value)
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			return "", errors.New("application did not report a loopback listener")
		}
		// 端口必须是已经分配的有效 TCP 端口，供后续构造 HTTP URL。
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("application reported an invalid listener port")
		}
		// 返回已校验的 host:port，HTTP 协议前缀由调用方添加。
		return address, nil
	}
}

// probeApplicationHTTP 使用 client 向 baseURL 上的未注册路径发起一次 GET，验证真实路由入口已能处理请求。
// ctx 约束请求和响应读取。探测使用无业务副作用的 NoRoute 路径，要求状态为 HTTP 404 且 JSON error.code 为 NOT_FOUND。
// 请求构造、传输或响应检查失败时返回错误，由 waitForHTTP 在剩余预算内重试。
func probeApplicationHTTP(ctx context.Context, client *http.Client, baseURL string) error {
	// 构造探测请求，继承应用启动上下文的取消信号和截止时间。
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/__integration_ready__", nil)
	if err != nil {
		// 构造请求失败时返回具体原因，调用方会将它记录为本轮探测错误。
		return err
	}
	// 实际连接回环端口并等待服务返回响应。
	response, err := client.Do(request)
	if err != nil {
		// 连接未就绪、超时等传输错误由外层等待循环处理。
		return err
	}
	// 及时释放本次响应的资源，后续探测可重新建立连接。
	defer response.Body.Close()
	// 只解码路由约定中的错误码字段。状态码与错误码共同表明请求到达了预期的 HTTP 入口。
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	// 先检查 HTTP 状态，再从最多 4096 字节的响应中检查 JSON 错误码。
	if response.StatusCode != http.StatusNotFound ||
		json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body) != nil || body.Error.Code != "NOT_FOUND" {
		// 诊断只报告状态码，响应正文仍留在探测内部。
		return fmt.Errorf("unexpected application readiness response (HTTP %d)", response.StatusCode)
	}
	// 服务已按约定完成本次探测请求。
	return nil
}

// stop 向子进程发送 SIGTERM，触发 cmd/web 停止接收请求并排空仍在执行的 HTTP 和内部工作。
// 给服务及其依赖收尾预留 25 秒。信号发送失败或超过预算时，强制终止并报告停机异常。
// 调用方根据 exited 通知确认进程资源已经回收，再清理服务使用的 MySQL 环境。
func (p *applicationProcess) stop() error {
	// 先查看退出通知。服务在预定清理前退出属于测试失败，需要保留 Wait 的结果。
	select {
	case <-p.exited:
		return errors.Join(errors.New("application exited before shutdown"), p.waitErr)
	default:
		// 尚未收到退出通知，继续向子进程发送停机信号。
	}
	// 如果进程恰好在发送信号前退出，ErrProcessDone 允许继续等待 Wait 发布结果。
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		// 发送信号失败时尝试强制终止，并合并两步的错误供调用方诊断。
		return errors.Join(fmt.Errorf("signal application shutdown: %w", err), p.killAndWait())
	}
	// 给子进程的 HTTP 排空、内部工作和后续依赖收尾留出时间。
	select {
	case <-p.exited:
		// 子进程已退出并被回收，返回服务实际的退出结果。
		return p.waitErr
	case <-time.After(25 * time.Second):
		// 优雅停机超时后强制终止。即使终止成功，也向测试报告原先的停机超时。
		return errors.Join(errors.New("application graceful shutdown timed out"), p.killAndWait())
	}
}

// killAndWait 在正常停机失败时强制结束子进程，并给负责 Wait 的协程最多 5 秒确认进程退出。
// 成功收到退出通知时返回 nil。发送终止信号失败或超时仍无法确认退出时返回错误。
func (p *applicationProcess) killAndWait() error {
	// 强制终止仍在运行的进程。进程已结束时同样继续等待退出通知，以确认资源回收完成。
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		// 保留操作系统返回的终止错误，清理流程据退出通知决定后续动作。
		return fmt.Errorf("kill integration application: %w", err)
	}
	// 由 Wait 协程确认子进程被回收，等待本身也有明确的时间上限。
	select {
	case <-p.exited:
		// 退出已确认，调用方可以继续回收服务使用的数据库。
		return nil
	case <-time.After(5 * time.Second):
		// 超时仍未收到退出通知，交给调用方记录清理失败。
		return errors.New("timed out waiting for application process exit")
	}
}
