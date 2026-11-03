package bootstrap

import (
	"fmt"

	"go-service-template/env"
)

// Init 在服务启动前初始化应用依赖。
func Init() error {
	if err := env.Init(); err != nil {
		return fmt.Errorf("initialize deployment environment: %w", err)
	}
	return nil
}
