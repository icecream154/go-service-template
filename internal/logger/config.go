package logger

import (
	"errors"
	"strings"

	"go.uber.org/zap/zapcore"
)

// Config 通过构造参数注入日志专属信息，部署环境复用单独传入的 env.Config。
// 本配置不读取配置文件或环境变量。
// 字段必须显式提供；构造后按值保存，修改原配置不会影响已有 Logger。
type Config struct {
	// ServiceName 是应用配置提供的稳定服务名称，构造 Logger 时复制到每条日志的公共信息中。
	// 本包不从工作目录、模块路径或环境变量推断该名称。
	ServiceName string
	// InstanceID 是装配方为当前进程提供的实例标识，同一进程内应保持一致。
	// 本包只校验和使用该值，不在创建 Logger 时重新生成标识。
	InstanceID string
	// MinLevel 是允许写出的最低日志级别，必须显式使用本包声明的 Level 常量。
	// 例如 LevelInfo 会过滤 DEBUG，保留 INFO、WARN、ERROR；零值不表示默认 INFO。
	MinLevel Level
}

// validate 检查日志专属配置，并将项目日志级别转换为 Zap 内部使用的数值级别。
// 校验只判断字段是否可用，不修剪原值、不补默认值，也不读取部署环境或创建输出资源。
func (config Config) validate() (zapcore.Level, error) {
	// 1. 确认服务名称包含有效内容，避免写出无法区分服务来源的日志。
	// TrimSpace 仅用于判断空白，成功后仍由构造流程保存调用方提供的原始名称。
	if strings.TrimSpace(config.ServiceName) == "" {
		return 0, errors.New("logger service name is required")
	}

	// 2. 确认装配方提供了进程实例标识，保证多副本及重启前后的日志具有区分依据。
	// 不在失败时临时生成标识，避免把日志配置错误隐藏为另一个实例身份。
	if strings.TrimSpace(config.InstanceID) == "" {
		return 0, errors.New("logger instance ID is required")
	}

	// 3. 将已声明的字符串级别逐项映射到 Zap 的级别顺序，供写入阶段进行阈值比较。
	// 映射采用精确匹配，不自动转换大小写，也不接受未声明的扩展级别。
	switch config.MinLevel {
	case LevelDebug:
		// 3.1. DEBUG 是当前支持的最低阈值，允许四种级别的日志继续写入。
		return zapcore.DebugLevel, nil
	case LevelInfo:
		// 3.2. INFO 阈值过滤调试日志，保留正常操作、警告和故障信息。
		return zapcore.InfoLevel, nil
	case LevelWarn:
		// 3.3. WARN 阈值仅保留警告和故障信息，普通操作记录会在写入入口被过滤。
		return zapcore.WarnLevel, nil
	case LevelError:
		// 3.4. ERROR 阈值仅保留故障日志，不自动启用 Panic 或 Fatal 等其他行为。
		return zapcore.ErrorLevel, nil
	default:
		// 3.5. 空值或其他未知级别属于构造参数错误，由调用方处理，不能静默采用默认阈值。
		// 返回的数值零值仅用于满足返回签名；发生错误时构造流程不会继续使用它。
		return 0, errors.New("logger minimum level must be DEBUG, INFO, WARN or ERROR")
	}
}
