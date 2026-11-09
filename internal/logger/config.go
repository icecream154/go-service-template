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
	ServiceName string
	InstanceID  string
	MinLevel    Level
}

func (config Config) validate() (zapcore.Level, error) {
	if strings.TrimSpace(config.ServiceName) == "" {
		return 0, errors.New("logger service name is required")
	}
	if strings.TrimSpace(config.InstanceID) == "" {
		return 0, errors.New("logger instance ID is required")
	}
	switch config.MinLevel {
	case LevelDebug:
		return zapcore.DebugLevel, nil
	case LevelInfo:
		return zapcore.InfoLevel, nil
	case LevelWarn:
		return zapcore.WarnLevel, nil
	case LevelError:
		return zapcore.ErrorLevel, nil
	default:
		return 0, errors.New("logger minimum level must be DEBUG, INFO, WARN or ERROR")
	}
}
