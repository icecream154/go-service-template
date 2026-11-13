//go:build integration_app

package thirdparty

import (
	"errors"
	"os"
	"strings"

	"go-service-template/app/integrations/thirdparty/ai"
	"go-service-template/app/integrations/thirdparty/ai/llm"
)

// SDKMockURLKey 是测试装配器向 integration_app 子进程注入跨进程第三方 SDK Mock 控制面地址的环境变量名。
const SDKMockURLKey = "APP_INTEGRATION_SDK_MOCK_URL"

// assembleAIClients 为集成测试进程提供受控的第三方 AI SDK 装配。
// 若未注入 APP_INTEGRATION_SDK_MOCK_URL，返回零值集合（调用时返回未初始化错误）；
// 若已注入，则装配转发至父进程 sdkmock.Controller 的 Client 实例。
func assembleAIClients(_ string, environment string) (ai.Clients, error) {
	// 仅允许启动器明确标记的测试进程使用此装配。
	if environment != "test" || os.Getenv("APP_ENV") != "test" || os.Getenv("APP_INTEGRATION_TEST") != "1" {
		return ai.Clients{}, errors.New("integration_app requires APP_ENV=test and APP_INTEGRATION_TEST=1")
	}

	mockURL := strings.TrimSpace(os.Getenv(SDKMockURLKey))
	if mockURL == "" {
		return ai.Clients{}, nil
	}

	llmClients, err := llm.NewIntegrationMockClients(mockURL)
	if err != nil {
		return ai.Clients{}, err
	}
	return ai.Clients{LLM: llmClients}, nil
}
