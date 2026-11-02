package services

import (
	"context"

	"go-service-template/app/shared/requestmeta"
)

// Call 将调用取消与追踪信息传给用例，不携带 HTTP 框架对象。
type Call struct {
	Context context.Context
	Trace   *requestmeta.RequestContext
}

type Result struct {
	Code    int
	Message string
	Data    any
}

type CheckResult struct {
	Status string
}

type UseCases interface {
	Check(Call) Result
}

// Service 检查进程存活，不访问数据库或其他外部依赖。
type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) Check(_ Call) Result {
	return Result{Code: 200, Message: "Success", Data: CheckResult{Status: "ok"}}
}

var _ UseCases = (*Service)(nil)
