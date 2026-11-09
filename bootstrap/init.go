// Package bootstrap 负责按依赖顺序装配应用运行资源，并把资源的关闭责任交给启动入口。
// 此处不注册路由、不监听端口，也不直接终止进程；失败通过 error 向上传递，
// 由 main 统一记录英文日志并决定退出结果。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go-service-template/app/identity"
	"go-service-template/app/integrations/company"
	"go-service-template/env"
	"go-service-template/internal/authn"
	authnjwt "go-service-template/internal/authn/jwt"
	"go-service-template/internal/composition"
	"go-service-template/internal/database/mysql"
	appruntime "go-service-template/internal/runtime"
	"go-service-template/internal/snowflake"
)

// Init 按部署环境、公司 Client、JWT、雪花、数据库和业务服务的顺序初始化应用依赖。
//
// ctx 必须非 nil，启动入口负责为它设置初始化时限，并在初始化结束后释放计时器。
// 此 context 会传递给数据库连接建立与连接检查，用于取消启动或限制等待时间；
// 它不作为服务运行期间每次数据库操作的 context，业务请求应另行传入自身的 context。
//
// configDir 是配置根目录，例如 config 或 /opt/service/config，而不是 MySQL 叶子目录。
// 本函数分别在其下追加 integrations/company、authn/jwt、snowflake、database/mysql，按已校验的部署环境选择
// dev.yml、test.yml 或 prod.yml。部署区域由 env 映射为雪花数据中心号。
//
// 必须先完成 env.Init 才能调用 env.Current。环境配置成功后即保存在环境单例中；
// JWT 组件和雪花发号器也只初始化一次；后续数据库初始化失败不会重置组件或发号序列，
// 但不会返回部分可用的 Runtime。启动失败由 main 退出进程，不在原进程重新初始化。
// Runtime 的创建、失败清理及默认实例发布由 appruntime.Init 完成。
// 随后由 composition.Init 创建默认服务；该步骤失败时关闭已经创建的连接池。
// company.Init 自行加载配置并装配 Client，本入口只管理调用顺序和失败清理。
// 成功返回后，调用方负责数据库及公司 Client 空闲连接的最终清理。
// 本函数不会启动 HTTP 服务，调用方应在收到成功结果后再注册服务的监听入口。
func Init(ctx context.Context, configDir string) (_ *appruntime.Runtime, resultErr error) {
	// 已取消的启动无需继续读取配置或尝试联网；保留错误链供上层区分取消与普通故障。
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("initialize application: %w", err)
	}
	if err := env.Init(); err != nil {
		return nil, fmt.Errorf("initialize deployment environment: %w", err)
	}
	deployment := env.Current()
	if err := company.Init(filepath.Join(configDir, "integrations", "company"), deployment.Environment); err != nil {
		return nil, fmt.Errorf("initialize company HTTP clients: %w", err)
	}
	companyClients := company.Current()
	defer func() {
		if resultErr != nil {
			companyClients.CloseIdleConnections()
		}
	}()
	// 认证配置和密钥在建立数据库连接前校验，缺少密钥等装配错误直接阻止启动。
	jwtConfig, err := authnjwt.LoadConfig(filepath.Join(configDir, "authn", "jwt"), deployment.Environment)
	if err != nil {
		return nil, fmt.Errorf("load JWT configuration: %w", err)
	}
	// 具体命名空间由应用层传入；公共 JWT 组件不依赖项目的 identity 包。
	// 允许一种身份组合仅表示组件可签发和验证它，不授予任何业务接口权限。
	if err := authnjwt.Init(jwtConfig, map[authn.PrincipalKind][]string{
		authn.PrincipalKindUser:    {identity.UserNamespaceMember},
		authn.PrincipalKindService: {identity.ServiceNamespaceAdmin},
	}); err != nil {
		return nil, fmt.Errorf("initialize JWT provider: %w", err)
	}
	dataCenterID, err := deployment.DataCenterID()
	if err != nil {
		return nil, fmt.Errorf("resolve snowflake data center ID: %w", err)
	}
	snowflakeConfig, err := snowflake.LoadConfig(filepath.Join(configDir, "snowflake"), deployment.Environment)
	if err != nil {
		return nil, fmt.Errorf("load snowflake configuration: %w", err)
	}
	if err := snowflake.Init(dataCenterID, snowflakeConfig.MachineID); err != nil {
		return nil, fmt.Errorf("initialize snowflake generator: %w", err)
	}
	// 显式拼接根目录与固定子目录，不搜索其他环境文件，也不在缺文件时回退到 dev。
	config, err := mysql.LoadConfig(filepath.Join(configDir, "database", "mysql"), deployment.Environment)
	if err != nil {
		return nil, fmt.Errorf("load MySQL configuration: %w", err)
	}
	runtime, err := appruntime.Init(ctx, config)
	if err != nil {
		return nil, err
	}
	// 服务装配成功前不向调用方交付资源；装配失败仍须释放已初始化的连接池。
	if err := composition.Init(); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize application services: %w", err), runtime.Close())
	}
	return runtime, nil
}
