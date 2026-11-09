// Package thirdparty 同步初始化第三方 Client，并按功能分组提供共享实例。
package thirdparty

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"go-service-template/app/integrations/thirdparty/ai"
)

var shared struct {
	sync.RWMutex
	clients *Clients
}

// Clients 保存共享的第三方 Client，发布后实例及配置保持只读。
type Clients struct {
	AI ai.Clients
}

// Init 从配置叶子目录 integrations/thirdparty 选择当前环境，再调用各功能的装配方法。
// 功能层自行加载已注册 SDK 的配置并构造 Client，全部成功后一次性发布。
// 本方法不调用远端模型；成功发布后禁止重复初始化。
func Init(configDir, environment string) error {
	shared.Lock()
	defer shared.Unlock()
	if shared.clients != nil {
		return errors.New("thirdparty clients are already initialized")
	}
	switch environment {
	case "dev", "test", "prod":
	default:
		return errors.New("unsupported thirdparty client deployment environment")
	}
	if strings.TrimSpace(configDir) == "" {
		return errors.New("thirdparty client configuration directory is required")
	}

	aiClients, err := ai.NewClients(filepath.Join(configDir, environment, "ai"))
	if err != nil {
		return fmt.Errorf("initialize AI clients: %w", err)
	}
	shared.clients = &Clients{AI: aiClients}
	return nil
}

// Current 返回已发布集合的值副本，各 Client 指针继续复用。
// 修改分组字段不会替换共享实例；未初始化时调用视为程序装配错误。
func Current() Clients {
	shared.RLock()
	defer shared.RUnlock()
	if shared.clients == nil {
		panic("thirdparty clients are not initialized; call Init before Current")
	}
	return *shared.clients
}

// CloseIdleConnections 由资源持有方在内部工作结束后调用，不取消在途请求。
func (clients Clients) CloseIdleConnections() {
	clients.AI.CloseIdleConnections()
}
