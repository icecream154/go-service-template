// web 是 HTTP 服务的进程入口，负责参数解析、退出信号和应用资源生命周期。
// 环境与数据库的创建交给 bootstrap；本入口负责在初始化成功后启动服务，
// 正常退出先结束 HTTP 访问及内部业务，再释放数据库连接池。运行时日志统一使用英文。
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

// run 持有资源生命周期：全部依赖初始化成功后监听，HTTP 和内部业务均结束后关闭数据库。
//
// ctx 是进程级停止通知，args 仅包含命令行参数，不包含可执行文件名。
// 返回错误交给 main 记录并转换为非零退出码；本函数自身不终止进程。
// resultErr 使用命名返回值，以便 defer 把数据库关闭错误并入原有启动或服务错误。
func run(ctx context.Context, args []string) (resultErr error) {
	// ContinueOnError 使参数错误作为返回值处理，避免 flag 包自行退出进程。
	// 参数帮助信息和后续错误文案保持英文；请求帮助时直接返回，不初始化任何资源。
	options := flag.NewFlagSet("web", flag.ContinueOnError)
	// 配置根目录默认 config，包含 integrations/company、integrations/thirdparty、authn/jwt、snowflake 和 database/mysql。
	// 可使用绝对路径，使从其他工作目录启动时仍能定位同一套部署配置。
	configDir := options.String("config-dir", "config", "Configuration root directory containing integrations/company, integrations/thirdparty, authn/jwt, snowflake and database/mysql")
	// 默认监听所有本机地址的 8080 端口；可指定 127.0.0.1:8081 等地址限制监听范围。
	address := options.String("addr", ":8080", "HTTP listen address")
	// 启动预算默认 30 秒，数据库连接和会话检查共享同一个预算，不是每个连接各获 30 秒。
	startupTimeout := options.Duration("startup-timeout", 30*time.Second, "Maximum application initialization time")
	// HTTP 与内部业务收尾共用默认 10 秒预算，不代表每个业务请求的执行时限。
	shutdownTimeout := options.Duration("shutdown-timeout", 10*time.Second, "Maximum HTTP and internal work shutdown time")
	if err := options.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse startup options: %w", err)
	}
	// 所有参数先校验，再读取配置和联网；额外的位置参数通常意味着启动命令填写错误。
	if options.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if strings.TrimSpace(*configDir) == "" || strings.TrimSpace(*address) == "" {
		return fmt.Errorf("config-dir and addr must not be empty")
	}
	if *startupTimeout <= 0 || *shutdownTimeout <= 0 {
		return fmt.Errorf("startup-timeout and shutdown-timeout must be positive")
	}
	// 例如启动进程后立刻收到停止信号，此时不再读取配置或创建连接。
	if ctx.Err() != nil {
		return nil
	}

	// 启动 context 同时响应系统停止信号和启动超时。
	// 初始化结束后立即释放其计时器；数据库池不会持续使用这个短生命周期的 context。
	startupCtx, cancelStartup := context.WithTimeout(ctx, *startupTimeout)
	runtime, err := bootstrap.Init(startupCtx, *configDir)
	cancelStartup()
	if err != nil {
		// 用户在初始化期间主动停止属于正常退出；独立的启动超时或普通配置、连接故障
		// 仍向上传递错误，最终以非零状态退出，便于部署平台识别启动失败。
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("initialize application: %w", err)
	}
	// 启动或监听失败时正常释放资源；服务运行后，必须确认 HTTP 和内部业务都结束。
	// 收尾预算耗尽时不主动关闭仍可能被使用的连接池，由 main 的失败退出释放进程资源。
	closeRuntime := true
	companyClients := company.Current()
	thirdpartyClients := thirdparty.Current()
	defer func() {
		if !closeRuntime {
			log.Printf("Skip shared resource cleanup: HTTP or internal work was not fully drained; process will exit")
			return
		}
		thirdpartyClients.CloseIdleConnections()
		companyClients.CloseIdleConnections()
		if err := runtime.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close database connections: %w", err))
		}
	}()
	// 初始化刚结束时也可能恰好收到停止信号；此时只执行上面的资源清理，不再监听。
	if ctx.Err() != nil {
		return nil
	}

	// 创建完整路由，并按方法和路径选择默认或覆盖的响应等待时间。
	handler, err := routers.NewHTTPHandler()
	if err != nil {
		return fmt.Errorf("initialize HTTP handler: %w", err)
	}

	// 只有环境、配置文件以及全部启用的数据库连接都初始化成功后，才打印成功日志。
	// 此日志描述应用依赖已就绪；HTTP 是否成功监听由后面的独立日志说明。
	deployment := env.Current()
	log.Printf("Application initialized successfully: environment=%s region=%s", deployment.Environment, deployment.Region)

	// 显式创建 listener 使端口占用等错误直接返回 run，并触发数据库的 defer 清理。
	// 成功后将 listener 的关闭责任交给 serve，避免路由组件自行创建和持有数据库资源。
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("listen for HTTP requests: %w", err)
	}
	log.Printf("HTTP server listening: address=%s", listener.Addr())
	closeRuntime, resultErr = serve(ctx, listener, handler, *shutdownTimeout)
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
