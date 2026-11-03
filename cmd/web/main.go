// web 是 HTTP 服务的进程入口，负责参数解析、退出信号和应用资源生命周期。
// 环境与数据库的创建交给 bootstrap；本入口负责在初始化成功后启动服务，
// 并保证退出路径先结束 HTTP 访问，再释放数据库连接池。运行时日志统一使用英文。
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

	usercontroller "go-service-template/app/domains/user/controller"
	userrepository "go-service-template/app/domains/user/repository"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/bootstrap"
	"go-service-template/env"
	"go-service-template/routers"
)

// main 将系统信号转换为取消通知，并在 run 完成全部清理后决定进程退出码。
// 不在持有资源的函数中调用 log.Fatal 或 os.Exit，因为它们会直接终止进程，跳过 defer。
func main() {
	// Ctrl+C 对应 os.Interrupt，服务管理器通常通过 SIGTERM 请求正常停止。
	// 任一信号都会取消同一个 ctx，初始化阶段和服务运行阶段据此选择各自的退出路径。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	// 释放信号通知资源并恢复信号处理；显式调用，确保后面的 os.Exit 不会跳过它。
	stop()
	// 到达这里时，run 的 defer 已执行，主错误和资源关闭错误也已合并。
	// 正常收到退出信号且清理成功时 err 为 nil，进程自然返回成功退出码。
	if err != nil {
		log.Printf("Application failed: %v", err)
		os.Exit(1)
	}
}

// run 持有资源生命周期：全部依赖初始化成功后监听，HTTP 收尾后关闭数据库。
//
// ctx 是进程级停止通知，args 仅包含命令行参数，不包含可执行文件名。
// 返回错误交给 main 记录并转换为非零退出码；本函数自身不终止进程。
// resultErr 使用命名返回值，以便 defer 把数据库关闭错误并入原有启动或服务错误。
func run(ctx context.Context, args []string) (resultErr error) {
	// ContinueOnError 使参数错误作为返回值处理，避免 flag 包自行退出进程。
	// 参数帮助信息和后续错误文案保持英文；请求帮助时直接返回，不初始化任何资源。
	options := flag.NewFlagSet("web", flag.ContinueOnError)
	// 配置根目录默认 config，实际 MySQL 文件位于其 database/mysql 子目录中。
	// 可使用绝对路径，使从其他工作目录启动时仍能定位同一套部署配置。
	configDir := options.String("config-dir", "config", "Configuration root directory containing database/mysql")
	// 默认监听所有本机地址的 8080 端口；可指定 127.0.0.1:8081 等地址限制监听范围。
	address := options.String("addr", ":8080", "HTTP listen address")
	// 启动预算默认 30 秒，数据库连接和会话检查共享同一个预算，不是每个连接各获 30 秒。
	startupTimeout := options.Duration("startup-timeout", 30*time.Second, "Maximum application initialization time")
	// HTTP 收尾预算默认 10 秒，只控制优雅关闭等待，不代表每个业务请求的执行时限。
	shutdownTimeout := options.Duration("shutdown-timeout", 10*time.Second, "Maximum HTTP graceful shutdown time")
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
	// Runtime 成功返回后，所有后续退出路径都必须释放数据库资源，包括监听失败。
	// errors.Join 同时保留主故障与关闭故障的错误链，避免后发生的错误覆盖最初原因。
	defer func() {
		if err := runtime.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close database connections: %w", err))
		}
	}()
	// 初始化刚结束时也可能恰好收到停止信号；此时只执行上面的资源清理，不再监听。
	if ctx.Err() != nil {
		return nil
	}

	// 使用普通 user 连接装配仓储、Service 和 Controller，随后注册路由。
	// 此连接不绑定已经取消的 startupCtx；连接池仍由上面的 Runtime.Close 统一关闭。
	userDB, err := runtime.Databases.Get("user")
	if err != nil {
		return fmt.Errorf("get user database for HTTP routes: %w", err)
	}
	userProfileRepository := userrepository.NewUserProfileRepository(userDB)
	userProfileService := userservice.NewUserProfileService(userProfileRepository)
	userProfileController := usercontroller.NewUserProfileController(userProfileService)
	router := routers.New(userProfileController)

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
	return serve(ctx, listener, router, *shutdownTimeout)
}

// serve 在收到退出信号后等待现有请求结束。超时则强制关闭 HTTP 连接，
// 返回到 run 后才释放数据库连接池；收尾使用独立 context，不复用已取消的运行 context。
//
// listener 已由 run 成功创建，本函数负责关闭它和 HTTP 连接；handler 只处理业务请求。
// 正常路径使用 Shutdown：停止接收新连接并等待在途请求完成，之后才允许 run 关闭数据库。
// 超时路径使用 Close：直接断开仍存活的 HTTP 连接，随后返回错误并进入数据库清理。
// 强制断开不等于所有业务处理器已经执行完毕；业务和 SQL 操作仍需响应请求 context，
// 并自行处理事务提交结果，不能把网络连接关闭当作业务操作必然已回滚的证明。
func serve(ctx context.Context, listener net.Listener, handler http.Handler, shutdownTimeout time.Duration) error {
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

	select {
	case err := <-serverErr:
		// 监听或服务循环主动失败时及时向上传递；ErrServerClosed 是主动关闭的正常结果。
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP requests: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Printf("Shutting down HTTP server")
		// 运行 ctx 此刻已经取消，若以它为父 context，Shutdown 会立即失败而无法等待请求。
		// 改用 Background 派生独立的关闭预算，仍可用 shutdownTimeout 限制等待时间。
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			// 超时或其他收尾故障后强制断开连接，保留 Shutdown 和 Close 两个阶段的错误。
			// 返回后 run 才执行数据库关闭，main 最后记录合并错误并以非零状态退出。
			return errors.Join(fmt.Errorf("shut down HTTP server: %w", err), server.Close())
		}
		// Shutdown 成功已经等到普通在途请求结束；再接收 Serve 的终止结果，
		// 使监听协程也完成退出，并检查是否存在独立的服务循环错误。
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP requests: %w", err)
		}
		log.Printf("HTTP server shutdown completed")
		return nil
	}
}
