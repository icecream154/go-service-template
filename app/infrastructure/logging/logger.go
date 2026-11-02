package logging

import "go.uber.org/zap"

// New creates a process logger without changing Zap's global logger.
func New(appName string, debug bool) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.Development = debug
	if debug {
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	cfg.InitialFields = map[string]any{"app_name": appName, "log_type": "server"}
	return cfg.Build()
}
