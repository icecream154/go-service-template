//go:build integration || business

package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"go-service-template/tests/testkit/apicall"
	mysqltest "go-service-template/tests/testkit/database/mysql"
	"go-service-template/tests/testkit/httpstub"
	"go-service-template/tests/testkit/process"
	"go-service-template/tests/testkit/repo"
	"go-service-template/tests/testkit/sdkmock"
)

const (
	defaultTokenTTL               = 10 * time.Minute
	defaultStartupTimeout         = 30 * time.Second
	defaultShutdownTimeout        = 10 * time.Second
	defaultCompanyConnectTimeout  = 2 * time.Second
	defaultCompanyRequestTimeout  = 3 * time.Second
	defaultDatabaseResetTimeout   = 30 * time.Second
	defaultDatabaseCleanupTimeout = 90 * time.Second
	sdkMockURLEnvKey              = "APP_INTEGRATION_SDK_MOCK_URL"
)

type environmentConfig struct {
	tokenTTL              time.Duration
	startupTimeout        time.Duration
	shutdownTimeout       time.Duration
	companyConnectTimeout time.Duration
	companyRequestTimeout time.Duration
	machineID             int64
	logDir                string
}

// Option 自定义单套集成测试运行环境（常用于 suite.Isolated 覆盖超时或 Token 有效期）。
type Option func(*environmentConfig)

// WithTokenTTL 自定义被测服务与测试签发器共用的 JWT 有效时长（整秒）。
func WithTokenTTL(ttl time.Duration) Option {
	return func(cfg *environmentConfig) {
		cfg.tokenTTL = ttl
	}
}

// WithShutdownTimeout 自定义 cmd/web 收到停机信号后的优雅排空等待预算。
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *environmentConfig) {
		cfg.shutdownTimeout = timeout
	}
}

// WithCompanyRequestTimeout 自定义公司内部 HTTP Client 的单次请求超时时间。
func WithCompanyRequestTimeout(timeout time.Duration) Option {
	return func(cfg *environmentConfig) {
		cfg.companyRequestTimeout = timeout
	}
}

// Environment 代表一套完整的被测应用运行环境（含真实 MySQL 容器、公司 HTTP 桩群、跨进程第三方 SDK 控制面与 cmd/web 子进程）。
type Environment struct {
	BaseURL      string
	HTTP         *apicall.Client
	Database     *mysqltest.Environment
	Auth         *AuthHelper
	SDKMock      *sdkmock.Controller
	Process      *process.Process
	companyStubs map[string]*httpstub.Server
}

func startEnvironment(ctx context.Context, opts ...Option) (_ *Environment, resultErr error) {
	cfg := environmentConfig{
		tokenTTL:              defaultTokenTTL,
		startupTimeout:        defaultStartupTimeout,
		shutdownTimeout:       defaultShutdownTimeout,
		companyConnectTimeout: defaultCompanyConnectTimeout,
		companyRequestTimeout: defaultCompanyRequestTimeout,
		machineID:             1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	repoRoot, err := repo.FindRoot("")
	if err != nil {
		return nil, fmt.Errorf("locate repository root: %w", err)
	}

	executable, err := process.EnsureBinary(ctx, process.BuildSpec{
		RepositoryRoot: repoRoot,
		Package:        "./cmd/web",
		BuildTags:      []string{"integration_app"},
		EnvOverride:    "INTEGRATION_WEB_BINARY",
	})
	if err != nil {
		return nil, fmt.Errorf("prepare cmd/web integration binary: %w", err)
	}

	authHelper, err := newAuthHelper(cfg.tokenTTL)
	if err != nil {
		return nil, fmt.Errorf("initialize test JWT authority: %w", err)
	}

	companyStubs, companyCfg, err := discoverAndCreateCompanyStubs(repoRoot, cfg.companyConnectTimeout, cfg.companyRequestTimeout)
	if err != nil {
		return nil, err
	}
	sdkMockCtrl := sdkmock.NewController()

	env := &Environment{
		Auth:         authHelper,
		SDKMock:      sdkMockCtrl,
		companyStubs: companyStubs,
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, env.Close())
		}
	}()

	database, err := mysqltest.Create(ctx, mysqltest.Options{
		RepositoryRoot: repoRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("create MySQL test environment: %w", err)
	}
	if database == nil {
		return nil, errors.New("repository must declare MySQL schema for cmd/web integration tests")
	}
	env.Database = database

	configDir := database.ConfigDir()
	if err := writeApplicationTestConfigs(configDir, authHelper.Config(), cfg.machineID, companyCfg); err != nil {
		return nil, fmt.Errorf("write application test configs (%s): %w", database.RunID(), err)
	}

	proc, err := process.Start(ctx, process.Spec{
		Executable: executable,
		Args: []string{
			"-config-dir", configDir,
			"-addr", "127.0.0.1:0",
			"-startup-timeout", cfg.startupTimeout.String(),
			"-shutdown-timeout", cfg.shutdownTimeout.String(),
		},
		WorkDir: configDir,
		Env: []string{
			"APP_ENV=test",
			"APP_REGION=cn",
			"APP_INTEGRATION_TEST=1",
			sdkMockURLEnvKey + "=" + sdkMockCtrl.URL(),
		},
		LogDir:              cfg.logDir,
		ListenLogPrefix:     "HTTP server listening: address=",
		ReadinessProbe:      probeWebNoRouteReady,
		StartupTimeout:      cfg.startupTimeout,
		GracefulStopTimeout: cfg.shutdownTimeout + 15*time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("start cmd/web process (%s): %w", database.RunID(), err)
	}

	env.Process = proc
	env.BaseURL = proc.BaseURL()
	env.HTTP = apicall.NewClient(proc.BaseURL(), 15*time.Second)
	return env, nil
}

// CompanyStub 按 config/integrations/company/test.yml 中声明的服务名取得对应的 HTTP 模拟桩。
func (e *Environment) CompanyStub(serviceName string) *httpstub.Server {
	stub, ok := e.companyStubs[serviceName]
	if !ok {
		panic(fmt.Sprintf("company service %q is not declared in config/integrations/company/test.yml", serviceName))
	}
	return stub
}

// CompanyStubs 返回当前环境挂载的全部公司内部服务 HTTP 模拟桩映射副本。
func (e *Environment) CompanyStubs() map[string]*httpstub.Server {
	copied := make(map[string]*httpstub.Server, len(e.companyStubs))
	maps.Copy(copied, e.companyStubs)
	return copied
}

// DB 按业务域名称（如 "user"）获取供测试直接准备数据或断言终态的独立 SQL 连接池。
func (e *Environment) DB(t testing.TB, domain string) *sql.DB {
	t.Helper()
	pool, err := e.Database.DB(domain)
	if err != nil {
		t.Fatalf("get MySQL connection pool for domain %q: %v", domain, err)
	}
	return pool
}

// verifyOutboundMocksAndReset 清空并核对所有公司内部 HTTP 桩与第三方 SDK 控制面是否发生未注册的意外调用。
func (e *Environment) verifyOutboundMocksAndReset() error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(e.companyStubs)) {
		if err := e.companyStubs[name].VerifyAndReset(); err != nil {
			errs = append(errs, fmt.Errorf("company stub %q: %w", name, err))
		}
	}
	if e.SDKMock != nil {
		if err := e.SDKMock.VerifyAndReset(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// resetStateForReuse 执行共享环境在两条子用例之间的校验与数据清空。
// 返回 outboundErr（当前用例触发了未预期下游调用）和 fatalEnvErr（进程崩溃或数据库清空失败，环境不可继续共享）。
func (e *Environment) resetStateForReuse() (outboundErr error, fatalEnvErr error) {
	outboundErr = e.verifyOutboundMocksAndReset()

	if e.Process == nil || !e.Process.Running() {
		tail := ""
		if e.Process != nil {
			tail = e.Process.TailLog(4096)
		}
		if tail != "" {
			return outboundErr, fmt.Errorf("cmd/web process exited unexpectedly:\n%s", tail)
		}
		return outboundErr, errors.New("cmd/web process exited unexpectedly")
	}

	resetCtx, cancel := context.WithTimeout(context.Background(), defaultDatabaseResetTimeout)
	defer cancel()
	if err := e.Database.Reset(resetCtx); err != nil {
		return outboundErr, fmt.Errorf("reset MySQL tables (%s): %w", e.Database.RunID(), err)
	}
	return outboundErr, nil
}

// Close 按严格逆序回收整套环境：先优雅停止 cmd/web 等待内部请求与连接池排空，再关闭模拟端点，最后销毁 MySQL 容器。
func (e *Environment) Close() error {
	var errs []error

	if e.Process != nil {
		if err := e.Process.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stop cmd/web process: %w", err))
		}
		if !e.Process.Exited() {
			errs = append(errs, errors.New("cmd/web process exit not confirmed; retaining MySQL container to prevent active use-after-close"))
			return errors.Join(errs...)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(e.companyStubs)) {
		e.companyStubs[name].Close()
	}
	if e.SDKMock != nil {
		e.SDKMock.Close()
	}

	if e.Database != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), defaultDatabaseCleanupTimeout)
		defer cancel()
		if err := e.Database.Close(closeCtx); err != nil {
			errs = append(errs, fmt.Errorf("close MySQL environment (%s): %w", e.Database.RunID(), err))
		}
	}

	return errors.Join(errs...)
}

// probeWebNoRouteReady 向未注册路径发起无副作用 GET 请求，确认 cmd/web 完整路由链已就绪并返回标准 404 NOT_FOUND。
func probeWebNoRouteReady(ctx context.Context, client *http.Client, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/__integration_ready__", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if resp.StatusCode != http.StatusNotFound ||
		json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil ||
		body.Error.Code != "NOT_FOUND" {
		return fmt.Errorf("unexpected readiness response (HTTP %d)", resp.StatusCode)
	}
	return nil
}
