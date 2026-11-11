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
	"go-service-template/app/integrations/thirdparty"
	"go-service-template/env"
	"go-service-template/internal/authn"
	authnjwt "go-service-template/internal/authn/jwt"
	"go-service-template/internal/composition"
	"go-service-template/internal/database/mysql"
	"go-service-template/internal/logger"
	appruntime "go-service-template/internal/runtime"
	"go-service-template/internal/snowflake"
	"go-service-template/internal/tracing"
)

// Init 按部署环境、Logger、tracing、公司及第三方 Client、JWT、雪花、数据库和业务服务的顺序初始化应用依赖。
//
// ctx 必须非 nil，启动入口负责为它设置初始化时限，并在初始化结束后释放计时器。
// 此 context 会传递给数据库连接建立与连接检查，用于取消启动或限制等待时间；
// 它不作为服务运行期间每次数据库操作的 context，业务请求应另行传入自身的 context。
//
// configDir 是配置根目录，例如 config 或 /opt/service/config，而不是 MySQL 叶子目录。
// 本函数分别在其下追加 logger、integrations/company、integrations/thirdparty、authn/jwt、snowflake、database/mysql，
// 各组件按已校验的部署环境选择自己的配置文件。部署区域由 env 映射为雪花数据中心号。
//
// 必须先完成 env.Init 才能调用 env.Current。环境配置成功后即保存在环境单例中；
// JWT 组件和雪花发号器也只初始化一次；后续数据库初始化失败不会重置组件或发号序列，
// 但不会返回部分可用的 Runtime。启动失败由 main 退出进程，不在原进程重新初始化。
// Runtime 的创建、失败清理及默认实例发布由 appruntime.Init 完成。
// 随后由 composition.Init 创建默认服务；该步骤失败时关闭已经创建的连接池。
// company.Init、thirdparty.Init 自行加载配置并装配 Client，本入口只管理调用顺序和失败清理。
// logger.Init 自行读取日志配置、生成实例标识并发布共享 Logger；本入口不构造日志输出器。
// tracing.Init 复用日志元信息和部署环境，自行构造并发布 SDK Provider 与共享 Tracer。
// 成功返回后，调用方负责数据库及各 Client 空闲连接的最终清理。
// Logger 和 tracing 在后续初始化失败时仍保留，追踪及日志文件的收尾由进程入口处理。
// 本函数不会启动 HTTP 服务，调用方应在收到成功结果后再注册服务的监听入口。
func Init(ctx context.Context, configDir string) (_ *appruntime.Runtime, resultErr error) {
	// 1. 已取消的启动无需继续读取配置或尝试联网；保留错误链供上层区分取消与普通故障。
	if err := ctx.Err(); err != nil {
		// 1.1. 尚未创建本次启动的资源，直接返回取消或超时原因，不继续初始化。
		return nil, fmt.Errorf("initialize application: %w", err)
	}
	// 2. 首先读取并校验进程级部署环境，后续组件统一复用其结果选择唯一的配置文件。
	if err := env.Init(); err != nil {
		// 2.1. 环境配置失败时还不能选择日志环境文件，由进程入口使用既有错误输出报告。
		return nil, fmt.Errorf("initialize deployment environment: %w", err)
	}
	// 3. 取得已经校验的部署配置值副本，避免日志组件另行读取环境变量或维护另一套环境来源。
	deployment := env.Current()
	// 4. 只调用 Logger 的公共初始化入口；配置解码、实例标识生成、输出设置和单例发布
	// 都封装在 internal/logger 中，使后续依赖初始化之前就有可获取的共享 Logger。
	if err := logger.Init(filepath.Join(configDir, "logger"), deployment); err != nil {
		// 4.1. Logger 尚未成功发布时停止启动，保留初始化错误供进程入口报告。
		return nil, fmt.Errorf("initialize application logger: %w", err)
	}
	// 4.2. 取得已发布 Logger 的配置副本，仅复用服务名和进程实例标识，不在启动层重新生成。
	loggerConfig := logger.Current().Config()
	// 4.3. 只调用 tracing 公共初始化入口；Resource、Provider、采样及共享实例发布均由其内部完成。
	if err := tracing.Init(ctx, loggerConfig.ServiceName, loggerConfig.InstanceID, deployment); err != nil {
		// 4.3.1. 追踪未完成初始化时停止装配，已经可用的 Logger 由进程入口在最终退出时收尾。
		return nil, fmt.Errorf("initialize application tracing: %w", err)
	}
	// 5. 构造公司内部 HTTP Client；这里只传入配置叶子目录和已确认的环境名称。
	if err := company.Init(filepath.Join(configDir, "integrations", "company"), deployment.Environment); err != nil {
		// 5.1. 公司 Client 未完成装配时，不进入其他组件初始化；Logger 继续保留到进程收尾。
		return nil, fmt.Errorf("initialize company HTTP clients: %w", err)
	}
	// 5.2. 保存本次已成功装配的集合。若后续任何步骤失败，释放其空闲连接；
	// 成功返回则把最终清理责任交给进程入口，不在这里结束共享 Client 的生命周期。
	companyClients := company.Current()
	defer func() {
		if resultErr != nil {
			// 5.3. 初始化失败时尚未接收业务请求，允许清理本次已创建的 HTTP 连接资源。
			companyClients.CloseIdleConnections()
		}
	}()
	// 6. 逐层构造第三方 SDK Client，全部供应商成功后由 thirdparty 包发布共享集合。
	if err := thirdparty.Init(filepath.Join(configDir, "integrations", "thirdparty"), deployment.Environment); err != nil {
		// 6.1. 第三方初始化内部负责自身的部分资源清理，公司 Client 则由前面的 defer 清理。
		return nil, fmt.Errorf("initialize thirdparty clients: %w", err)
	}
	// 6.2. 保存完整的第三方集合，登记后续初始化失败时的空闲连接清理。
	thirdpartyClients := thirdparty.Current()
	defer func() {
		if resultErr != nil {
			// 6.3. defer 按登记的逆序执行，先清理后创建的第三方 Client，再清理公司 Client。
			thirdpartyClients.CloseIdleConnections()
		}
	}()
	// 7. 在建立数据库连接前读取并校验认证配置，缺少密钥等装配错误直接阻止启动。
	jwtConfig, err := authnjwt.LoadConfig(filepath.Join(configDir, "authn", "jwt"), deployment.Environment)
	if err != nil {
		// 7.1. 返回加载错误，已经成功构造的 HTTP Client 由各自 defer 释放空闲连接。
		return nil, fmt.Errorf("load JWT configuration: %w", err)
	}
	// 8. 由应用层明确传入允许的身份组合，公共 JWT 组件不依赖项目的 identity 包。
	// 允许一种身份组合仅表示组件可签发和验证它，不授予任何业务接口权限。
	if err := authnjwt.Init(jwtConfig, map[authn.PrincipalKind][]string{
		authn.PrincipalKindUser:    {identity.UserNamespaceMember},
		authn.PrincipalKindService: {identity.ServiceNamespaceAdmin},
	}); err != nil {
		// 8.1. 认证组件发布失败时停止启动，不继续构造发号器、数据库或业务服务。
		return nil, fmt.Errorf("initialize JWT provider: %w", err)
	}
	// 9. 复用部署区域到雪花数据中心编号的映射，不把业务区域文本当作机器编号使用。
	dataCenterID, err := deployment.DataCenterID()
	if err != nil {
		// 9.1. 未登记区域无法保证发号配置正确，原样保留映射错误的原因。
		return nil, fmt.Errorf("resolve snowflake data center ID: %w", err)
	}
	// 10. 读取当前环境专属的机器号配置，不从 Logger 的随机实例标识推导机器身份。
	snowflakeConfig, err := snowflake.LoadConfig(filepath.Join(configDir, "snowflake"), deployment.Environment)
	if err != nil {
		// 10.1. 配置缺失或机器号不合法时停止，避免静默使用默认机器号引发冲突。
		return nil, fmt.Errorf("load snowflake configuration: %w", err)
	}
	// 11. 使用已经确认的数据中心编号和机器号初始化共享发号器。
	if err := snowflake.Init(dataCenterID, snowflakeConfig.MachineID); err != nil {
		// 11.1. 发号器拒绝装配时不创建数据库资源，已有 HTTP Client 仍由 defer 清理。
		return nil, fmt.Errorf("initialize snowflake generator: %w", err)
	}
	// 12. 显式拼接数据库配置叶子目录，只读取当前环境文件，不搜索或回退到其他环境。
	config, err := mysql.LoadConfig(filepath.Join(configDir, "database", "mysql"), deployment.Environment)
	if err != nil {
		// 12.1. 配置失败时尚未交付数据库 Runtime，直接返回加载或校验错误。
		return nil, fmt.Errorf("load MySQL configuration: %w", err)
	}
	// 13. 创建并检查命名连接池，再由 Runtime 装配对应 Repository；过程共享启动 Context。
	runtime, err := appruntime.Init(ctx, config)
	if err != nil {
		// 13.1. Runtime 负责释放自身未完成的部分资源，本入口继续触发已登记的 Client 清理。
		return nil, err
	}
	// 14. 数据库和 Repository 就绪后创建业务 Service；成功前不向调用方交付 Runtime。
	if err := composition.Init(); err != nil {
		// 14.1. Service 装配失败也必须释放已创建的连接池，关闭失败与原始故障一起返回。
		return nil, errors.Join(fmt.Errorf("initialize application services: %w", err), runtime.Close())
	}
	// 15. 全部组件均已完成初始化，交付 Runtime。进程入口负责按顺序收尾共享资源。
	return runtime, nil
}
