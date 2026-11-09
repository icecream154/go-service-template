package base

import "time"

// ServiceConfig 保存目标服务的共享配置及各 domain、controller 的配置。
// company 负责读取和基础校验，目标服务包按参数接收并检查自身注册的 Client。
type ServiceConfig struct {
	BaseURL        string                                 `yaml:"base_url"`
	ConnectTimeout time.Duration                          `yaml:"connect_timeout"`
	Domains        map[string]map[string]ControllerConfig `yaml:",inline"`
}

// ControllerConfig 保存单个 controller 的独立配置。
type ControllerConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
}
