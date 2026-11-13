package process

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureBinaryAndLifecycle(t *testing.T) {
	// 在临时独立 Go 模块中编译一个最小 HTTP 服务，验证 EnsureBinary、Start、就绪探测与优雅停机完整闭环，零依赖业务代码。
	tempModule := t.TempDir()
	goMod := "module example.com/minihttp\n\ngo 1.24\n"
	mainSrc := `package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "MINI_LISTEN=%s\n", ln.Addr().String())
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		_ = srv.Shutdown(context.Background())
	}()
	_ = srv.Serve(ln)
}
`
	if err := os.WriteFile(filepath.Join(tempModule, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempModule, "main.go"), []byte(mainSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	binPath, err := EnsureBinary(t.Context(), BuildSpec{
		RepositoryRoot: tempModule,
		Package:        ".",
	})
	if err != nil {
		t.Fatalf("EnsureBinary failed: %v", err)
	}
	// 第二次调用命中缓存，返回完全一致的路径。
	binPath2, err := EnsureBinary(t.Context(), BuildSpec{
		RepositoryRoot: tempModule,
		Package:        ".",
	})
	if err != nil || binPath2 != binPath {
		t.Fatalf("second EnsureBinary = (%q, %v), want (%q, nil)", binPath2, err, binPath)
	}

	proc, err := Start(t.Context(), Spec{
		Executable:      binPath,
		WorkDir:         tempModule,
		LogDir:          t.TempDir(),
		ListenLogPrefix: "MINI_LISTEN=",
		StartupTimeout:  20 * time.Second,
		ReadinessProbe: func(ctx context.Context, client *http.Client, baseURL string) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/ready", nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				return fmt.Errorf("status = %d", resp.StatusCode)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !proc.Running() || proc.Exited() {
		t.Fatal("process should be running after readiness")
	}
	if !strings.HasPrefix(proc.BaseURL(), "http://127.0.0.1:") {
		t.Fatalf("unexpected BaseURL: %q", proc.BaseURL())
	}

	// 验证幂等优雅停机。
	if err := proc.Stop(); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}
	if err := proc.Stop(); err != nil {
		t.Fatalf("second Stop must be idempotent, got: %v", err)
	}
	if !proc.Exited() || proc.Running() {
		t.Fatal("process must report Exited after Stop")
	}
}

func TestStartIncludesLogTailWhenProcessCrashesEarly(t *testing.T) {
	tempModule := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempModule, "go.mod"), []byte("module example.com/crasher\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crashSrc := `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "FATAL_BOOT_DIAGNOSTIC: missing required configuration")
	os.Exit(2)
}
`
	if err := os.WriteFile(filepath.Join(tempModule, "main.go"), []byte(crashSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	binPath, err := EnsureBinary(t.Context(), BuildSpec{
		RepositoryRoot: tempModule,
		Package:        ".",
	})
	if err != nil {
		t.Fatalf("EnsureBinary failed: %v", err)
	}

	_, err = Start(t.Context(), Spec{
		Executable:      binPath,
		LogDir:          t.TempDir(),
		ListenLogPrefix: "LISTEN=",
		StartupTimeout:  20 * time.Second,
	})
	if err == nil {
		t.Fatal("Start must fail when process exits before readiness")
	}
	if !strings.Contains(err.Error(), "FATAL_BOOT_DIAGNOSTIC") {
		t.Fatalf("startup error should contain process log tail, got: %v", err)
	}
}
