package bootstrap

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestInitializeBuildsRuntimeAndCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yml")
	if err := os.WriteFile(path, []byte("app_name: bootstrap-test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := Initialize(path)
	if err != nil {
		t.Fatal(err)
	}
	if app.Runtime == nil || app.Logger == nil || app.Config.AppName != "bootstrap-test" {
		t.Fatal("application is incomplete")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseIgnoresOnlyUnsupportedConsoleSyncAndPreservesFailures(t *testing.T) {
	console := &os.PathError{Op: "sync", Path: "/dev/stdout", Err: syscall.EINVAL}
	disk := &os.PathError{Op: "sync", Path: "/tmp/application.log", Err: syscall.EINVAL}
	for _, tc := range []struct {
		name    string
		err     error
		wantErr error
	}{
		{"console", console, nil},
		{"console without sync descriptor", &os.PathError{Op: "sync", Path: "/dev/stdout", Err: syscall.EBADF}, nil},
		{"disk", disk, disk},
		{"console write failure", &os.PathError{Op: "sync", Path: "/dev/stderr", Err: syscall.EIO}, syscall.EIO},
		{"plain errno", syscall.EINVAL, syscall.EINVAL},
		{"mixed sinks", multierr.Combine(console, disk), disk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &syncSink{err: tc.err}
			logger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), sink, zap.InfoLevel))
			app := &Application{Logger: logger}
			for range 2 {
				err := app.Close()
				if tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("Close() = %v, expected %v", err, tc.wantErr)
				}
			}
			if sink.calls != 1 {
				t.Fatalf("Sync called %d times", sink.calls)
			}
		})
	}
}

type syncSink struct {
	err   error
	calls int
}

func (s *syncSink) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (s *syncSink) Sync() error                 { s.calls++; return s.err }
