package bootstrap

import (
	"errors"
	"os"
	"sync"
	"syscall"

	"go-service-template/app/composition"
	"go-service-template/app/infrastructure/logging"
	"go-service-template/config"

	"go.uber.org/multierr"
	"go.uber.org/zap"
)

type Application struct {
	Config  config.Config
	Logger  *zap.Logger
	Runtime *composition.Runtime

	closeOnce sync.Once
	closeErr  error
}

// Initialize loads startup settings and assembles services without creating HTTP objects.
func Initialize(configPath string) (*Application, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	logger, err := logging.New(cfg.AppName, cfg.AppDebug)
	if err != nil {
		return nil, err
	}
	return &Application{Config: cfg, Logger: logger, Runtime: composition.New()}, nil
}

// Close runs after HTTP shutdown. Repeated calls return the original result.
func (a *Application) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.Logger == nil {
			return
		}
		var failures []error
		for _, err := range multierr.Errors(a.Logger.Sync()) {
			if !unsupportedConsoleSync(err) {
				failures = append(failures, err)
			}
		}
		a.closeErr = errors.Join(failures...)
	})
	return a.closeErr
}

func unsupportedConsoleSync(err error) bool {
	var pathError *os.PathError
	if !errors.As(err, &pathError) || pathError.Op != "sync" {
		return false
	}
	console := pathError.Path == os.Stdout.Name() || pathError.Path == os.Stderr.Name() || pathError.Path == "/dev/stdout" || pathError.Path == "/dev/stderr" || pathError.Path == "/dev/fd/1" || pathError.Path == "/dev/fd/2"
	return console && (errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EBADF))
}
