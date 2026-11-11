package logger

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go-service-template/env"
)

type loggerInitOutputCase struct {
	name     string
	withFile bool
}

func TestInit(t *testing.T) {
	tests := []loggerInitOutputCase{
		// 省略文件路径，验证只发布标准输出 Logger，实例标识随初始化生成。
		{name: "stdout only"},
		// 文件已含一条旧日志，验证新记录追加在末尾，并与标准输出的 JSON 行相同。
		{name: "stdout and append to file", withFile: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			stdout := captureLoggerRuntimeStdout(t, dir)
			isolateLoggerRuntime(t)
			const previousLog = "{\"message\":\"existing log\"}\n"
			var path string
			if test.withFile {
				path = filepath.Join(dir, "app.jsonl")
				if err := os.WriteFile(path, []byte(previousLog), 0o600); err != nil {
					t.Fatalf("write existing log: %v", err)
				}
			}
			writeLoggerConfigFixture(t, dir, env.TestEnv, loggerRuntimeDocument(path))
			if err := Init(dir, loggerRuntimeDeployment()); err != nil {
				t.Fatalf("Init() error = %v, want nil", err)
			}
			instance := Current()
			config := instance.Config()
			if config.ServiceName != "logger-runtime" || config.MinLevel != LevelInfo {
				t.Errorf("Config() = %+v, want logger-runtime at INFO", config)
			}
			if id := config.InstanceID; len(id) != 32 || id != strings.ToLower(id) {
				t.Errorf("instance ID = %q, want 32 lowercase hexadecimal characters", id)
			} else if _, err := hex.DecodeString(id); err != nil {
				t.Errorf("instance ID = %q, want hexadecimal characters", id)
			}
			if (shared.file != nil) != test.withFile {
				t.Errorf("Init() opened a shared file = %t, want %t", shared.file != nil, test.withFile)
			}

			instance.Info(t.Context(), "runtime log")
			output := readLoggerRuntimeOutput(t, stdout.Name())
			var record Record
			if err := json.Unmarshal(output, &record); err != nil {
				t.Fatalf("decode standard output record: %v", err)
			}
			if record.Message != "runtime log" || record.InstanceID != config.InstanceID || record.Environment != env.TestEnv {
				t.Errorf("standard output record = %+v, want initialized instance and message", record)
			}
			if !bytes.HasSuffix(output, []byte("\n")) {
				t.Error("standard output did not end with a JSONL newline")
			}
			if test.withFile {
				want := previousLog + string(output)
				if got := string(readLoggerRuntimeOutput(t, path)); got != want {
					t.Errorf("log file = %q, want %q", got, want)
				}
			}

			// 再传空目录，验证已初始化时优先拒绝重复装配并保留原实例。
			err := Init("", loggerRuntimeDeployment())
			if err == nil || !strings.Contains(err.Error(), "already initialized") {
				t.Errorf("repeated Init() error = %v, want already initialized", err)
			}
			if Current() != instance {
				t.Error("repeated Init() replaced the published logger")
			}
		})
	}
}

type loggerInitFailureCase struct {
	name          string
	document      func(*testing.T, string) string
	wantErrorPart string
	wantCause     error
}

func TestInitRejectsInvalidConfiguration(t *testing.T) {
	tests := []loggerInitFailureCase{
		// 所选环境文件不存在，验证初始化保留读取错误的底层原因。
		{name: "missing config file", wantErrorPart: "cannot read logger configuration file", wantCause: os.ErrNotExist},
		// 提供纯空白路径，验证其不会被解释成有效路径或关闭文件输出。
		{
			name: "blank file path", wantErrorPart: "file_path must not be whitespace only",
			document: func(_ *testing.T, _ string) string { return loggerRuntimeDocument(" \t") },
		},
		// 用普通文件占据父目录，验证创建日志目录失败时报告对应阶段。
		{
			name: "cannot create file directory", wantErrorPart: "create logger file directory",
			document: func(t *testing.T, dir string) string {
				parent := filepath.Join(dir, "occupied")
				if err := os.WriteFile(parent, nil, 0o600); err != nil {
					t.Fatalf("create occupied directory fixture: %v", err)
				}
				return loggerRuntimeDocument(filepath.Join(parent, "nested", "app.jsonl"))
			},
		},
		// 将目录本身用作日志路径，验证目录可创建但无法作为追加文件打开。
		{
			name: "cannot open log file", wantErrorPart: "open logger file",
			document: func(_ *testing.T, dir string) string { return loggerRuntimeDocument(dir) },
		},
		// YAML 结构有效但日志级别非法，验证没有文件时仍返回构造器错误。
		{
			name: "invalid minimum level", wantErrorPart: "minimum level",
			document: func(_ *testing.T, _ string) string { return "service_name: logger-runtime\nmin_level: unknown\n" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			isolateLoggerRuntime(t)
			if test.document != nil {
				writeLoggerConfigFixture(t, dir, env.TestEnv, test.document(t, dir))
			}
			err := Init(dir, loggerRuntimeDeployment())
			if err == nil || !strings.Contains(err.Error(), test.wantErrorPart) {
				t.Errorf("Init() error = %v, want error containing %q", err, test.wantErrorPart)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Errorf("Init() error = %v, want cause %v", err, test.wantCause)
			}
			assertNoPublishedLogger(t)
		})
	}
}

func TestInitCanRetryAfterConstructionFailureWithFile(t *testing.T) {
	// 路径可打开但服务名非法，验证失败不会发布实例，修正配置后可重试初始化。
	dir := t.TempDir()
	isolateLoggerRuntime(t)
	path := filepath.Join(dir, "logs", "app.jsonl")
	document := strings.Replace(loggerRuntimeDocument(path), "service_name: logger-runtime", "service_name: ' '", 1)
	writeLoggerConfigFixture(t, dir, env.TestEnv, document)
	err := Init(dir, loggerRuntimeDeployment())
	if err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("Init() error = %v, want missing service name", err)
	}
	assertNoPublishedLogger(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Init() did not reach log file creation: %v", err)
	}

	writeLoggerConfigFixture(t, dir, env.TestEnv, loggerRuntimeDocument(path))
	if err := Init(dir, loggerRuntimeDeployment()); err != nil {
		t.Fatalf("retry Init() error = %v, want nil", err)
	}
	if shared.logger == nil || shared.file == nil {
		t.Error("retry Init() did not publish the logger and file")
	}
}

type runtimeSyncProbe struct {
	io.Writer
	calls int
	err   error
}

func TestFileOutputSync(t *testing.T) {
	// 外层输出器带有可观察的 Sync，验证文件包装器只同步自己拥有的文件。
	file, err := os.CreateTemp(t.TempDir(), "sync")
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	probe := &runtimeSyncProbe{Writer: io.Discard, err: errors.New("unexpected sync of additional output")}
	output := fileOutput{Writer: probe, file: file}
	if err := output.Sync(); err != nil {
		t.Errorf("fileOutput.Sync() error = %v, want nil", err)
	}
	// 关闭真实文件后再同步，验证包装器保留文件的底层同步错误。
	if err := file.Close(); err != nil {
		t.Fatalf("close fixture log file: %v", err)
	}
	if err := output.Sync(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("fileOutput.Sync() error = %v, want closed file", err)
	}
	if probe.calls != 0 {
		t.Errorf("additional output Sync() called %d times, want 0", probe.calls)
	}
}

func TestCurrent(t *testing.T) {
	// 清空共享实例后读取，验证未初始化是显式的启动顺序错误。
	isolateLoggerRuntime(t)
	defer func() {
		if recover() == nil {
			t.Error("Current() did not panic before successful initialization")
		}
	}()
	Current()
}

func TestSync(t *testing.T) {
	// 未持有文件时没有同步工作，验证退出前调用 Sync 安全返回。
	t.Run("without owned file", func(t *testing.T) {
		isolateLoggerRuntime(t)
		if err := Sync(); err != nil {
			t.Errorf("Sync() error = %v, want nil", err)
		}
	})
	// 已初始化的文件可同步，验证同步后所有权和打开状态仍然保留。
	t.Run("keeps file open", func(t *testing.T) {
		dir := t.TempDir()
		isolateLoggerRuntime(t)
		file := initLoggerRuntimeFile(t, dir)
		if err := Sync(); err != nil {
			t.Errorf("Sync() error = %v, want nil", err)
		}
		if _, err := file.Stat(); err != nil || shared.file != file {
			t.Errorf("Sync() did not keep the original file open: %v", err)
		}
	})
}

type loggerShutdownFailureCase struct {
	name        string
	closeBefore bool
}

func TestShutdown(t *testing.T) {
	// 双路输出结束后只关闭 Logger 持有的文件，验证标准输出仍可用且重复关闭安全。
	t.Run("closes only the owned file", func(t *testing.T) {
		dir := t.TempDir()
		stdout := captureLoggerRuntimeStdout(t, dir)
		isolateLoggerRuntime(t)
		file := initLoggerRuntimeFile(t, dir)
		if err := Shutdown(); err != nil {
			t.Fatalf("Shutdown() error = %v, want nil", err)
		}
		if shared.file != nil {
			t.Error("Shutdown() retained the closed file")
		}
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("log file Stat() error = %v, want closed file", err)
		}
		if _, err := stdout.WriteString("still open\n"); err != nil {
			t.Errorf("standard output was closed: %v", err)
		}
		if err := Shutdown(); err != nil {
			t.Errorf("repeated Shutdown() error = %v, want nil", err)
		}
	})

	tests := []loggerShutdownFailureCase{
		// 同步失败但文件仍可关闭，验证保留同步原因并释放文件。
		{name: "sync failure still closes the file"},
		// 文件预先关闭，验证同步和关闭都失败时聚合两种原因。
		{name: "joins sync and close failures", closeBefore: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			isolateLoggerRuntime(t)
			file, err := os.CreateTemp(dir, "shutdown")
			if err != nil {
				t.Fatalf("create log file: %v", err)
			}
			t.Cleanup(func() { _ = file.Close() })
			if test.closeBefore {
				if err := file.Close(); err != nil {
					t.Fatalf("close fixture log file: %v", err)
				}
			}
			syncErr := errors.New("fixture output sync failure")
			probe := &runtimeSyncProbe{Writer: io.Discard, err: syncErr}
			instance, err := New(
				Config{ServiceName: "logger-runtime", InstanceID: "test-instance", MinLevel: LevelInfo},
				loggerRuntimeDeployment(), probe,
			)
			if err != nil {
				t.Fatalf("construct fixture Logger: %v", err)
			}
			shared.logger, shared.file = instance, file

			err = Shutdown()
			if !errors.Is(err, syncErr) || errors.Is(err, os.ErrClosed) != test.closeBefore {
				t.Errorf("Shutdown() error = %v, want sync failure and closed file cause = %t", err, test.closeBefore)
			}
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) || shared.file != nil {
				t.Errorf("Shutdown() did not release the file after failure: %v", err)
			}
			if err := Shutdown(); err != nil || probe.calls != 1 {
				t.Errorf("repeated Shutdown() = %v, Sync calls = %d, want nil, 1", err, probe.calls)
			}
		})
	}
}

func (probe *runtimeSyncProbe) Sync() error {
	probe.calls++
	return probe.err
}

func isolateLoggerRuntime(t *testing.T) {
	t.Helper()
	shared.Lock()
	originalLogger, originalFile := shared.logger, shared.file
	shared.logger, shared.file = nil, nil
	shared.Unlock()
	t.Cleanup(func() {
		if err := Shutdown(); err != nil {
			t.Errorf("close test logger: %v", err)
		}
		shared.Lock()
		shared.logger, shared.file = originalLogger, originalFile
		shared.Unlock()
	})
}

func captureLoggerRuntimeStdout(t *testing.T, dir string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatalf("capture standard output: %v", err)
	}
	original := os.Stdout
	os.Stdout = file
	t.Cleanup(func() {
		os.Stdout = original
		_ = file.Close()
	})
	return file
}

func loggerRuntimeDeployment() env.Config {
	return env.Config{Environment: env.TestEnv, Region: env.CnRegion}
}

func loggerRuntimeDocument(filePath string) string {
	document := "service_name: logger-runtime\nmin_level: INFO\n"
	if filePath != "" {
		document += "file_path: " + strconv.Quote(filePath) + "\n"
	}
	return document
}

func initLoggerRuntimeFile(t *testing.T, dir string) *os.File {
	t.Helper()
	path := filepath.Join(dir, "logs", "app.jsonl")
	writeLoggerConfigFixture(t, dir, env.TestEnv, loggerRuntimeDocument(path))
	if err := Init(dir, loggerRuntimeDeployment()); err != nil {
		t.Fatalf("Init() error = %v, want nil", err)
	}
	if shared.file == nil {
		t.Fatal("Init() did not publish the log file")
	}
	return shared.file
}

func assertNoPublishedLogger(t *testing.T) {
	t.Helper()
	if shared.logger != nil || shared.file != nil {
		t.Error("failed Init() published shared resources")
	}
}

func readLoggerRuntimeOutput(t *testing.T, path string) []byte {
	t.Helper()
	output, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read test log output: %v", err)
	}
	return output
}
