// Package ai 装配 AI 各子功能的第三方 Client，不维护独立的全局实例。
package ai

import (
	"path/filepath"

	"go-service-template/app/integrations/thirdparty/ai/llm"
)

// Clients 按 AI 子功能保存已装配的 Client 集合。
type Clients struct {
	LLM llm.Clients
}

// NewClients 接收已经选好环境的 ai 配置目录，构造各子功能的 Client。
// 失败时清理已创建资源，不发布实例，也不发起远端调用。
func NewClients(configDir string) (_ Clients, resultErr error) {
	var clients Clients
	defer func() {
		if resultErr != nil {
			clients.CloseIdleConnections()
		}
	}()
	llmClients, err := llm.NewClients(filepath.Join(configDir, "llm"))
	if err != nil {
		return Clients{}, err
	}
	clients.LLM = llmClients
	return clients, nil
}

// CloseIdleConnections 清理各子功能的空闲连接，不取消在途请求。
func (clients Clients) CloseIdleConnections() {
	clients.LLM.CloseIdleConnections()
}
