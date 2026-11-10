// Package tracing 封装 HTTP、Controller、Service、公司 HTTP Client 和第三方 SDK Client 的 Span 边界。
// 同时提供 TraceId 的生成、校验、Context 存储及统一读取，兼容尚未接入 Span 的现有调用。
// New 接收外部 Provider；Init 构造进程共享 Provider，不设置 OpenTelemetry 全局实例或启动导出器。
package tracing

import (
	"context"
	"errors"
	"reflect"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Tracer 提供规范允许的五类 Span 入口；实例可并发复用，不保存请求 Context 或当前 Span。
// 不暴露通用 Start 方法，避免业务层绕过固定边界随意创建其他类型的 Span。
type Tracer struct {
	// tracer 来自显式注入的 Provider，其采样、资源和导出策略由 Provider 的装配方负责。
	tracer trace.Tracer
}

// New 根据调用方提供的 TracerProvider 构造独立的追踪入口。
// Provider 由调用方创建、关闭；本包不接管其生命周期，也不隐式使用全局 Provider。
// 注入 SDK Provider 才能实际生成和采集 Span；显式注入 no-op Provider 时沿用其不采集行为。
func New(provider trace.TracerProvider) (*Tracer, error) {
	// 1. 缺少 Provider 属于构造参数错误，不能悄悄退回全局实例或创建另一套追踪配置。
	if provider == nil {
		return nil, errors.New("tracing provider is required")
	}
	// 2. 使用本包的编译期导入路径作为 instrumentation scope，模块改名后随编译结果更新。
	// 该名称表示追踪封装来源，不作为业务 Span 的 operation。
	tracer := provider.Tracer(reflect.TypeFor[Tracer]().PkgPath())
	if tracer == nil {
		// 2.1. Provider 应返回可用 Tracer；不符合接口约定时在构造阶段报告，避免请求时才暴露。
		return nil, errors.New("tracing provider returned a nil tracer")
	}
	// 3. 只保存可并发复用的 Tracer；每次调用的 Span 和 Context 均由对应入口独立创建。
	return &Tracer{tracer: tracer}, nil
}

// StartHTTP 创建一次入站 HTTP 的 SERVER Span，返回下游应沿用的派生 Context。
// method 必须非空；路由模板尚未确定时传空字符串，名称暂用 <unresolved>。
// 匹配后调用 Span.SetHTTPRoute 补充模板；不得将带 Query 或实际资源 ID 的 URL 作为模板。
func (t *Tracer) StartHTTP(ctx context.Context, method, routeTemplate string) (context.Context, *Span) {
	// 1. HTTP 方法由入站基础设施提供；缺失时无法形成规范操作名，按调用契约错误处理。
	if method == "" {
		panic("tracing HTTP method is required")
	}
	// 2. 使用方法和注册模板生成稳定名称，已有远端或本地父 Span 时由 SDK 自动建立关系。
	ctx, span := t.start(ctx, httpOperation(method, routeTemplate), trace.SpanKindServer,
		attribute.String("http.request.method", method))
	// 3. 只在本次 Span 句柄中保存不可变方法名，供路由确认后更新名称，不保存请求对象。
	span.httpMethod = method
	// 3.1. 独立调用时也可直接提供模板；未知状态占位只用于名称，不写成真实注册路由属性。
	span.SetHTTPRoute(routeTemplate)
	return ctx, span
}

// StartController 创建一次 Controller 公开接口处理方法的 INTERNAL Span。
// operation 由统一入口按日志规范解析后传入，业务代码不得手写名称或将实参拼入名称。
func (t *Tracer) StartController(ctx context.Context, operation string) (context.Context, *Span) {
	// 1. 未识别到操作时使用当前边界的固定占位名，不借用上层 HTTP 或其他方法的身份。
	if operation == "" {
		operation = "controller <unresolved>"
	}
	// 2. 当前 Context 中的 Span 成为父节点，返回的子 Context 只供本次 Controller 使用。
	return t.start(ctx, operation, trace.SpanKindInternal)
}

// StartService 创建一次 Service 公开业务方法的 INTERNAL Span，不用于私有辅助或生命周期方法。
// operation 由统一入口解析；内部调用其他 Service 时继续传入当前派生 Context。
func (t *Tracer) StartService(ctx context.Context, operation string) (context.Context, *Span) {
	// 1. 识别失败时明确保留 Service 边界，不能将父 Controller 的操作名误记为本次业务名称。
	if operation == "" {
		operation = "service <unresolved>"
	}
	// 2. 每次执行创建独立子 Span；同一业务方法重复调用时名称相同，SpanId 由 SDK 分别生成。
	return t.start(ctx, operation, trace.SpanKindInternal)
}

// StartCompanyClient 创建一次公司 HTTP Client 调用的 CLIENT Span。
// operation 使用具体 Client 已解析的名称；公共请求层统一调用，底层 Transport 不重复创建。
func (t *Tracer) StartCompanyClient(ctx context.Context, operation string) (context.Context, *Span) {
	// 1. 未提供具体接口身份时使用公司 Client 的固定占位名，不使用公共底座方法名替代。
	if operation == "" {
		operation = "company_client <unresolved>"
	}
	// 2. 创建独立的本地调用节点，标准头注入由公共请求层使用本次派生 Context 显式触发。
	ctx, span := t.start(ctx, operation, trace.SpanKindClient)
	// 3. 只在返回前登记公司 HTTP 边界，供受限收尾方法写入实际响应状态和结束时刻。
	// 标记之后保持只读，不公开任意属性入口，也不允许借用其他边界的句柄提交 HTTP 结果。
	span.companyHTTP = true
	return ctx, span
}

// StartSDKClient 创建一次项目封装的第三方 SDK Client 业务调用的 CLIENT Span。
// operation 使用具体 Client 已解析的名称；SDK 内部方法、HTTP 发送和重试不额外创建 Span。
func (t *Tracer) StartSDKClient(ctx context.Context, operation string) (context.Context, *Span) {
	// 1. 识别失败时使用 SDK Client 的固定占位名，不将供应商 SDK 的内部符号当作业务操作。
	if operation == "" {
		operation = "sdk_client <unresolved>"
	}
	// 2. 只创建本项目可观测的调用边界，不隐式向第三方发送追踪头或推测其内部调用关系。
	ctx, span := t.start(ctx, operation, trace.SpanKindClient)
	// 3. 返回前登记第三方 SDK 边界，供受限收尾方法记录功能公共层确认的状态和结束时刻。
	// 标记之后保持只读，不把 SDK 内部方法、HTTP 发送或重试变成额外的执行节点。
	span.sdkClient = true
	return ctx, span
}

// start 是五类固定入口共用的内部创建流程，不对业务层开放任意 Span kind 或附加创建选项。
// ctx 必须由调用入口提供；本方法不补造 Background，也不修改父 Context 的其他值。
func (t *Tracer) start(ctx context.Context, operation string, kind trace.SpanKind, attributes ...attribute.KeyValue) (context.Context, *Span) {
	// 1. 未构造实例和 nil Context 都是入口接入错误，不能悄悄转成丢失父链路的根调用。
	if t == nil || t.tracer == nil {
		panic("tracer is not initialized")
	}
	if ctx == nil {
		panic("tracing context is required")
	}
	// 2. SDK 从标准 Context 读取父节点，并管理 TraceId、SpanId、开始时间及采样状态。
	// 不使用 WithNewRoot，因此已有上游链路时不会在本地擅自断开。
	// 已知属性在创建时交给 SDK，便于采样器取得入口信息；属性只由本包的固定边界提供。
	derived, span := t.tracer.Start(ctx, operation, trace.WithSpanKind(kind), trace.WithAttributes(attributes...))
	// 3. 以受限句柄交回结束责任；派生 Context 传给下层，调用方原有 Context 仍保留原 Span。
	return derived, &Span{span: span}
}
