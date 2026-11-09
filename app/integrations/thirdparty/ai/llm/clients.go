package llm

import (
	"fmt"
	"path/filepath"
)

// Clients 保存已接入的 LLM Client，不维护独立的全局实例。
type Clients struct {
	Qwen    *QwenClient
	MiniMax *MiniMaxClient
}

// NewClients 接收已经选好环境的 ai/llm 配置目录，逐个加载和构造已注册 Client。
// 只有全部成功才返回可用集合；失败时清理本次已创建的资源，不发起模型调用。
func NewClients(configDir string) (_ Clients, resultErr error) {
	var clients Clients
	defer func() {
		if resultErr != nil {
			clients.CloseIdleConnections()
		}
	}()
	qwenConfig, err := LoadQwenConfig(filepath.Join(configDir, "qwen.yml"))
	if err != nil {
		return Clients{}, fmt.Errorf("load Qwen configuration: %w", err)
	}
	clients.Qwen, err = NewQwenClient(qwenConfig)
	if err != nil {
		return Clients{}, fmt.Errorf("initialize Qwen client: %w", err)
	}
	miniMaxConfig, err := LoadMiniMaxConfig(filepath.Join(configDir, "minimax.yml"))
	if err != nil {
		return Clients{}, fmt.Errorf("load MiniMax configuration: %w", err)
	}
	clients.MiniMax, err = NewMiniMaxClient(miniMaxConfig)
	if err != nil {
		return Clients{}, fmt.Errorf("initialize MiniMax client: %w", err)
	}
	return clients, nil
}

// CloseIdleConnections 清理各供应商 Client 的空闲连接，不取消在途请求。
func (clients Clients) CloseIdleConnections() {
	clients.MiniMax.CloseIdleConnections()
	clients.Qwen.CloseIdleConnections()
}
