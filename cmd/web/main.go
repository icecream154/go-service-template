// web 是 HTTP 服务的进程入口，负责参数解析、退出信号和应用资源生命周期。
// 环境、Logger 与数据库等依赖的创建交给 bootstrap；本入口负责在初始化成功后启动服务，
// 正常退出先结束 HTTP 访问及内部业务，再释放共享资源并同步日志。运行时日志统一使用英文。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go-service-template/app/integrations/company"
	"go-service-template/app/integrations/thirdparty"
	"go-service-template/bootstrap"
	"go-service-template/env"
	"go-service-template/internal/logger"
	"go-service-template/routers"
)

// main 将系统信号转换为停止通知，依据 run 的收尾结果决定进程退出码。
// 不在持有资源的函数中调用 log.Fatal 或 os.Exit，因为它们会直接终止进程，跳过 defer。
func main() {
	// Ctrl+C 对应 os.Interrupt，服务管理器通常通过 SIGTERM 请求正常停止。
	// 任一信号都会取消同一个 ctx，初始化阶段和服务运行阶段据此选择各自的退出路径。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	// 释放信号通知资源并恢复信号处理；显式调用，确保后面的 os.Exit 不会跳过它。
	stop()
	// 正常收尾已关闭资源；收尾超时则保留仍被内部业务使用的连接池，直接失败退出。
	// 进程退出释放连接，不代表所有业务已完成，也不代表所有事务已回滚。
	// 正常收到退出信号且清理成功时 err 为 nil，进程自然返回成功退出码。
	if err != nil {
		log.Printf("Application failed: %v", err)
		os.Exit(1)
	}
}

// run 持有资源生命周期：全部依赖初始化成功后监听，HTTP 和内部业务均结束后清理资源，最后同步日志。
//
// ctx 是进程级停止通知，args 仅包含命令行参数，不包含可执行文件名。
// 返回错误交给 main 记录并转换为非零退出码；本函数自身不终止进程。
// resultErr 使用命名返回值，以便 defer 把数据库关闭、日志同步错误并入原有启动或服务错误。
func run(ctx context.Context, args []string) (resultErr error) {
	// 1. 创建独立的启动参数集合。ContinueOnError 使参数错误作为返回值处理，避免 flag 包自行退出进程。
	// 参数帮助信息和后续错误文案保持英文；请求帮助时直接返回，不初始化任何资源。
	options := flag.NewFlagSet("web", flag.ContinueOnError)
	// 1.1. 配置根目录默认 config，包含 logger、integrations/company、integrations/thirdparty、authn/jwt、snowflake 和 database/mysql。
	// 可使用绝对路径，使从其他工作目录启动时仍能定位同一套部署配置。
	configDir := options.String("config-dir", "config", "Configuration root directory containing logger, integrations/company, integrations/thirdparty, authn/jwt, snowflake and database/mysql")
	// 1.2. 声明 HTTP 监听地址，默认监听所有本机地址的 8080 端口；可指定 127.0.0.1:8081 等地址限制范围。
	address := options.String("addr", ":8080", "HTTP listen address")
	// 1.3. 声明整个依赖初始化过程的预算，默认 30 秒；数据库连接和会话检查共享它，不是每次检查各获 30 秒。
	startupTimeout := options.Duration("startup-timeout", 30*time.Second, "Maximum application initialization time")
	// 1.4. 声明 HTTP 和内部业务收尾共同使用的预算，默认 10 秒；它不表示某个请求的业务执行期限。
	shutdownTimeout := options.Duration("shutdown-timeout", 10*time.Second, "Maximum HTTP and internal work shutdown time")
	// 2. 解析调用方传入的参数，先处理帮助或语法错误，再进行任何配置读取与资源创建。
	if err := options.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// 2.1. 帮助信息已由 flag 包写出，按正常结果返回，无须进入初始化和资源清理流程。
			return nil
		}
		// 2.2. 其他参数解析故障保留原始原因，交给 main 统一输出并决定失败退出码。
		return fmt.Errorf("parse startup options: %w", err)
	}
	// 3. 依次检查参数契约，全部通过后才允许 bootstrap 读取配置或建立连接。
	// 3.1. 本进程只接受命名选项，额外的位置参数通常意味着启动命令填写错误。
	if options.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	// 3.2. 配置根目录和监听地址必须具有内容；这里只判断空白，不静默修剪或改写调用方传入的值。
	if strings.TrimSpace(*configDir) == "" || strings.TrimSpace(*address) == "" {
		return fmt.Errorf("config-dir and addr must not be empty")
	}
	// 3.3. 两种预算都必须为正，避免用零值或负值意外表达立即超时或无限等待。
	if *startupTimeout <= 0 || *shutdownTimeout <= 0 {
		return fmt.Errorf("startup-timeout and shutdown-timeout must be positive")
	}
	// 4. 检查进程是否已收到停止通知；例如启动后立即按下 Ctrl+C，此时无需继续读取配置或创建连接。
	if ctx.Err() != nil {
		return nil
	}

	// 5. 在 bootstrap 前登记最终日志同步，使 Logger 成功但后续依赖失败的路径也能收尾。
	// 与现有共享资源共用关闭条件；后登记的 Client/数据库清理 defer 会先执行，Logger 最后同步。
	closeRuntime := true
	defer func() {
		// 5.1. 内部工作未排空时可能仍在写日志，跳过同步以免等待写锁而延长已经耗尽的收尾预算。
		// Logger 不拥有 stdout，跳过同步也不会关闭或取消后台任务正在使用的输出器。
		if !closeRuntime {
			return
		}
		// 5.2. 尚未初始化 Logger 时 Sync 安全返回 nil；成功初始化后同步错误并入进程返回值。
		// 最终由 main 的既有错误输出报告，避免用同一个故障 Logger 递归记录同步错误。
		if err := logger.Sync(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("sync application logs: %w", err))
		}
	}()

	// 6. 从进程通知 Context 派生独立启动预算，使初始化同时响应系统停止信号和启动超时。
	startupCtx, cancelStartup := context.WithTimeout(ctx, *startupTimeout)
	// 6.1. 统一调用 bootstrap 完成环境、Logger、各 Client、认证和数据库等资源装配，入口不展开组件内部逻辑。
	runtime, err := bootstrap.Init(startupCtx, *configDir)
	// 6.2. 初始化返回后立即释放计时器；成功创建的连接池不会持续使用这个短生命周期 Context。
	cancelStartup()
	// 6.3. 初始化没有交付完整 Runtime 时结束启动；bootstrap 负责自身部分资源的失败清理。
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			// 6.3.1. 进程停止通知导致的初始化取消视为正常退出，仍会执行前面登记的 Logger 同步。
			return nil
		}
		// 6.3.2. 独立启动超时、配置错误或连接故障保留错误链，最终以失败状态退出供部署平台识别。
		return fmt.Errorf("initialize application: %w", err)
	}
	// 7. 保存完整的 Client 集合，并在成功获得 Runtime 后登记共享资源清理。
	// 此 defer 后登记、先执行，只有 HTTP 和内部业务都确认结束，才释放资源并允许最后同步 Logger。
	companyClients := company.Current()
	thirdpartyClients := thirdparty.Current()
	defer func() {
		// 7.1. 未排空时资源可能仍被内部工作使用，保留既有错误输出并跳过主动清理；
		// 同一个 closeRuntime 标志也会使更外层 Logger defer 跳过同步，最终交由失败退出释放进程资源。
		if !closeRuntime {
			log.Printf("Skip shared resource cleanup: HTTP or internal work was not fully drained; process will exit")
			return
		}
		// 7.2. 内部工作已结束，先释放第三方、公司 HTTP 空闲连接，再关闭数据库连接池。
		// 空闲连接清理不会取消在途调用；前面的排空判断保证不会提前释放仍被业务使用的资源。
		thirdpartyClients.CloseIdleConnections()
		companyClients.CloseIdleConnections()
		if err := runtime.Close(); err != nil {
			// 7.3. 数据库关闭错误与启动或服务错误合并，不覆盖原有失败原因；之后仍继续执行 Logger defer。
			resultErr = errors.Join(resultErr, fmt.Errorf("close database connections: %w", err))
		}
	}()
	// 8. 初始化完成时再次检查停止通知；若此时才收到信号，执行已登记的完整清理而不再启动监听。
	if ctx.Err() != nil {
		return nil
	}

	// 9. 装配完整 HTTP Handler，使每次请求按方法和路径选择默认或覆盖的响应等待时间。
	handler, err := routers.NewHTTPHandler()
	if err != nil {
		// 9.1. 路由或响应等待配置失败时尚未开始接收请求，可以正常清理全部资源。
		return fmt.Errorf("initialize HTTP handler: %w", err)
	}

	// 10. 依赖和路由均已就绪，使用既有进程日志输出环境、区域；这条记录不表示 HTTP 已成功监听。
	// 该记录明确使用标准库 log.Printf；共享 Logger 的发布不改变标准库日志的输出方式。
	deployment := env.Current()
	log.Printf("Application initialized successfully: environment=%s region=%s", deployment.Environment, deployment.Region)

	// 11. 显式创建 TCP Listener，端口占用等错误在进入服务循环前返回，并触发已登记的资源清理。
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		// 11.1. 监听尚未成功时没有业务请求进入，维持 closeRuntime=true，允许完整清理及日志同步。
		return fmt.Errorf("listen for HTTP requests: %w", err)
	}
	// 11.2. 输出实际监听地址；随后将 Listener 的关闭责任交给 serve，不交给 Router 持有。
	log.Printf("HTTP server listening: address=%s", listener.Addr())
	// 12. 持续服务并统一收尾；第一个返回值仅在 HTTP 与内部工作均排空时为 true。
	// 该值决定两个 defer 是否可以安全执行共享资源清理及 Logger 同步，不由某次请求状态推断。
	closeRuntime, resultErr = serve(ctx, listener, handler, *shutdownTimeout)
	// 13. 返回服务与收尾结果，随后逆序执行共享资源清理、Logger 同步；最终错误由 main 报告。
	return resultErr
}

// serve 在停止信号或监听故障后，使用同一独立预算等待 HTTP 和全部内部业务。
// drained 仅在两部分都确认结束时为 true，供 run 决定能否正常关闭数据库。
// 收尾超时会关闭 HTTP 连接并返回错误，不取消内部业务，也不宣称事务已回滚。
func serve(ctx context.Context, listener net.Listener, handler *routers.HTTPHandler, shutdownTimeout time.Duration) (drained bool, resultErr error) {
	server := &http.Server{
		Handler: handler,
		// 只限制读取 HTTP 请求头的时间，避免慢请求头长期占用连接；不限制完整业务执行时长。
		ReadHeaderTimeout: 5 * time.Second,
	}
	// 覆盖监听异常等提前返回路径；正常 Shutdown 后再次 Close 也可安全处理。
	defer server.Close()
	// Serve 负责接收连接，会一直阻塞到监听结束，因此放入独立协程执行。
	// 通道只传递一次终止结果，容量 1 保证主流程在关闭失败时提前返回也不会阻塞发送协程。
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve(listener)
	}()

	var serveErr error
	serveReturned := false
	select {
	case serveErr = <-serverErr:
		serveReturned = true
	case <-ctx.Done():
	}

	// 监听异常也可能留下正在执行的业务，因此所有退出触发都经过同一收尾流程。
	log.Printf("Shutting down HTTP server and internal work")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	httpErr := server.Shutdown(shutdownCtx)
	if httpErr != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("shut down HTTP server: %w", httpErr), server.Close())
	}
	// 即使 HTTP 等待失败，也不能跳过内部收尾；各超时组件共用剩余的同一预算。
	workErr := handler.Shutdown(shutdownCtx)
	if workErr != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("drain internal HTTP work: %w", workErr))
	}
	if !serveReturned {
		serveErr = <-serverErr
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		resultErr = errors.Join(resultErr, fmt.Errorf("serve HTTP requests: %w", serveErr))
	}
	drained = httpErr == nil && workErr == nil
	if drained {
		log.Printf("HTTP server and internal work shutdown completed")
	} else {
		log.Printf("HTTP shutdown incomplete: http_error=%v internal_work_error=%v", httpErr, workErr)
	}
	return drained, resultErr
}
