//go:build integration || business

package harness

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// Suite 管理单个测试包生命周期内的按需共享环境（Shared）与用例级独占环境（Isolated）。
// 零值可直接声明为包级变量并在 TestMain 中通过 suite.Run(m) 托管。
type Suite struct {
	mu           sync.Mutex
	shared       *Environment
	poisonReason error
}

// Run 执行当前测试包中的全部测试并在退出前安全回收共享环境资源。
func (s *Suite) Run(m *testing.M) {
	code := m.Run()
	if err := s.closeShared(); err != nil {
		fmt.Fprintf(os.Stderr, "harness.Suite teardown error: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// Shared 返回当前测试包复用的共享运行环境（首次调用时懒启动），并在当前用例结束时自动核对出站调用并清空数据库表。
// 注意：共用 Shared 环境的测试函数或子场景禁止并行（t.Parallel），单个子场景内部可自行并发发请求并等待全部完成。
func (s *Suite) Shared(t testing.TB) *Environment {
	t.Helper()

	s.mu.Lock()
	if s.poisonReason != nil {
		reason := s.poisonReason
		s.mu.Unlock()
		t.Fatalf("shared integration environment is unavailable due to an earlier unrecoverable failure: %v", reason)
	}
	if s.shared == nil {
		initCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		env, err := startEnvironment(initCtx)
		cancel()
		if err != nil {
			s.poisonReason = err
			s.mu.Unlock()
			t.Fatalf("initialize shared integration environment: %v", err)
		}
		s.shared = env
	}
	env := s.shared
	s.mu.Unlock()

	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.shared != env {
			return
		}
		outboundErr, fatalEnvErr := env.resetStateForReuse()
		if outboundErr != nil {
			t.Errorf("unexpected outbound dependency call during test: %v", outboundErr)
		}
		if fatalEnvErr != nil {
			s.poisonReason = fatalEnvErr
			s.shared = nil
			closeErr := env.Close()
			t.Errorf("shared integration environment poisoned and closed (%v; closeErr=%v)", fatalEnvErr, closeErr)
		}
	})

	return env
}

// Isolated 为当前测试用例单独创建一套完全隔离的容器与 cmd/web 子进程（仍复用单次编译缓存的二进制），
// 适用于测试停机排空、长尾后台未完成事务或特殊全局配置（如自定义 TokenTTL / ShutdownTimeout）的场景。
func (s *Suite) Isolated(t testing.TB, opts ...Option) *Environment {
	t.Helper()

	optsWithLog := append([]Option{func(cfg *environmentConfig) {
		cfg.logDir = t.ArtifactDir()
	}}, opts...)

	env, err := startEnvironment(t.Context(), optsWithLog...)
	if err != nil {
		t.Fatalf("initialize isolated integration environment: %v", err)
	}
	t.Cleanup(func() {
		if outboundErr := env.verifyOutboundMocksAndReset(); outboundErr != nil {
			t.Errorf("unexpected outbound dependency call during isolated test: %v", outboundErr)
		}
		if closeErr := env.Close(); closeErr != nil {
			t.Errorf("close isolated integration environment: %v", closeErr)
		}
	})
	return env
}

func (s *Suite) closeShared() error {
	s.mu.Lock()
	env := s.shared
	s.shared = nil
	s.mu.Unlock()
	if env == nil {
		return nil
	}
	return env.Close()
}
