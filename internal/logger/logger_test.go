package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"

	"go-service-template/env"
	"go-service-template/internal/tracing"
)

type newCoreLoggerCase struct {
	name        string
	config      Config
	environment string
	output      io.Writer
	wantError   string
}

func TestNew(t *testing.T) {
	valid := coreLoggerConfig(LevelInfo)
	tests := []newCoreLoggerCase{
		// 配置完整且输出目标可用，验证能构造独立的日志实例。
		{name: "complete configuration", config: valid, environment: "test", output: io.Discard},
		// 服务名只含空白，验证无法区分服务来源时拒绝构造。
		{
			name: "blank service", config: Config{ServiceName: " \t", InstanceID: valid.InstanceID, MinLevel: LevelInfo},
			environment: "test", output: io.Discard, wantError: "service name is required",
		},
		// 实例标识为空白时缺少进程来源，验证构造器要求显式提供标识。
		{
			name: "blank instance ID", config: Config{ServiceName: valid.ServiceName, InstanceID: "\n", MinLevel: LevelInfo},
			environment: "test", output: io.Discard, wantError: "instance ID is required",
		},
		// 省略最低级别，验证零值不能触发隐式默认配置。
		{name: "missing level", config: coreLoggerConfig(""), environment: "test", output: io.Discard, wantError: "minimum level must be"},
		// 小写名称对应已知级别，验证构造器仍要求固定的大小写。
		{name: "lowercase level", config: coreLoggerConfig("info"), environment: "test", output: io.Discard, wantError: "minimum level must be"},
		// 有有效的日志配置但没有部署环境，验证缺失环境会单独报错。
		{name: "blank environment", config: valid, environment: " ", output: io.Discard, wantError: "environment is required"},
		// 配置合法但未提供 Writer，验证缺失输出目标时不发布部分实例。
		{name: "nil output", config: valid, environment: "test", wantError: "output is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := New(test.config, env.Config{Environment: test.environment}, test.output)
			assertCoreLoggerError(t, err, nil, test.wantError)
			if test.wantError == "" {
				if got == nil {
					t.Fatal("New() returned a nil logger")
				}
				return
			}
			if got != nil {
				t.Error("New() returned a logger with invalid input")
			}
		})
	}
}

func TestLoggerConfig(t *testing.T) {
	// 配置值含有效内容及两侧空格，验证构造后保存原值，修改输入和返回副本都不会改变实例配置。
	input := Config{ServiceName: " profile service ", InstanceID: " instance-a ", MinLevel: LevelWarn}
	want := input
	instance, err := New(input, env.Config{Environment: "test"}, io.Discard)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	input.ServiceName = "modified input"
	snapshot := instance.Config()
	if snapshot != want {
		t.Fatalf("Config() = %+v, want %+v", snapshot, want)
	}
	snapshot.MinLevel = LevelDebug
	if got := instance.Config(); got != want {
		t.Errorf("Config() after mutating returned copy = %+v, want %+v", got, want)
	}
}

type coreLogLevelCase struct {
	name        string
	minimum     Level
	fields      []Fields
	wantWritten bool
	wantFailure string
}

func TestLoggerDebug(t *testing.T) {
	testCoreLogLevel(t, (*Logger).Debug, LevelDebug, LevelInfo)
}

func TestLoggerInfo(t *testing.T) {
	testCoreLogLevel(t, (*Logger).Info, LevelInfo, LevelWarn)
}

type coreLogRecordCase struct {
	name       string
	context    func(*testing.T) context.Context
	fields     []Fields
	wantFields map[string]any
}

func TestLoggerInfoRecord(t *testing.T) {
	tests := []coreLogRecordCase{
		// 进程日志没有上下文和可选字段，验证只输出必填字段和真实打印位置。
		{name: "process record"},
		// 仅有兼容 TraceId 和开始事件，验证输出已有标识及事件，省略没有提供的 SpanId。
		{
			name: "legacy trace and started event",
			context: func(t *testing.T) context.Context {
				return tracing.WithTraceID(t.Context(), "0123456789abcdef0123456789abcdef")
			},
			fields:     []Fields{{Event: EventStarted}},
			wantFields: map[string]any{"trace_id": "0123456789abcdef0123456789abcdef", "event": "started"},
		},
		// 取消的上下文仍保留操作及未采样的标准链路，显式数值零仍应写入 JSON。
		{
			name: "canceled HTTP operation with explicit zero values", context: coreHTTPLogContext,
			fields: []Fields{{Event: EventFinished, DurationMS: new(float64(0)), StatusCode: new(0), Error: "Controlled failure"}},
			wantFields: map[string]any{
				"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736", "span_id": "00f067aa0ba902b7",
				"component": "http_entry", "operation": "GET /profiles/:id", "event": "finished",
				"duration_ms": float64(0), "status_code": float64(0), "error": "Controlled failure",
			},
		},
		// 提供带小数的毫秒耗时和实际 HTTP 状态码，验证两个字段都以 JSON 数值输出。
		{
			name: "measured duration and status", fields: []Fields{{DurationMS: new(12.5), StatusCode: new(204)}},
			wantFields: map[string]any{"duration_ms": 12.5, "status_code": float64(204)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var output bytes.Buffer
				instance := newCoreLogger(t, LevelInfo, &output)
				var ctx context.Context
				if test.context != nil {
					ctx = test.context(t)
				}

				// 虚拟时钟推进非整秒，验证时间使用 UTC 且保留纳秒。
				time.Sleep(123_456_789 * time.Nanosecond)
				wantTime := time.Now().UTC().Format(time.RFC3339Nano)
				const message = "first line\n\"second line\""
				wantCaller := writeCoreInfo(t, instance, ctx, message, test.fields)
				want := map[string]any{
					"timestamp": wantTime, "level": "INFO", "message": message, "caller": wantCaller,
					"service_name": "profile-service", "environment": "test", "instance_id": "instance-a",
				}
				maps.Copy(want, test.wantFields)
				if got := decodeCoreLogRecord(t, output.Bytes()); !reflect.DeepEqual(got, want) {
					t.Errorf("JSON record = %#v, want %#v", got, want)
				}
			})
		})
	}
}

func TestLoggerInfoConcurrent(t *testing.T) {
	// 让多个协程共用非并发安全的内存 Writer，验证 Logger 输出的每一行完整且互不覆盖。
	const workers = 16
	var output bytes.Buffer
	instance := newCoreLogger(t, LevelInfo, &output)
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := range workers {
		group.Go(func() {
			<-start
			instance.Info(t.Context(), fmt.Sprintf("worker %d", index))
		})
	}
	close(start)
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent logging did not finish")
	}

	seen := make(map[string]bool, workers)
	for line := range bytes.SplitAfterSeq(output.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		record := decodeCoreLogRecord(t, line)
		message, ok := record["message"].(string)
		if !ok || seen[message] {
			t.Fatalf("JSON log contains an invalid or duplicate message: %v", record["message"])
		}
		seen[message] = true
	}
	if len(seen) != workers {
		t.Errorf("wrote %d records, want %d", len(seen), workers)
	}
	for index := range workers {
		if message := fmt.Sprintf("worker %d", index); !seen[message] {
			t.Errorf("missing log record for %q", message)
		}
	}
}

func TestLoggerWarn(t *testing.T) {
	testCoreLogLevel(t, (*Logger).Warn, LevelWarn, LevelError)
}

func TestLoggerError(t *testing.T) {
	testCoreLogLevel(t, (*Logger).Error, LevelError, "")
}

type coreSyncCase struct {
	name       string
	prepare    func(*testing.T) *Logger
	syncError  error
	wantCalls  int
	wantDetail string
}

func TestLoggerSync(t *testing.T) {
	failure := errors.New("fixture sync failure")
	tests := []coreSyncCase{
		// nil 接收者代表未取得实例，验证同步操作安全返回初始化错误。
		{name: "nil logger", prepare: func(*testing.T) *Logger { return nil }, wantDetail: "not initialized"},
		// 零值没有绑定输出，验证同步操作不会隐式创建目标。
		{name: "zero logger", prepare: func(*testing.T) *Logger { return &Logger{} }, wantDetail: "not initialized"},
		// 纯 Writer 没有 Sync 方法，验证直接成功返回。
		{
			name: "writer without sync", prepare: func(t *testing.T) *Logger {
				return newCoreLogger(t, LevelInfo, io.Discard)
			},
		},
		// 输出器支持 Sync，验证调用一次且不接管输出器的关闭。
		{name: "delegates sync", wantCalls: 1},
		// 输出器返回错误时，验证同步失败保留原始错误链并且不重试。
		{name: "preserves sync error", syncError: failure, wantCalls: 1, wantDetail: "sync JSON logs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &coreLogOutput{syncError: test.syncError}
			var instance *Logger
			if test.prepare == nil {
				instance = newCoreLogger(t, LevelInfo, output)
			} else {
				instance = test.prepare(t)
			}
			assertCoreLoggerError(t, instance.Sync(), test.syncError, test.wantDetail)
			if output.syncCalls != test.wantCalls || output.closeCalls != 0 || output.writeCalls != 0 {
				t.Errorf("calls (sync=%d, close=%d, write=%d), want (%d, 0, 0)",
					output.syncCalls, output.closeCalls, output.writeCalls, test.wantCalls)
			}
		})
	}
}

type coreWriteFailureCase struct {
	name       string
	prepare    func(*Logger) *Logger
	fields     []Fields
	write      func([]byte) (int, error)
	wantErr    error
	wantDetail string
	wantWrites int
}

func TestLoggerWrite(t *testing.T) {
	encoderError := errors.New("fixture encoding failure")
	writeError := errors.New("fixture output failure")
	tests := []coreWriteFailureCase{
		// 使用 nil 实例，验证内部写入在访问编码器前失败。
		{name: "nil logger", prepare: func(*Logger) *Logger { return nil }, wantDetail: "not initialized"},
		// 输出器已配置但编码器缺失，验证不会直接写入未经编码的内容。
		{
			name: "missing encoder", prepare: func(instance *Logger) *Logger {
				instance.encoder = nil
				return instance
			},
			wantDetail: "not initialized",
		},
		// 编码器已配置但输出器缺失，验证不会尝试写入未初始化的目标。
		{
			name: "missing output", prepare: func(instance *Logger) *Logger {
				instance.output = nil
				return instance
			},
			wantDetail: "not initialized",
		},
		// 事件名拼错时无法形成约定的记录，验证参数校验先于输出。
		{name: "invalid event", fields: []Fields{{Event: "start"}}, wantDetail: "event must be started or finished"},
		// 模拟 JSON 编码器失败，验证原始原因保留且输出器未收到正文。
		{
			name: "encoding failure", prepare: func(instance *Logger) *Logger {
				instance.encoder = failingCoreLogEncoder{Encoder: instance.encoder, cause: encoderError}
				return instance
			},
			wantErr: encoderError, wantDetail: "encode JSON log",
		},
		// Writer 只接受部分字节却返回 nil，验证补齐短写错误并停止重试。
		{
			name: "short write without error", write: func(data []byte) (int, error) { return len(data) - 1, nil },
			wantErr: io.ErrShortWrite, wantDetail: "write JSON log", wantWrites: 1,
		},
		// Writer 报告部分写入和原始原因，验证保留其原因且不追加第二次输出。
		{
			name: "partial write with error", write: func(data []byte) (int, error) { return len(data) - 1, writeError },
			wantErr: writeError, wantDetail: "write JSON log", wantWrites: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &coreLogOutput{write: test.write}
			instance := newCoreLogger(t, LevelInfo, output)
			if test.prepare != nil {
				instance = test.prepare(instance)
			}
			err := instance.write(t.Context(), zapcore.InfoLevel, "Fixture message", test.fields)
			assertCoreLoggerError(t, err, test.wantErr, test.wantDetail)
			if output.writeCalls != test.wantWrites {
				t.Errorf("writer called %d times, want %d", output.writeCalls, test.wantWrites)
			}
		})
	}
}

type validateCoreFieldsCase struct {
	name       string
	fields     Fields
	wantDetail string
}

func TestFieldsValidate(t *testing.T) {
	tests := []validateCoreFieldsCase{
		// 普通日志省略所有可选字段时符合约定。
		{name: "zero fields"},
		// 操作刚开始时尚无耗时，验证 started 可以独立提供。
		{name: "started event", fields: Fields{Event: EventStarted}},
		// 完成事件配显式零耗时，验证零值与未采集的 nil 都可接受。
		{name: "finished with zero duration", fields: Fields{Event: EventFinished, DurationMS: new(float64(0))}},
		// 浮点数可表达亚毫秒耗时，验证合法小数不会被拒绝。
		{name: "fractional duration", fields: Fields{DurationMS: new(0.5)}},
		// 事件大小写与声明不同，验证不会将任意字符串视为生命周期事件。
		{name: "unknown event", fields: Fields{Event: "FINISHED"}, wantDetail: "event must be started or finished"},
		// 用小于零的有限值隔离耗时的非负约束。
		{name: "negative duration", fields: Fields{DurationMS: new(-0.5)}, wantDetail: "duration_ms must be finite and non-negative"},
		// NaN 不满足 JSON 数值要求，验证在编码前拒绝。
		{name: "NaN duration", fields: Fields{DurationMS: new(math.NaN())}, wantDetail: "duration_ms must be finite and non-negative"},
		// 正无穷虽非负也无法作为 JSON 数值，验证必须有限。
		{name: "infinite duration", fields: Fields{DurationMS: new(math.Inf(1))}, wantDetail: "duration_ms must be finite and non-negative"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCoreLoggerError(t, test.fields.validate(), nil, test.wantDetail)
		})
	}
}

func TestLogCallerWithoutFrame(t *testing.T) {
	// 在浅调用栈直接请求日志调用位置，验证没有可用帧时返回未定义的位置。
	result := make(chan zapcore.EntryCaller, 1)
	go func() { result <- logCaller() }()
	select {
	case caller := <-result:
		if caller.Defined {
			t.Errorf("logCaller() = %v, want undefined", caller)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logCaller() did not return")
	}
}

type coreLogOutput struct {
	write      func([]byte) (int, error)
	syncError  error
	writeCalls int
	syncCalls  int
	closeCalls int
}

type failingCoreLogEncoder struct {
	zapcore.Encoder
	cause error
}

func coreLoggerConfig(level Level) Config {
	return Config{ServiceName: "profile-service", InstanceID: "instance-a", MinLevel: level}
}

func newCoreLogger(t *testing.T, level Level, output io.Writer) *Logger {
	t.Helper()
	instance, err := New(coreLoggerConfig(level), env.Config{Environment: "test"}, output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return instance
}

func testCoreLogLevel(t *testing.T, write func(*Logger, context.Context, string, ...Fields), level, nextLevel Level) {
	t.Helper()
	tests := []coreLogLevelCase{
		// 调用级别等于配置阈值，验证阈值包含本级且输出使用约定的大写级别。
		{name: "at minimum level", minimum: level, wantWritten: true},
		// 在未过滤的调用中传两份 Fields，验证该入口向 stderr 报告错误且不写入 JSON。
		{name: "invalid fields", minimum: level, fields: []Fields{{}, {}}, wantFailure: "logger accepts at most one Fields value"},
	}
	if nextLevel != "" {
		// 级别低于下一档阈值时传同样的非法参数，验证过滤发生在字段校验及错误报告之前。
		tests = append(tests, coreLogLevelCase{name: "below minimum level", minimum: nextLevel, fields: []Fields{{}, {}}})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			instance := newCoreLogger(t, test.minimum, &output)
			const message = "Level fixture"
			fallback := captureLoggerFallbackOutput(t, func() { write(instance, t.Context(), message, test.fields...) })
			if test.wantFailure == "" && fallback != "" || test.wantFailure != "" && !strings.Contains(fallback, test.wantFailure) {
				t.Errorf("stderr = %q, want %q", fallback, test.wantFailure)
			}
			if !test.wantWritten {
				if output.Len() != 0 {
					t.Errorf("wrote unexpected JSON record %q", output.String())
				}
				return
			}
			record := decodeCoreLogRecord(t, output.Bytes())
			if record["level"] != string(level) || record["message"] != message {
				t.Errorf("JSON level/message = %v/%v, want %s/%s", record["level"], record["message"], level, message)
			}
		})
	}
}

func coreHTTPLogContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = tracing.WithTraceID(ctx, "0123456789abcdef0123456789abcdef")
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("parse test TraceId: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("parse test SpanId: %v", err)
	}
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
	ctx = WithHTTPOperation(ctx, "GET", "/profiles/:id")
	return ctx
}

func writeCoreInfo(t *testing.T, instance *Logger, ctx context.Context, message string, fields []Fields) string {
	t.Helper()
	_, file, line, ok := runtime.Caller(0)
	instance.Info(ctx, message, fields...)
	if !ok {
		t.Fatal("could not determine expected caller")
	}
	return filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file) + ":" + strconv.Itoa(line+1)
}

func decodeCoreLogRecord(t *testing.T, data []byte) map[string]any {
	t.Helper()
	if len(data) == 0 || data[len(data)-1] != '\n' || bytes.Count(data, []byte("\n")) != 1 {
		t.Fatalf("expected exactly one JSONL record, got %q", data)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode log record: %v", err)
	}
	return record
}

func assertCoreLoggerError(t *testing.T, err, cause error, detail string) {
	t.Helper()
	if cause == nil && detail == "" {
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		return
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Errorf("error = %v, want cause %v", err, cause)
	}
	if detail != "" && (err == nil || !strings.Contains(err.Error(), detail)) {
		t.Errorf("error = %v, want message containing %q", err, detail)
	}
}

func (output *coreLogOutput) Write(data []byte) (int, error) {
	output.writeCalls++
	if output.write != nil {
		return output.write(data)
	}
	return len(data), nil
}

func (output *coreLogOutput) Sync() error {
	output.syncCalls++
	return output.syncError
}

func (output *coreLogOutput) Close() error {
	output.closeCalls++
	return nil
}

func (encoder failingCoreLogEncoder) EncodeEntry(zapcore.Entry, []zapcore.Field) (*buffer.Buffer, error) {
	return nil, encoder.cause
}
