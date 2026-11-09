// Package logger 提供统一日志结构、操作上下文识别和同步 JSON 写入能力。
// Logger 支持独立构造和启动时发布共享实例；四级打印方法在内部处理日志写入故障。
package logger

import "time"

// Level 表示日志级别，使用本包声明的固定值。
type Level string

const (
	// LevelDebug 表示额外的调试信息。
	LevelDebug Level = "DEBUG"
	// LevelInfo 表示正常的操作过程或结果。
	LevelInfo Level = "INFO"
	// LevelWarn 表示需要关注的异常情况。
	LevelWarn Level = "WARN"
	// LevelError 表示操作故障。
	LevelError Level = "ERROR"
)

// Component 按代码职责标识日志来源，不包含具体业务或供应商分类。
type Component string

const (
	// ComponentHTTPEntry 表示 HTTP 请求入口及响应处理。
	ComponentHTTPEntry Component = "http_entry"
	// ComponentController 表示领域 Controller。
	ComponentController Component = "controller"
	// ComponentService 表示业务 Service。
	ComponentService Component = "service"
	// ComponentClient 表示外部依赖调用组件。
	ComponentClient Component = "client"
	// ComponentRepository 表示数据库访问 Repository。
	ComponentRepository Component = "repository"
)

// Event 表示操作的生命周期节点，普通说明性日志可以不提供。
type Event string

const (
	// EventStarted 表示操作开始。
	EventStarted Event = "started"
	// EventFinished 表示操作结束，不表示操作一定成功。
	EventFinished Event = "finished"
)

// Fields 表示调用方可选提供的单条日志信息，不允许覆盖公共自动字段。
// 错误文本由调用方预先受控处理；调用期间保持只读，Logger 不保存本结构或其中的指针。
type Fields struct {
	Event      Event
	DurationMS *float64
	StatusCode *int
	Error      string
}

// Record 表示一条单行 JSON 日志的字段结构，不保存 Context、DTO 或原始错误对象。
// 本结构声明本身不生成字段、不识别操作、不脱敏或输出日志，也不自动校验枚举值。
type Record struct {
	// Timestamp 是日志发生时间，由公共 Logger 以 UTC 填充。
	Timestamp time.Time `json:"timestamp"`
	// Level 是调用方选择的日志级别。
	Level Level `json:"level"`
	// Message 是本条日志的英文说明。
	Message string `json:"message"`
	// Component 从当前操作 Context 读取代码职责类别；不适用或无法识别时省略。
	Component Component `json:"component,omitempty"`
	// Event 是当前操作的生命周期节点；普通说明性日志可以省略。
	Event Event `json:"event,omitempty"`
	// TraceID 来自当前 Context；无请求链路标识的进程日志省略。
	TraceID string `json:"trace_id,omitempty"`
	// ServiceName 来自应用配置，通过 Logger 构造参数注入。
	ServiceName string `json:"service_name"`
	// Environment 复用构造时传入的 env.Config.Environment，日志配置不重复声明。
	Environment string `json:"environment"`
	// InstanceID 由调用方在进程启动时生成一次，通过 Logger 构造参数注入。
	InstanceID string `json:"instance_id"`
	// Caller 是实际打印日志的代码位置；无法取得时省略。
	Caller string `json:"caller,omitempty"`
	// Operation 是框架在操作入口识别后放入派生 Context 的稳定操作标识。
	// 同一操作的开始、结束及正常、错误路径使用相同值；不适用或无法识别时省略。
	Operation string `json:"operation,omitempty"`
	// DurationMS 是操作实际耗时，单位为毫秒；nil 表示未采集，指向 0 表示实际为 0。
	DurationMS *float64 `json:"duration_ms,omitempty"`
	// StatusCode 是实际取得的 HTTP 状态码；无 HTTP 状态时使用 nil。
	StatusCode *int `json:"status_code,omitempty"`
	// Error 仅保存已受控处理的错误文本；没有错误信息时省略。
	// 本字段不接收原始 error、SDK 异常对象、密钥或完整请求/响应正文。
	Error string `json:"error,omitempty"`
}
