// Package bootstrap 负责按依赖顺序装配应用运行资源，并把资源的关闭责任交给启动入口。
// 此处不注册路由、不监听端口，也不直接终止进程；失败通过 error 向上传递，
// 由 main 统一记录英文日志并决定退出结果。
package bootstrap

import (
	"context"
	"fmt"
	"path/filepath"

	"go-service-template/env"
	"go-service-template/internal/database/mysql"
)

// Init 按环境配置、数据库配置、数据库连接的顺序初始化应用依赖。
//
// ctx 必须非 nil，启动入口负责为它设置初始化时限，并在初始化结束后释放计时器。
// 此 context 会传递给数据库连接建立与连接检查，用于取消启动或限制等待时间；
// 它不作为服务运行期间每次数据库操作的 context，业务请求应另行传入自身的 context。
//
// configDir 是配置根目录，例如 config 或 /opt/service/config，而不是 MySQL 叶子目录。
// 本函数在其下追加 database/mysql，再由 LoadConfig 根据已校验的部署环境选择
// dev.yml、test.yml 或 prod.yml。相对路径相对于进程工作目录，加载器最终转换为绝对路径。
// 部署区域当前不参与文件选择，仍由 env 管理并供后续日志及业务读取。
//
// 必须先完成 env.Init 才能调用 env.Current。环境配置成功后即保存在环境单例中；
// 后续数据库初始化失败不会回滚该单例，但不会返回部分可用的 Runtime。
// Runtime 的创建、连接入口获取和失败清理由 newRuntime 完成。
// 成功返回后，调用方负责最终 Close。
// 本函数不会启动 HTTP 服务，调用方应在收到成功结果后再注册服务的监听入口。
func Init(ctx context.Context, configDir string) (*Runtime, error) {
	// 已取消的启动无需继续读取配置或尝试联网；保留错误链供上层区分取消与普通故障。
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("initialize application: %w", err)
	}
	if err := env.Init(); err != nil {
		return nil, fmt.Errorf("initialize deployment environment: %w", err)
	}
	// 显式拼接根目录与固定子目录，不搜索其他环境文件，也不在缺文件时回退到 dev。
	config, err := mysql.LoadConfig(filepath.Join(configDir, "database", "mysql"), env.Current().Environment)
	if err != nil {
		return nil, fmt.Errorf("load MySQL configuration: %w", err)
	}
	return newRuntime(ctx, config)
}
