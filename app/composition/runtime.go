// Package composition 构建业务服务依赖图，构造过程不执行 I/O 或启动任务。
package composition

import health "go-service-template/app/domains/health/services"

// Runtime 持有进程内共享的业务服务，通过参数传给各入口。
type Runtime struct {
	Health health.UseCases
}

func New() *Runtime {
	return &Runtime{Health: health.NewService()}
}
