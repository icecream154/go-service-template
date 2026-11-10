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
	"go-service-template/internal/tracing"

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
	// 1. 校验日志专属配置，并将公开的字符串级别转换为 Zap 的比较级别。
	// 服务名称和实例标识缺失时不能构造可区分来源的日志；校验失败原样返回，不发布部分实例。
	minLevel, err := config.validate()
	if err != nil {
		// 1.1. 原样返回配置校验错误，此时尚未创建编码器或接触输出器。
		return nil, err
	}
	// 2. 确认传入的部署环境名称可用。这里只检查必填条件，环境白名单由 env 包维护；
	// TrimSpace 仅用于判断空白值，不修改调用方提供的部署配置，也不重新读取进程环境变量。
	if strings.TrimSpace(deployment.Environment) == "" {
		// 2.1. 缺少环境时直接拒绝构造，不把空环境当成某个默认部署环境。
		return nil, errors.New("logger environment is required")
	}
	// 3. 要求调用方提供实际输出目标，避免构造成功后才因缺少 Writer 无法写入。
	// 这里不替调用方选择 stdout、文件或其他目标，输出器的创建和关闭责任也不发生转移。
	// 此处只检查 Writer 接口是否为空，不验证动态值是否为 typed-nil 或底层资源是否可写。
	if output == nil {
		// 3.1. 输出接口为空时无法写日志，返回构造错误而非自动丢弃日志。
		return nil, errors.New("logger output is required")
	}
	// 4. 按公共字段契约创建 JSON 编码器；后续只并发调用 EncodeEntry，不修改其配置。
	encoder := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		// 4.1. 固定 Zap Entry 内置字段的 JSON 名称，使所有日志使用相同的查询字段。
		TimeKey:    "timestamp",
		LevelKey:   "level",
		MessageKey: "message",
		CallerKey:  "caller",
		// 4.2. 每条记录以换行结束，便于运行环境按 JSONL 保存和按行采集。
		LineEnding: zapcore.DefaultLineEnding,
		// 4.3. 级别统一为大写；时间保留时区和可用精度；调用位置使用短路径及行号。
		// UTC 时间在 write 中生成，编码器这里只负责格式化，不改变操作计时或调用位置来源。
		EncodeLevel:  zapcore.CapitalLevelEncoder,
		EncodeTime:   zapcore.TimeEncoderOfLayout(time.RFC3339Nano),
		EncodeCaller: zapcore.ShortCallerEncoder,
	})
	// 5. 保存配置值副本、最低级别和只读编码器，返回一个独立且可并发复用的 Logger。
	// checkedWriteSyncer 补齐短写检查和同步转交；最外层锁覆盖整次 Write/Sync，串行化同一 Logger 的输出。
	return &Logger{
		config:     config,
		deployment: deployment,
		minLevel:   minLevel,
		encoder:    encoder,
		output:     zapcore.Lock(&checkedWriteSyncer{writer: output}),
	}, nil
}

// Debug 输出调试日志。fields 可省略或提供一项；低于最低级别时不产生输出。
// 各级打印方法不返回错误，参数、编码或写入故障在 Logger 内部通过独立 stderr 报告。
func (l *Logger) Debug(ctx context.Context, message string, fields ...Fields) {
	// 1. 固定本次级别为 DEBUG，复用原有字段填充、级别过滤及同步写入流程。
	// 直接调用 write，保持调用栈层级不变，使 caller 继续指向业务中实际打印的位置。
	if err := l.write(ctx, zapcore.DebugLevel, message, fields); err != nil {
		// 1.1. 日志自身故障统一交给私有兜底，不要求业务方处理或修改其执行结果。
		reportWriteFailure(ctx, err)
	}
}

// Info 输出普通日志，可选字段及错误处理约定与 Debug 相同。
func (l *Logger) Info(ctx context.Context, message string, fields ...Fields) {
	// 1. 固定本次级别为 INFO，复用公共写入流程，自动字段及调用位置遵循相同规则。
	if err := l.write(ctx, zapcore.InfoLevel, message, fields); err != nil {
		// 1.1. 仅在发生日志故障时向独立 stderr 报告，不递归调用 Logger 或返回业务错误。
		reportWriteFailure(ctx, err)
	}
}

// Warn 输出警告日志，可选字段及错误处理约定与 Debug 相同。
func (l *Logger) Warn(ctx context.Context, message string, fields ...Fields) {
	// 1. 固定本次级别为 WARN，只执行日志写入，不触发业务重试、终止进程或改变 Context。
	if err := l.write(ctx, zapcore.WarnLevel, message, fields); err != nil {
		// 1.1. 写入失败由 Logger 内部独立兜底，调用方仍按原业务路径继续处理。
		reportWriteFailure(ctx, err)
	}
}

// Error 输出故障日志，可选字段及错误处理约定与 Debug 相同。
func (l *Logger) Error(ctx context.Context, message string, fields ...Fields) {
	// 1. 固定本次级别为 ERROR，表达业务故障级别；写入流程与其他日志级别一致。
	if err := l.write(ctx, zapcore.ErrorLevel, message, fields); err != nil {
		// 1.1. 日志本身的故障只走独立 stderr，不追加另一条业务错误日志或修改业务返回。
		reportWriteFailure(ctx, err)
	}
}

// Sync 转交输出器的 Sync 方法；输出器未提供该方法时不执行同步操作。
// 外部缓冲器的 Flush 和输出器的 Close 仍由调用方负责。
func (l *Logger) Sync() error {
	// 1. 检查接收者及输出器是否已构造，避免空指针调用；零值 Logger 不会隐式创建输出目标。
	if l == nil || l.output == nil {
		return errors.New("logger is not initialized")
	}
	// 2. 在输出器的锁保护下转交同步操作。包装错误保留原始原因，便于 errors.Is/As 判断底层故障。
	// 此处只同步已有输出器，不调用 Close，也不接管调用方额外缓冲器的 Flush。
	if err := l.output.Sync(); err != nil {
		return fmt.Errorf("sync JSON logs: %w", err)
	}
	// 3. 同步完成，或底层只有 io.Writer 而无需同步时，统一返回 nil。
	return nil
}

// write 是四级日志方法的私有写入流程，负责字段来源、同步编码输出及内部错误返回。
// 返回错误仅供打印方法执行统一兜底，不暴露给业务调用方。
func (l *Logger) write(ctx context.Context, level zapcore.Level, message string, fields []Fields) error {
	// 1. 确认 Logger 的编码器和输出器均存在；不完整实例无法保证字段格式和写入行为，直接返回错误。
	if l == nil || l.encoder == nil || l.output == nil {
		return errors.New("logger is not initialized")
	}
	// 2. 尽早过滤低于最低级别的日志，跳过可选字段校验、调用位置采集及 JSON 编码开销。
	// 被配置过滤属于正常结果，返回 nil，并不表示已产生一条输出记录。
	if level < l.minLevel {
		return nil
	}
	// 3. 可选字段只接受零份或一份，避免多份 Fields 的同名字段出现不明确的覆盖顺序。
	if len(fields) > 1 {
		return errors.New("logger accepts at most one Fields value")
	}
	// 4. 准备本次调用独有的可选字段，未提供时使用零值，不修改或保存调用方的 Fields。
	var optional Fields
	if len(fields) == 1 {
		// 4.1. 复制唯一一份 Fields；其中数值指针只在本次编码期间读取，调用方应保持其只读。
		optional = fields[0]
	}
	// 4.2. 检查事件枚举和耗时数值，失败时不输出格式或语义不符合契约的记录。
	if err := optional.validate(); err != nil {
		return err
	}
	// 5. 分别取得实际打印位置和操作作用域。caller 来自本次调用栈；operation 来自入口保存的 Context。
	// 辅助函数可拥有自己的 caller，同时继续使用其所属入口的 operation，不在这里重新推断操作名称。
	caller := logCaller()
	operation := operationFromContext(ctx)
	// 6. 将不同来源的数据组合为本次 Record，公共字段由 Logger 提供，调用方只能补充 Fields 中的项目。
	record := Record{
		// 6.1. 写日志时生成 UTC 时间，并使用本次级别和原始消息；不更改调用方的消息内容。
		Timestamp: time.Now().UTC(),
		Level:     Level(level.CapitalString()),
		Message:   message,
		// 6.2. 读取最近的操作作用域；未建立或识别失败时保持零值，编码时省略这两个字段。
		Component: operation.component,
		Operation: operation.name,
		// 6.3. 事件由本次调用提供，普通说明性日志可以留空；写入方法不自动产生生命周期事件。
		Event: optional.Event,
		// 6.4. 公共来源信息取自构造时保存的值，日志逐条写入时不读取全局配置或环境变量。
		ServiceName: l.config.ServiceName,
		Environment: l.deployment.Environment,
		InstanceID:  l.config.InstanceID,
		// 6.5. 可选结果信息保持调用方提供的值；非 nil 数值指针即使指向 0，也应写入 JSON。
		DurationMS: optional.DurationMS,
		StatusCode: optional.StatusCode,
		Error:      optional.Error,
	}
	// 7. 仅在成功取得调用帧时填充可读位置；无法取得时保持空值，避免输出虚构的来源。
	if caller.Defined {
		record.Caller = caller.TrimmedPath()
	}
	// 8. 从追踪公共入口读取本次日志的关联标识，不在日志层创建 Span 或生成标识。
	// 已取消 Context 中的值仍可读取，取消之后的排障日志也应保留原链路和执行节点。
	if ctx != nil {
		// 8.1. TraceId 优先使用有效标准 SpanContext，缺失时沿用入口保存的兼容值。
		record.TraceID = tracing.TraceID(ctx)
		// 8.2. SpanId 只取有效标准节点，未采样时仍保留；只有兼容 TraceId 时为空，编码时省略。
		record.SpanID = tracing.SpanID(ctx)
	}
	// 9. 将时间、级别、消息和调用位置交给 Zap Entry 的专用编码器，其他字段由 recordFields 按需追加。
	// 这些字段不会作为额外字段重复传入，避免一条 JSON 内出现同名键。
	entry := zapcore.Entry{
		Time:    record.Timestamp,
		Level:   level,
		Message: record.Message,
		Caller:  caller,
	}
	// 10. 并发安全地编码完整 JSON 行；共享编码器保持只读，本条记录使用独立的临时字节缓冲区。
	buffer, err := l.encoder.EncodeEntry(entry, recordFields(record))
	if err != nil {
		// 10.1. 编码失败时尚未写入输出器；包装并返回原始错误，不通过自身日志方法递归报告。
		return fmt.Errorf("encode JSON log: %w", err)
	}
	// 10.2. 成功获得缓冲区后立即登记归还，无论后续写入成功或失败都释放到 Zap 缓冲池。
	defer buffer.Free()
	// 11. 将完整 JSON 行交给加锁的输出器，确保同一 Logger 的并发调用不会把各条正文交错写入。
	if _, err := l.output.Write(buffer.Bytes()); err != nil {
		// 11.1. 输出器可能已写入部分正文，故障或短写都原样保留错误链；不重试，以免重复拼接 JSON。
		return fmt.Errorf("write JSON log: %w", err)
	}
	// 12. 本次写入未报告错误，返回 nil；这只表示输出器接受了正文，不承诺远端采集或持久化已完成。
	return nil
}

// validate 检查可选字段自身的公共格式，不检查业务结果，也不替调用方脱敏错误文本。
func (fields Fields) validate() error {
	// 1. 空事件表示普通说明性日志；有事件时只允许公共规范声明的生命周期节点。
	if fields.Event != "" && fields.Event != EventStarted && fields.Event != EventFinished {
		return errors.New("logger event must be started or finished")
	}
	// 2. 只检查已经采集的耗时。nil 与数值 0 表示不同含义，不能通过数值真假来决定是否输出。
	if fields.DurationMS != nil {
		// 2.1. 读取本次耗时值，要求它能以非负 JSON 数值表达；NaN、无穷和负值都违反毫秒耗时契约。
		duration := *fields.DurationMS
		if duration < 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return errors.New("logger duration_ms must be finite and non-negative")
		}
	}
	// 3. 可选字段符合当前格式约定；HTTP 状态和受控错误文本的实际来源仍由对应操作负责。
	return nil
}

// recordFields 将 Record 的附加属性转换为 Zap 字段，保持可选项的省略与零值语义。
func recordFields(record Record) []zap.Field {
	// 1. 三项进程来源信息在构造时已确认必填，因此每条日志都输出，便于跨环境和实例筛选。
	// 时间、级别、消息和调用位置由 Zap Entry 编码，不在这里重复添加。
	fields := []zap.Field{
		zap.String("service_name", record.ServiceName),
		zap.String("environment", record.Environment),
		zap.String("instance_id", record.InstanceID),
	}
	// 2. 仅在 Context 确实提供链路标识时追加；进程日志省略该字段，不使用空字符串制造归属。
	if record.TraceID != "" {
		fields = append(fields, zap.String("trace_id", record.TraceID))
	}
	// 3. 仅在标准 Context 确实提供有效节点时追加 SpanId；不输出空字符串或推导虚构节点。
	// write 从相同 Context 读取两种标识，因此本字段存在时 TraceId 也会指向同一条标准链路。
	if record.SpanID != "" {
		fields = append(fields, zap.String("span_id", record.SpanID))
	}
	// 4. 有明确操作作用域时输出组件类别；识别失败的空值不进入 JSON。
	if record.Component != "" {
		fields = append(fields, zap.String("component", string(record.Component)))
	}
	// 5. 有明确操作标识时输出其稳定名称，名称与开始、结束或成功、失败结果无关。
	if record.Operation != "" {
		fields = append(fields, zap.String("operation", record.Operation))
	}
	// 6. 仅生命周期日志需要事件字段；普通过程说明可以省略。
	if record.Event != "" {
		fields = append(fields, zap.String("event", string(record.Event)))
	}
	// 7. 指针非 nil 表示实际采集过耗时，包含数值 0；输出 JSON 数值而非字符串。
	if record.DurationMS != nil {
		fields = append(fields, zap.Float64("duration_ms", *record.DurationMS))
	}
	// 8. 指针非 nil 表示取得了 HTTP 状态，避免把未提供状态的业务或数据库日志默认写成 200。
	if record.StatusCode != nil {
		fields = append(fields, zap.Int("status_code", *record.StatusCode))
	}
	// 9. 仅追加已有受控错误文本；不在编码阶段展开 Go error、SDK 响应或其内部原因。
	if record.Error != "" {
		fields = append(fields, zap.String("error", record.Error))
	}
	// 10. 返回本条记录独有的字段切片，交由调用方与 Zap Entry 一起编码，不缓存请求内容。
	return fields
}

// logCaller 返回真正调用四级日志方法的代码位置，不承担操作入口识别职责。
func logCaller() zapcore.EntryCaller {
	// 1. 只需要一个逻辑调用位置，使用定长存储取得目标帧的程序计数器，避免采集整个调用栈。
	var pcs [1]uintptr
	// 2. skip=4 依次跳过 runtime.Callers、logCaller、write 和 Debug/Info/Warn/Error，
	// 下一帧才是业务代码或公共组件真正写日志的位置；无法取得时返回未定义位置，由编码器省略。
	if runtime.Callers(4, pcs[:]) == 0 {
		return zapcore.EntryCaller{}
	}
	// 3. 使用 CallersFrames 处理返回地址调整和内联帧，再提取调用位置，而非直接解释原始计数器。
	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	// 4. 将文件及行号交给 Zap 调用位置编码器；函数符号为空时标记为未定义，不猜测代码来源。
	return zapcore.NewEntryCaller(frame.PC, frame.File, frame.Line, frame.Function != "")
}

// checkedWriteSyncer 补齐短写检查，并保留调用方输出器的同步能力。
// 外层 zapcore.Lock 对整行 Write 和 Sync 加锁，串行化同一 Logger 对输出器的访问。
type checkedWriteSyncer struct {
	writer io.Writer
}

func (w *checkedWriteSyncer) Write(data []byte) (int, error) {
	// 1. 保持一次完整正文对应一次底层 Write，使用调用方选定的输出器并保留其返回字节数和错误。
	written, err := w.writer.Write(data)
	// 2. 补齐 Writer 未写满却未报告错误的情况，让上层能够识别短写；已有底层错误不被替换。
	if err == nil && written != len(data) {
		// 2.1. 将未写满且没有底层错误的结果明确表达为短写，原始字节数仍保留给上层。
		err = io.ErrShortWrite
	}
	// 3. 返回真实写入数量及最终错误，不主动补写或重试；外层锁负责串行化并发写入。
	return written, err
}

func (w *checkedWriteSyncer) Sync() error {
	// 1. 通过可选接口保留底层输出器的同步能力；有 Sync 时直接转交，成功或失败都原样返回。
	if syncer, ok := w.writer.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	// 2. 普通 io.Writer 没有同步契约，返回 nil 表示无需执行；不猜测 Flush 方法或关闭调用方资源。
	return nil
}
