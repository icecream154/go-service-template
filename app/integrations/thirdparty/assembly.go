//go:build !integration_app

package thirdparty

import (
	"errors"
	"os"
	"path/filepath"

	"go-service-template/app/integrations/thirdparty/ai"
)

// assembleAIClients 为普通应用构建装配真实 SDK。
func assembleAIClients(configDir, environment string) (ai.Clients, error) {
	// 集成测试启动器会显式设置标记。误传入普通二进制时，在加载 SDK 配置前报告构建模式错误。
	if os.Getenv("APP_INTEGRATION_TEST") == "1" {
		return ai.Clients{}, errors.New("integration service requires a binary built with the integration_app tag")
	}
	return ai.NewClients(filepath.Join(configDir, environment, "ai"))
}
