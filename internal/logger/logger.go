package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"time"

	"go-service-template/env"
	"go-service-template/internal/traceid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger 使用 Zap JSON 编码器同步输出日志，构造后允许并发复用。
// 配置和编码器保持只读，不保存请求 Context 或单次日志的 Fields。
type Logger struct {
	config     Config
	deployment env.Config
	minLevel   zapcore.Level
	encoder    zapcore.Encoder
	output     zapcore.WriteSyncer
}

// New 按注入配置和部署环境创建独立 Logger，不生成实例标识或修改全局日志器。
// deployment 复用已有 env.Config，构造时按值保存；本函数不调用 env.Current 或重新读取环境变量。
// output 由调用方创建和管理，Logger 仅写入及同步，不打开或关闭输出器。
func New(config Config, deployment env.Config, output io.Writer) (*Logger, error) {
	minLevel, err := config.validate()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(deployment.Environment) == "" {
		return nil, errors.New("logger environment is required")
	}
	if output == nil {
		return nil, errors.New("logger output is required")
	}
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:      "timestamp",
		LevelKey:     "level",
		MessageKey:   "message",
		CallerKey:    "caller",
		LineEnding:   zapcore.DefaultLineEnding,
		EncodeLevel:  zapcore.CapitalLevelEncoder,
		EncodeTime:   zapcore.TimeEncoderOfLayout(time.RFC3339Nano),
		EncodeCaller: zapcore.ShortCallerEncoder,
	})
	return &Logger{
		config:     config,
		deployment: deployment,
		minLevel:   minLevel,
		encoder:    encoder,
		output:     zapcore.Lock(&checkedWriteSyncer{writer: output}),
	}, nil
}

// Debug 输出调试日志。fields 可省略或提供一项；低于最低级别时直接返回 nil。
// 各级方法均返回参数、编码或写入错误，不通过 Logger 递归记录自身故障。
func (l *Logger) Debug(ctx context.Context, message string, fields ...Fields) error {
	return l.write(ctx, zapcore.DebugLevel, message, fields)
}

// Info 输出普通日志，可选字段及错误处理约定与 Debug 相同。
func (l *Logger) Info(ctx context.Context, message string, fields ...Fields) error {
	return l.write(ctx, zapcore.InfoLevel, message, fields)
}

// Warn 输出警告日志，可选字段及错误处理约定与 Debug 相同。
func (l *Logger) Warn(ctx context.Context, message string, fields ...Fields) error {
	return l.write(ctx, zapcore.WarnLevel, message, fields)
}

// Error 输出故障日志，可选字段及错误处理约定与 Debug 相同。
func (l *Logger) Error(ctx context.Context, message string, fields ...Fields) error {
	return l.write(ctx, zapcore.ErrorLevel, message, fields)
}

// Sync 转交输出器的 Sync 方法；输出器未提供该方法时不执行同步操作。
// 外部缓冲器的 Flush 和输出器的 Close 仍由调用方负责。
func (l *Logger) Sync() error {
	if l == nil || l.output == nil {
		return errors.New("logger is not initialized")
	}
	if err := l.output.Sync(); err != nil {
		return fmt.Errorf("sync JSON logs: %w", err)
	}
	return nil
}

func (l *Logger) write(ctx context.Context, level zapcore.Level, message string, fields []Fields) error {
	if l == nil || l.encoder == nil || l.output == nil {
		return errors.New("logger is not initialized")
	}
	if level < l.minLevel {
		return nil
	}
	if len(fields) > 1 {
		return errors.New("logger accepts at most one Fields value")
	}
	var optional Fields
	if len(fields) == 1 {
		optional = fields[0]
	}
	if err := optional.validate(); err != nil {
		return err
	}
	caller := logCaller()
	record := Record{
		Timestamp:   time.Now().UTC(),
		Level:       Level(level.CapitalString()),
		Message:     message,
		Event:       optional.Event,
		ServiceName: l.config.ServiceName,
		Environment: l.deployment.Environment,
		InstanceID:  l.config.InstanceID,
		DurationMS:  optional.DurationMS,
		StatusCode:  optional.StatusCode,
		Error:       optional.Error,
	}
	if caller.Defined {
		record.Caller = caller.TrimmedPath()
	}
	if ctx != nil {
		// 即使 Context 已取消，排障日志仍需保留原链路标识，不因取消而跳过输出。
		record.TraceID = traceid.FromContext(ctx)
	}
	entry := zapcore.Entry{
		Time:    record.Timestamp,
		Level:   level,
		Message: record.Message,
		Caller:  caller,
	}
	// EncodeEntry 可并发调用；自动字段来自 Record，调用方无法提供同名字段覆盖。
	buffer, err := l.encoder.EncodeEntry(entry, recordFields(record))
	if err != nil {
		return fmt.Errorf("encode JSON log: %w", err)
	}
	defer buffer.Free()
	if _, err := l.output.Write(buffer.Bytes()); err != nil {
		// 不重试，避免已部分写出的 JSON 与重复正文混合。
		return fmt.Errorf("write JSON log: %w", err)
	}
	return nil
}

func (fields Fields) validate() error {
	if fields.Event != "" && fields.Event != EventStarted && fields.Event != EventFinished {
		return errors.New("logger event must be started or finished")
	}
	if fields.DurationMS != nil {
		duration := *fields.DurationMS
		if duration < 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return errors.New("logger duration_ms must be finite and non-negative")
		}
	}
	return nil
}

func recordFields(record Record) []zap.Field {
	fields := []zap.Field{
		zap.String("service_name", record.ServiceName),
		zap.String("environment", record.Environment),
		zap.String("instance_id", record.InstanceID),
	}
	if record.TraceID != "" {
		fields = append(fields, zap.String("trace_id", record.TraceID))
	}
	if record.Component != "" {
		fields = append(fields, zap.String("component", string(record.Component)))
	}
	if record.Operation != "" {
		fields = append(fields, zap.String("operation", record.Operation))
	}
	if record.Event != "" {
		fields = append(fields, zap.String("event", string(record.Event)))
	}
	if record.DurationMS != nil {
		fields = append(fields, zap.Float64("duration_ms", *record.DurationMS))
	}
	if record.StatusCode != nil {
		fields = append(fields, zap.Int("status_code", *record.StatusCode))
	}
	if record.Error != "" {
		fields = append(fields, zap.String("error", record.Error))
	}
	return fields
}

func logCaller() zapcore.EntryCaller {
	var pcs [1]uintptr
	// 跳过 runtime.Callers、本方法、write 及 Debug/Info/Warn/Error，保留实际调用日志的位置。
	if runtime.Callers(4, pcs[:]) == 0 {
		return zapcore.EntryCaller{}
	}
	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	return zapcore.NewEntryCaller(frame.PC, frame.File, frame.Line, frame.Function != "")
}

// checkedWriteSyncer 补齐短写检查，并保留调用方输出器的同步能力。
// 外层 zapcore.Lock 对整行 Write 和 Sync 加锁，不会并发调用同一输出器。
type checkedWriteSyncer struct {
	writer io.Writer
}

func (w *checkedWriteSyncer) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	return written, err
}

func (w *checkedWriteSyncer) Sync() error {
	if syncer, ok := w.writer.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}
