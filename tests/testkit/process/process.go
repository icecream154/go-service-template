package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultStartupTimeout      = 30 * time.Second
	defaultGracefulStopTimeout = 25 * time.Second
	defaultKillWaitTimeout     = 5 * time.Second
	defaultLogTailBytes        = 4096
)

// ReadinessProbe 对发现的本机监听地址执行就绪探测，返回 nil 表示服务已可对外接收测试请求。
type ReadinessProbe func(ctx context.Context, client *http.Client, baseURL string) error

// Spec 定义通用宿主机子进程的启动、日志端口发现、就绪探测与停机参数。
type Spec struct {
	// Executable 指向宿主机可执行文件。
	Executable string
	// Args 是传给可执行文件的命令行参数切片。
	Args []string
	// WorkDir 是子进程的工作目录；为空时沿用当前工作目录。
	WorkDir string
	// Env 显式指定子进程的完整环境变量列表（不隐式继承父进程环境，确保配置隔离）。
	Env []string
	// LogDir 指定进程日志目录；为空时写入系统临时目录并在 CloseLogFile 或外部清理时移除。
	LogDir string
	// ListenLogPrefix 指定子进程输出实际监听地址（如 "127.0.0.1:54321"）前的固定日志前缀。
	ListenLogPrefix string
	// ReadinessProbe 可选，发现监听地址后循环调用直至返回 nil 或启动超时。
	ReadinessProbe ReadinessProbe
	// StartupTimeout 限制进程启动、端口发现与就绪探测的总预算；零值采用 30s。
	StartupTimeout time.Duration
	// GracefulStopTimeout 限制发送 SIGTERM 后等待进程自行排空退出的时长；零值采用 25s。
	GracefulStopTimeout time.Duration
	// KillWaitTimeout 限制优雅停机失败发送 SIGKILL 后等待操作系统回收进程的时长；零值采用 5s。
	KillWaitTimeout time.Duration
}

// Process 托管一个已启动的被测服务子进程，支持运行态检查、日志尾部截取及幂等优雅停机。
type Process struct {
	cmd                 *exec.Cmd
	baseURL             string
	logPath             string
	ownsTempLog         bool
	gracefulStopTimeout time.Duration
	killWaitTimeout     time.Duration

	exited  chan struct{}
	waitErr error // exited 关闭后只读安全访问。

	stopOnce sync.Once
	stopErr  error
}

// Start 启动宿主机子进程，等待日志报告回环监听端口并通过就绪探测。
// 若启动或探测中途失败，Start 会自动停止并回收已拉起的子进程，同时在错误中附带进程日志尾部诊断信息。
func Start(ctx context.Context, spec Spec) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("process start requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec.StartupTimeout < 0 || spec.GracefulStopTimeout < 0 || spec.KillWaitTimeout < 0 {
		return nil, errors.New("process timeouts must be nonnegative")
	}
	if strings.TrimSpace(spec.ListenLogPrefix) == "" {
		return nil, errors.New("process ListenLogPrefix is required to discover dynamic loopback port")
	}

	executable, err := validateExecutablePath(spec.Executable)
	if err != nil {
		return nil, err
	}

	startupTimeout := spec.StartupTimeout
	if startupTimeout == 0 {
		startupTimeout = defaultStartupTimeout
	}
	gracefulTimeout := spec.GracefulStopTimeout
	if gracefulTimeout == 0 {
		gracefulTimeout = defaultGracefulStopTimeout
	}
	killTimeout := spec.KillWaitTimeout
	if killTimeout == 0 {
		killTimeout = defaultKillWaitTimeout
	}

	ownsTempLog := strings.TrimSpace(spec.LogDir) == ""
	output, err := os.CreateTemp(spec.LogDir, "testkit-process-*.log")
	if err != nil {
		return nil, fmt.Errorf("create process log file: %w", err)
	}
	logPath := output.Name()

	cmd := exec.Command(executable, spec.Args...)
	cmd.Dir = spec.WorkDir
	if spec.Env != nil {
		cmd.Env = slices.Clone(spec.Env)
	} else {
		cmd.Env = []string{}
	}
	cmd.Stdout = output
	cmd.Stderr = output

	startErr := cmd.Start()
	_ = output.Close()
	if startErr != nil {
		if ownsTempLog {
			_ = os.Remove(logPath)
		}
		return nil, fmt.Errorf("start subprocess: %w", startErr)
	}

	proc := &Process{
		cmd:                 cmd,
		logPath:             logPath,
		ownsTempLog:         ownsTempLog,
		gracefulStopTimeout: gracefulTimeout,
		killWaitTimeout:     killTimeout,
		exited:              make(chan struct{}),
	}
	go proc.wait()

	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	baseURL, err := proc.waitForReady(startupCtx, spec.ListenLogPrefix, spec.ReadinessProbe)
	if err != nil {
		tail := proc.TailLog(defaultLogTailBytes)
		stopErr := proc.Stop()
		if tail != "" {
			return nil, errors.Join(fmt.Errorf("%w\n--- process log tail (%s) ---\n%s", err, logPath, tail), stopErr)
		}
		return nil, errors.Join(fmt.Errorf("%w (log %s)", err, logPath), stopErr)
	}
	proc.baseURL = baseURL
	return proc, nil
}

// BaseURL 返回已通过探测的本机 HTTP 基础地址（如 "http://127.0.0.1:51234"）。
func (p *Process) BaseURL() string {
	return p.baseURL
}

// LogPath 返回子进程标准输出与标准错误合并日志文件路径。
func (p *Process) LogPath() string {
	return p.logPath
}

// Running 非阻塞判断子进程当前是否仍在运行。
func (p *Process) Running() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// Exited 非阻塞判断子进程是否已彻底退出并完成操作系统进程回收（可安全清理下游容器依赖）。
func (p *Process) Exited() bool {
	if p == nil {
		return true
	}
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

// Stop 向子进程发送 SIGTERM 并等待优雅停机，超时则发送 SIGKILL 强制回收。
// 本方法幂等安全：首次调用若发现进程已在停机前异常退出，会返回错误；重复调用直接返回首次停机结果。
func (p *Process) Stop() error {
	if p == nil {
		return nil
	}
	p.stopOnce.Do(func() {
		p.stopErr = p.stopInternal()
		if p.ownsTempLog && p.stopErr == nil && p.Exited() {
			_ = os.Remove(p.logPath)
		}
	})
	return p.stopErr
}

// TailLog 读取日志文件末尾至多 maxBytes 字节内容，供启动失败或异常退出时快速诊断。
func (p *Process) TailLog(maxBytes int) string {
	if p == nil || p.logPath == "" || maxBytes <= 0 {
		return ""
	}
	file, err := os.Open(p.logPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - int64(maxBytes)
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (p *Process) wait() {
	p.waitErr = p.cmd.Wait()
	close(p.exited)
}

func (p *Process) waitForReady(ctx context.Context, listenPrefix string, probe ReadinessProbe) (string, error) {
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()

	var address string
	var probeErr error

	for {
		if address == "" {
			var err error
			address, err = readLoopbackAddressFromLog(p.logPath, listenPrefix)
			if err != nil {
				return "", err
			}
		}
		if address != "" {
			baseURL := "http://" + address
			if probe == nil {
				probeErr = nil
			} else {
				probeErr = probe(ctx, client, baseURL)
			}
			if probeErr == nil {
				select {
				case <-p.exited:
					return "", errors.Join(errors.New("process exited during readiness probe"), p.waitErr)
				default:
					return baseURL, ctx.Err()
				}
			}
		}

		select {
		case <-p.exited:
			return "", errors.Join(errors.New("process exited before readiness"), p.waitErr)
		case <-ctx.Done():
			if address == "" {
				return "", fmt.Errorf("timed out waiting for listener prefix %q in process log: %w", listenPrefix, ctx.Err())
			}
			return "", errors.Join(fmt.Errorf("readiness probe did not succeed before deadline: %w", ctx.Err()), probeErr)
		case <-ticker.C:
		}
	}
}

func readLoopbackAddressFromLog(logPath, prefix string) (string, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return "", fmt.Errorf("open process log: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read process log: %w", err)
		}
		_, value, found := strings.Cut(line, prefix)
		if !found {
			continue
		}
		// 若该日志行在地址后还带有其他键值字段，仅截取首个空白前的 host:port token。
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return "", errors.New("process reported an empty listener address")
		}
		address := strings.Trim(fields[0], `"'`)
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			return "", fmt.Errorf("process listener %q is not an IPv4 loopback address", address)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("process reported invalid port in %q", address)
		}
		return address, nil
	}
}

func (p *Process) stopInternal() error {
	select {
	case <-p.exited:
		return errors.Join(errors.New("process exited unexpectedly before shutdown"), p.waitErr)
	default:
	}

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Join(fmt.Errorf("signal process SIGTERM: %w", err), p.killAndWait())
	}

	select {
	case <-p.exited:
		return p.waitErr
	case <-time.After(p.gracefulStopTimeout):
		return errors.Join(errors.New("process graceful shutdown timed out"), p.killAndWait())
	}
}

func (p *Process) killAndWait() error {
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill process: %w", err)
	}
	select {
	case <-p.exited:
		return nil
	case <-time.After(p.killWaitTimeout):
		return errors.New("timed out waiting for process exit after SIGKILL")
	}
}
