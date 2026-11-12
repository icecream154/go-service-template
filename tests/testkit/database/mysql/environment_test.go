package mysqltest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/errdefs"
)

func TestCreateRejectsInvalidInputBeforeDocker(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	invalidSchemaRoot := t.TempDir()
	writeScannerFile(t, invalidSchemaRoot, "database/catalog/mysql/create_database.sql", " \n\t")
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
		// 仓库路径错误或已声明的建库脚本无效仍应报错，不能误判为项目不需要 MySQL。
		{name: "missing repository", ctx: context.Background(), options: Options{RepositoryRoot: filepath.Join(t.TempDir(), "missing")}},
		{name: "empty creation script", ctx: context.Background(), options: Options{RepositoryRoot: invalidSchemaRoot}},
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

func TestCreateSkipsDockerWhenProjectDoesNotUseMySQL(t *testing.T) {
	// Docker 地址故意不可用；没有 MySQL 定义时仍应直接成功返回，不能先探测或启动 Docker。
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "unavailable-docker.sock"))
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{name: "missing database directory"},
		{name: "empty database directory", directories: []string{"database"}},
		{name: "only Redis", files: map[string]string{"database/cache/redis/seed.sql": "not MySQL"}},
		{name: "no root creation script", directories: []string{"database/empty/mysql"}, files: map[string]string{
			"database/catalog/mysql/item.sql":                   "CREATE TABLE item (id BIGINT);",
			"database/nested/mysql/scripts/create_database.sql": "CREATE DATABASE nested_data;",
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range test.directories {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for path, contents := range test.files {
				writeScannerFile(t, root, path, contents)
			}
			environment, err := Create(t.Context(), Options{RepositoryRoot: root})
			if err != nil || environment != nil {
				t.Fatalf("project without MySQL must return no environment and no error; got %v, %v", environment, err)
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
