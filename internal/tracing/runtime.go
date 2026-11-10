package tracing

import (
	"context"
	"errors"
	"strings"
	"sync"

	"go-service-template/env"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// shared 保存启动时完整创建的 Tracer 与本包拥有的 SDK Provider，不保存请求级 Context。
// 读写锁保护一次性初始化和发布；关闭后不清空实例，也不支持在原进程重新初始化。
var shared struct {
	sync.RWMutex
	tracer   *Tracer
	provider *sdktrace.TracerProvider
}

// Init 构造 SDK Provider 并发布共享 Tracer，必须在接收请求或启动业务协程之前调用。
// serviceName、instanceID 由装配方从已初始化 Logger 的配置副本传入，deployment 复用已有环境配置。
// 不另读 YAML、不生成另一份实例标识，也不设置 OpenTelemetry 全局 Provider 或传播器。
// 当前不注册处理器或导出器；初始化本身不会产生、打印或上报 Span。
func Init(serviceName, instanceID string, deployment env.Config) error {
	// 1. 串行构造并发布，避免重复创建 Provider，或替换运行中的共享实例。
	shared.Lock()
	defer shared.Unlock()
	if shared.tracer != nil {
		// 1.1. 已成功初始化的实例保持不变；Shutdown 后也不能在同一进程重新发布。
		return errors.New("tracing is already initialized")
	}
	// 2. 校验装配方提供的三项来源信息，只判断非空白，不修剪或重新读取它们。
	// 服务名和实例标识由 Logger 确定，部署环境由 env 确定，不能在这里补默认值。
	// 2.1. 服务名必须已由应用日志配置明确提供，不能退回 SDK 的默认服务名称。
	if strings.TrimSpace(serviceName) == "" {
		return errors.New("tracing service name is required")
	}
	// 2.2. 实例标识必须复用当前进程的既有值，不能在追踪层重新生成。
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("tracing instance ID is required")
	}
	// 2.3. 部署环境必须来自已初始化的 env 配置，空值不能被当成某个默认环境。
	if strings.TrimSpace(deployment.Environment) == "" {
		return errors.New("tracing environment is required")
	}
	// 3. 只用显式元信息创建不可变 Resource，避免默认探测器引入另一套服务名或环境来源。
	// 标准属性名与日志 JSON 字段名不同，但值来自同一进程的同一份配置。
	identity := resource.NewSchemaless(
		attribute.String("service.name", serviceName),
		attribute.String("service.instance.id", instanceID),
		attribute.String("deployment.environment.name", deployment.Environment),
	)
	// 4. 创建真实 SDK Provider；根链路使用全量采样，子链路尊重父节点的采样决定。
	// 当前没有导出器或后台批处理协程；SDK 原始 panic 采集关闭，由边界 End 记录受控说明。
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(identity),
		// 4.1. 根 Span 沿用入口已经确定的合法 TraceId，子节点仍由 SDK 管理父子关系。
		// 不通过伪造上游 SpanId 实现兼容，也不修改上游的采样或随机性标志。
		sdktrace.WithIDGenerator(contextIDGenerator{}),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithoutPanicRecording(),
	)
	// 5. 复用已有的五类边界封装；该构造不写日志，不反向依赖 Logger，避免循环依赖。
	instance, err := New(provider)
	if err != nil {
		// 5.1. 未发布的 Provider 没有处理器、导出器或在途 Span，可直接释放；保留两类错误。
		return errors.Join(err, provider.Shutdown(context.Background()))
	}
	// 6. 全部步骤成功后同时发布实例和所有权，Current 不会读到未完成的构造状态。
	shared.tracer = instance
	shared.provider = provider
	return nil
}

// Current 返回启动时创建的共享 Tracer；未初始化时调用属于装配顺序错误。
// 仅获取实例，不创建 Span，不补造 Provider，不改变采样、导出或 Context。
func Current() *Tracer {
	// 1. 读锁与 Init 的发布锁配合，避免并发读取到只创建了部分资源的状态。
	shared.RLock()
	defer shared.RUnlock()
	if shared.tracer == nil {
		// 1.1. 必须由启动流程完成初始化，不能把遗漏接入静默降级为不采集。
		panic("tracing is not initialized; call Init before Current")
	}
	// 2. 返回同一可并发复用的实例；调用方不得在 Shutdown 后继续启动新的业务调用。
	return shared.tracer
}

// Shutdown 关闭本包在 Init 中创建的 Provider；未初始化时安全返回 nil。
// 调用方须先停止 HTTP 并等待内部工作结束，传入独立且有时限的非 nil Context。
// 不关闭 New 外部注入的 Provider，不清空共享指针，也不改变业务 Context 的取消状态。
func Shutdown(ctx context.Context) error {
	// 1. 只在短暂读锁下复制 Provider 指针，不在 SDK 清理期间阻塞共享实例的读取。
	shared.RLock()
	provider := shared.provider
	shared.RUnlock()
	// 2. 参数解析、环境或 Logger 初始化可能提前失败，没有 Provider 时无需执行清理。
	if provider == nil {
		return nil
	}
	// 3. 关闭已经创建的 Provider 必须有明确的执行 Context，不在包内隐式补造无限等待。
	if ctx == nil {
		return errors.New("tracing shutdown context is required")
	}
	// 4. 清理与重复关闭的语义交给 SDK；错误原样返回进程入口，不通过自身 Logger 递归报告。
	return provider.Shutdown(ctx)
}
