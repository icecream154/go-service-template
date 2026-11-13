// Package process 提供与具体业务解耦的宿主机测试二进制按需单次编译与子进程生命周期管理。
package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go-service-template/tests/testkit/repo"
)

// BuildSpec 描述待构建或复用的 Go main 包可执行文件规格。
type BuildSpec struct {
	// RepositoryRoot 指定执行 go build 的模块根目录；为空时从当前目录自动向上查找 go.mod。
	RepositoryRoot string
	// Package 指定待编译的 main 包路径（例如 "./cmd/web"）。
	Package string
	// BuildTags 指定传递给 go build -tags 的构建标签列表（例如 []string{"integration_app"}）。
	BuildTags []string
	// EnvOverride 可选环境变量名；若该环境变量非空，则直接校验并复用其指向的预构建二进制，跳过编译。
	EnvOverride string
}

var inProcessBuildCache sync.Map // key: cacheKey string -> binaryPath string

const staleBuildCacheMaxAge = 2 * time.Hour

// EnsureBinary 返回可直接运行的宿主机二进制绝对路径。
// 若 EnvOverride 指定的环境变量已设置，验证可执行文件后直接返回；
// 否则按父进程会话标识（PPID）与跨进程文件锁在系统临时目录仅编译一次，兼顾多包并行零重复编译与改码即时生效。
func EnsureBinary(ctx context.Context, spec BuildSpec) (string, error) {
	if ctx == nil {
		return "", errors.New("binary build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if envName := strings.TrimSpace(spec.EnvOverride); envName != "" {
		if rawPath := strings.TrimSpace(os.Getenv(envName)); rawPath != "" {
			return validateExecutablePath(rawPath)
		}
	}

	pkg := strings.TrimSpace(spec.Package)
	if pkg == "" {
		return "", errors.New("build package path is required when override binary is not set")
	}

	root, err := repo.FindRoot(spec.RepositoryRoot)
	if err != nil {
		return "", fmt.Errorf("locate module root for build: %w", err)
	}

	tags := slices.Clone(spec.BuildTags)
	slices.Sort(tags)
	tagsArg := strings.Join(tags, ",")

	keySource := strings.Join([]string{
		root,
		pkg,
		tagsArg,
		runtime.GOOS,
		runtime.GOARCH,
		runtime.Version(),
		strconv.Itoa(os.Getppid()),
	}, "|")
	sum := sha256.Sum256([]byte(keySource))
	cacheKey := hex.EncodeToString(sum[:12])

	if cached, ok := inProcessBuildCache.Load(cacheKey); ok {
		cachedPath := cached.(string)
		if validated, err := validateExecutablePath(cachedPath); err == nil {
			return validated, nil
		}
		inProcessBuildCache.Delete(cacheKey)
	}

	cacheBaseDir := filepath.Join(os.TempDir(), "go-testkit-build-cache")
	if err := os.MkdirAll(cacheBaseDir, 0o700); err != nil {
		return "", fmt.Errorf("create build cache directory: %w", err)
	}
	sweepStaleBuildArtifacts(cacheBaseDir)

	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	targetBinary := filepath.Join(cacheBaseDir, "bin-"+cacheKey+exeSuffix)
	lockPath := filepath.Join(cacheBaseDir, "bin-"+cacheKey+".lock")

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("open build lock file: %w", err)
	}
	defer lockFile.Close()

	if err := acquireFileLock(ctx, lockFile); err != nil {
		return "", err
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	// 获得跨进程锁后再次检查：若同批并行的其他测试包已完成编译，直接复用。
	if validated, err := validateExecutablePath(targetBinary); err == nil {
		inProcessBuildCache.Store(cacheKey, validated)
		return validated, nil
	}

	goBinary, err := resolveGoToolchainBinary()
	if err != nil {
		return "", err
	}

	tempBinary := fmt.Sprintf("%s.tmp.%d", targetBinary, os.Getpid())
	defer func() {
		_ = os.Remove(tempBinary)
	}()

	args := []string{"build"}
	if tagsArg != "" {
		args = append(args, "-tags="+tagsArg)
	}
	args = append(args, "-o", tempBinary, pkg)

	cmd := exec.CommandContext(ctx, goBinary, args...)
	cmd.Dir = root
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build %s (tags=%q) failed: %w\n%s", pkg, tagsArg, err, strings.TrimSpace(output.String()))
	}
	if err := os.Chmod(tempBinary, 0o755); err != nil {
		return "", fmt.Errorf("chmod built binary: %w", err)
	}
	if err := os.Rename(tempBinary, targetBinary); err != nil {
		return "", fmt.Errorf("publish built binary: %w", err)
	}

	validated, err := validateExecutablePath(targetBinary)
	if err != nil {
		return "", err
	}
	inProcessBuildCache.Store(cacheKey, validated)
	return validated, nil
}

// validateExecutablePath 将路径解析为绝对路径并确认其为普通可执行文件。
func validateExecutablePath(rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", errors.New("executable path is required")
	}
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("inspect executable %s: %w", absPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("executable path is not a regular file: %s", absPath)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("file does not have execute permission: %s", absPath)
	}
	return absPath, nil
}

// resolveGoToolchainBinary 优先复用编译当前测试进程的 GOROOT/bin/go，兼容 IDE 无 PATH 场景。
func resolveGoToolchainBinary() (string, error) {
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	if goroot := runtime.GOROOT(); goroot != "" {
		candidate := filepath.Join(goroot, "bin", "go"+exeSuffix)
		if validated, err := validateExecutablePath(candidate); err == nil {
			return validated, nil
		}
	}
	if found, err := exec.LookPath("go" + exeSuffix); err == nil {
		return found, nil
	}
	return "", errors.New("cannot locate go toolchain binary via runtime.GOROOT or PATH")
}

// acquireFileLock 在支持取消信号的轮询中请求排他文件锁，防止异常阻塞无法响应测试超时。
func acquireFileLock(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("acquire build lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for build lock: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// sweepStaleBuildArtifacts 尽力清理超过保留时长的历史临时编译产物，忽略并发竞争错误。
func sweepStaleBuildArtifacts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleBuildCacheMaxAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if strings.HasPrefix(entry.Name(), "bin-") && !strings.HasSuffix(entry.Name(), ".lock") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
