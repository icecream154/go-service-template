//go:build integration || business

package harness

import (
	"testing"

	"go-service-template/tests/testkit/flow"
)

// NewFlow 基于当前运行环境创建多步骤业务流程执行器，自动挂载被测 HTTP 客户端、SDK 控制面与全部公司内部服务 HTTP 桩。
func NewFlow(t testing.TB, env *Environment, opts ...flow.Option) *flow.Runner {
	t.Helper()
	if env == nil {
		t.Fatal("harness.NewFlow requires a non-nil Environment")
	}
	return flow.New(t, flow.Config{
		HTTP:      env.HTTP,
		SDKMock:   env.SDKMock,
		HTTPStubs: env.CompanyStubs(),
	}, opts...)
}
