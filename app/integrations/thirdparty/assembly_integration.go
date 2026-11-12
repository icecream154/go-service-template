//go:build integration_app

package thirdparty

import (
	"errors"
	"os"

	"go-service-template/app/integrations/thirdparty/ai"
)

// assembleAIClients 为集成测试进程提供禁用供应商调用的装配。
func assembleAIClients(_ string, environment string) (ai.Clients, error) {
	// 仅允许启动器明确标记的测试进程使用此装配。
	if environment != "test" || os.Getenv("APP_ENV") != "test" || os.Getenv("APP_INTEGRATION_TEST") != "1" {
		return ai.Clients{}, errors.New("integration_app requires APP_ENV=test and APP_INTEGRATION_TEST=1")
	}

	// 零值集合中的 Qwen 和 MiniMax 为空，调用入口会返回 Client 未初始化的错误。
	// 这允许启动依赖数据库的 HTTP 用例，并使需要 SDK 响应的用例明确失败。
	return ai.Clients{}, nil
}
