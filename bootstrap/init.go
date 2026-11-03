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

// Runtime 持有应用进程使用的资源，由启动入口统一管理生命周期。
// Init 成功后，调用方即成为这些资源的持有者，必须在停止业务访问后调用 Close。
// 将资源放在 Runtime 中便于明确资源归属，避免各业务模块自行创建或关闭连接池。
type Runtime struct {
	// Databases 管理本进程已初始化的 MySQL 命名连接及各自的底层连接池。
	// 仓储通过 Get("user")、Get("post") 或 Get("order") 明确选择连接；
	// 模型所在目录不会自动决定使用哪个数据库，也不会自动建立跨库事务。
	// 业务可以使用取得的连接，但连接池的关闭统一由 Runtime.Close 负责。
	Databases *mysql.Manager
}

// Init 按环境配置、数据库配置、数据库连接的顺序初始化应用依赖。
//
// ctx 必须非 nil，启动入口负责为它设置初始化时限，并在初始化结束后释放计时器。
// 此 context 会传递给数据库连接建立与连接检查，用于取消启动或控制网络等待时间；
// 它不作为服务运行期间每次数据库操作的 context，业务请求应另行传入自身的 context。
//
// configDir 是配置根目录，例如 config 或 /opt/service/config，而不是 MySQL 叶子目录。
// 本函数在其下追加 database/mysql，再由 LoadConfig 根据已校验的部署环境选择
// dev.yml、test.yml 或 prod.yml。相对路径相对于进程工作目录，加载器最终转换为绝对路径。
// 部署区域当前不参与文件选择，仍由 env 管理并供后续日志及业务读取。
//
// 必须先完成 env.Init 才能调用 env.Current。环境配置成功后即保存在环境单例中；
// 后续数据库初始化失败不会回滚该单例，但不会返回部分可用的 Runtime。
// mysql.Open 负责在中途失败时关闭本次已创建的连接池，因此失败时无资源移交给调用方。
// 只有全部启用连接都通过检查后才返回 Runtime，此时调用方负责最终 Close。
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
	// 完整配置校验后再逐个建立命名连接；任何失败均由 Open 在内部清理后返回。
	databases, err := mysql.Open(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("initialize MySQL connections: %w", err)
	}
	return &Runtime{Databases: databases}, nil
}

// Close 关闭本进程创建的数据库连接池；调用前应先停止 HTTP 请求和业务任务。
//
// 此方法不负责关闭 HTTP 服务，也不负责等待业务协程结束；这些先后关系由 main.run 管理。
// nil Runtime 或尚无数据库资源时可安全调用，数据库管理器也允许重复关闭。
// 关闭错误会返回给调用方，不能只依靠 defer 丢弃错误，否则可能掩盖资源释放故障。
func (r *Runtime) Close() error {
	if r == nil || r.Databases == nil {
		return nil
	}
	return r.Databases.Close()
}
