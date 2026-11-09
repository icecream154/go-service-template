package base

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// NewTransport 构造目标服务共享的 Transport，注入 DNS/TCP 连接超时，不建立网络连接。
// 每次调用返回独立实例；同一目标服务的各 Client 应复用同一个返回值。
func NewTransport(connectTimeout time.Duration) (*http.Transport, error) {
	if connectTimeout <= 0 {
		return nil, errors.New("connect_timeout must be positive")
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport must be an *http.Transport")
	}
	// 克隆保留默认连接池、代理和 TLS 设置，不修改全局 Transport 或共享它的连接池。
	transport := defaultTransport.Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	return transport, nil
}
