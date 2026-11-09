// Package company 加载环境配置并同步初始化公司内部 HTTP Client。
package company

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	pythonservicetemplate "go-service-template/app/integrations/company/python-service-template"
)

const pythonServiceTemplateName = "python-service-template"

var shared struct {
	sync.RWMutex
	clients *Clients
}

// Clients 保存共享实例，调用方通过服务、domain、controller 层级获取具体 Client。
type Clients struct {
	PythonServiceTemplate pythonservicetemplate.Clients
	transports            []*http.Transport
}

// Init 封装配置读取、校验及全部 Client 的创建，成功后一次性发布，不发起 HTTP 请求。
// configDir 是 integrations/company 叶子目录；重复初始化返回错误，不替换既有实例。
func Init(configDir, environment string) error {
	shared.Lock()
	defer shared.Unlock()
	if shared.clients != nil {
		return errors.New("company HTTP clients are already initialized")
	}
	config, err := loadConfig(configDir, environment)
	if err != nil {
		return err
	}
	clients, err := newClients(config)
	if err != nil {
		return err
	}
	shared.clients = clients
	return nil
}

func newClients(config configuration) (*Clients, error) {
	if _, exists := config[pythonServiceTemplateName]; !exists {
		return nil, errors.New("python-service-template client configuration is required")
	}
	clients := &Clients{}
	for _, serviceName := range slices.Sorted(maps.Keys(config)) {
		switch serviceName {
		case pythonServiceTemplateName:
			python, transport, err := pythonservicetemplate.New(config[serviceName])
			if err != nil {
				clients.CloseIdleConnections()
				return nil, err
			}
			clients.PythonServiceTemplate = python
			clients.transports = append(clients.transports, transport)
		default:
			clients.CloseIdleConnections()
			return nil, fmt.Errorf("company HTTP clients are not registered for service %q", serviceName)
		}
	}
	return clients, nil
}

// Current 返回集合的值副本；修改分组字段不会替换已发布的共享实例。
// 各 Client 的配置和 HTTP 客户端保持私有，未初始化时调用视为程序装配错误。
func Current() Clients {
	shared.RLock()
	defer shared.RUnlock()
	if shared.clients == nil {
		panic("company HTTP clients are not initialized; call Init before Current")
	}
	return *shared.clients
}

// CloseIdleConnections 由资源持有方在内部工作结束后调用，不取消在途请求。
func (clients Clients) CloseIdleConnections() {
	for _, transport := range clients.transports {
		transport.CloseIdleConnections()
	}
}
