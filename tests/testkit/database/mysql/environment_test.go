package mysqltest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/docker/docker/errdefs"
)

func TestCreateRejectsInvalidInputBeforeDocker(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name    string
		ctx     context.Context
		options Options
		want    error
	}{
		// nil Context 必须在访问 Docker 或创建临时资源前被拒绝。
		{name: "nil context"},
		// 已取消的调用保留取消原因，不尝试启动实例。
		{name: "cancelled", ctx: cancelled, want: context.Canceled},
		// 无效预算不能被当作无限等待或正常默认值。
		{name: "negative timeout", ctx: context.Background(), options: Options{StartupTimeout: -1}},
		// 浮动标签不保证环境可重现，不能启动 latest 或大版本标签。
		{name: "floating image", ctx: context.Background(), options: Options{Image: "mysql:latest"}},
		// 缺少正式 DDL 时不能先创建资源再用简化表继续测试。
		{name: "missing schema", ctx: context.Background(), options: Options{RepositoryRoot: t.TempDir()}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			environment, err := Create(test.ctx, test.options)
			if environment != nil || err == nil {
				t.Fatal("invalid input must return no environment and an error")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
		})
	}
}

func TestCleanupSucceededPreservesMixedFailures(t *testing.T) {
	missing := errdefs.NotFound(errors.New("resource is already removed"))
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// 正常清理完成，无需重试。
		{name: "success", want: true},
		// Ryuk 先回收资源后，Docker 的 NotFound 仍视为已完成清理。
		{name: "removed", err: fmt.Errorf("remove: %w", missing), want: true},
		// 多个收尾阶段都报告资源不存在时，不阻塞网络和文件清理。
		{name: "joined removed", err: errors.Join(missing, fmt.Errorf("hook: %w", missing)), want: true},
		// NotFound 不能掩盖同时发生的超时，否则会错误宣称资源已清理。
		{name: "removed and timeout", err: fmt.Errorf("terminate: %w", errors.Join(missing, context.DeadlineExceeded)), want: false},
		// 普通故障仍需报告并保留下一次 Close 重试的资源引用。
		{name: "failure", err: errors.New("permission denied"), want: false},
		// 包装了空原因的非 nil 错误依然是故障，不能当作没有错误。
		{name: "nil wrapped cause", err: fmt.Errorf("cleanup: %w", error(nil)), want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanupSucceeded(test.err); got != test.want {
				t.Errorf("cleanupSucceeded = %v; want %v", got, test.want)
			}
		})
	}
}
