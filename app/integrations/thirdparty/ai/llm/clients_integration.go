//go:build integration_app

package llm

import (
	"context"
	"errors"
	"time"

	"go-service-template/tests/testkit/sdkmock"
)

const integrationMockRequestTimeout = 10 * time.Second

type mockGenerateTextPayload struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type mockGenerateTextResponse struct {
	Content string `json:"content"`
}

// NewIntegrationMockClients 为 integration_app 测试进程创建委托至 sdkmock 控制面的 Qwen 与 MiniMax Client。
// 仅在测试装配边界启用，生产构建不包含本文件。
func NewIntegrationMockClients(mockControllerURL string) (Clients, error) {
	qwenMockClient, err := sdkmock.NewClient(mockControllerURL)
	if err != nil {
		return Clients{}, err
	}
	miniMaxMockClient, err := sdkmock.NewClient(mockControllerURL)
	if err != nil {
		qwenMockClient.CloseIdleConnections()
		return Clients{}, err
	}

	return Clients{
		Qwen: &QwenClient{
			client: newIntegrationMockOpenAIClient(qwenProvider, "ai.llm.qwen", qwenMockClient),
		},
		MiniMax: &MiniMaxClient{
			client: newIntegrationMockOpenAIClient(miniMaxProvider, "ai.llm.minimax", miniMaxMockClient),
		},
	}, nil
}

func newIntegrationMockOpenAIClient(providerLabel, sdkMockProvider string, mockClient *sdkmock.Client) *openAICompatibleClient {
	return &openAICompatibleClient{
		provider:       providerLabel,
		requestTimeout: integrationMockRequestTimeout,
		mockCloseIdle:  mockClient.CloseIdleConnections,
		mockGenerateText: func(ctx context.Context, params GenerateTextParams) (*GenerateTextResult, int, error) {
			var out mockGenerateTextResponse
			err := mockClient.Invoke(ctx, sdkMockProvider, "GenerateText", mockGenerateTextPayload{
				Model:  params.Model,
				Prompt: params.Prompt,
			}, &out)
			if err == nil {
				return &GenerateTextResult{Content: out.Content}, 200, nil
			}
			var callErr *sdkmock.CallError
			if errors.As(err, &callErr) {
				if callErr.Fault.Code == "INVALID_RESPONSE" {
					return nil, callErr.Fault.StatusCode, newInvalidResponseError(providerLabel)
				}
				return nil, callErr.Fault.StatusCode, &ClientError{
					Provider:     providerLabel,
					StatusCode:   callErr.Fault.StatusCode,
					ProviderCode: callErr.Fault.ProviderCode,
					cause:        callErr,
				}
			}
			return nil, 0, &ClientError{
				Provider: providerLabel,
				cause:    err,
			}
		},
	}
}
