package jwt

import (
	"errors"
	"sync"

	"go-service-template/internal/authn"
)

// shared 仅保存本进程的默认组件；Provider 内的配置和密钥发布后不再修改。
var shared struct {
	sync.RWMutex
	provider *Provider
}

// Init 创建并发布一次默认 JWT 组件，由启动装配层在监听请求前调用。
// 配置、密钥或允许的身份组合无效时返回错误，不发布部分组件；重复初始化不会替换原实例。
// 修改环境变量不会改变已发布的密钥，不支持运行中重载，也不自动签发任何凭证。
func Init(config Config, allowedNamespaces map[authn.PrincipalKind][]string) error {
	shared.Lock()
	defer shared.Unlock()
	if shared.provider != nil {
		return errors.New("JWT provider is already initialized")
	}
	provider, err := New(config, allowedNamespaces)
	if err != nil {
		return err
	}
	shared.provider = provider
	return nil
}

// Current 返回启动阶段发布的默认 JWT 组件，供可信调用方签发凭证或验证收到的凭证。
// 未成功 Init 时 panic，表示程序装配顺序错误；配置错误必须在 Init 返回时处理。
// 返回的组件允许并发使用，不持有需停机关闭的连接或后台任务。
func Current() *Provider {
	shared.RLock()
	provider := shared.provider
	shared.RUnlock()
	if provider == nil {
		panic("JWT provider is not initialized; call Init before Current")
	}
	return provider
}
