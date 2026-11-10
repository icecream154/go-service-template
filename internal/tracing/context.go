package tracing

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Extract 从载体提取 W3C traceparent、tracestate，返回携带远端父节点的 Context。
// ctx 必须非 nil；没有合法标准头时沿用原 Context，不生成 TraceId 或伪造父 SpanId。
// 本方法只提供能力，不自动包装 HTTP；自定义 TraceId 旧头的兼容不在本次提取中处理。
func Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	// 1. 提取上下文必须基于入口提供的父 Context，不用 Background 替换取消、截止时间或其他值。
	if ctx == nil {
		panic("tracing context is required")
	}
	// 2. 没有载体等同于没有标准追踪头，原样返回；此时后续边界仍可基于原 Context 创建 Span。
	if carrier == nil {
		return ctx
	}
	// 3. 标准解析、合法性校验及 remote 标记交给 SDK 提供的传播器，避免自行拆分或拼造头部。
	// 提取只派生 Context，不创建服务端 Span；HTTP 入口之后显式调用 StartHTTP。
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

// Inject 将当前有效 SpanContext 写入载体，由公司 HTTP 公共请求层在发送前显式调用。
// 载体须可写且没有其他请求遗留的追踪头；本方法不清理或管理调用方载体的生命周期。
// 不默认传播 Baggage，也不自动作用于第三方 SDK 请求。
func Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	// 1. 没有 Context 或载体时不补造上下文、不创建 Span，也不生成独立于本次调用的标识。
	if ctx == nil || carrier == nil {
		return
	}
	// 2. 传播器读取当前 Span 的 TraceId、SpanId 和追踪标志；无有效 SpanContext 时不写入。
	// 各次出站调用使用自己的子 Context，不能复用上一请求的当前 Span。
	propagation.TraceContext{}.Inject(ctx, carrier)
}

// TraceID 是链路标识的统一读取入口：优先使用有效标准 SpanContext，缺失时读取 WithTraceID 保存的值。
// 两个来源都没有值或 ctx 为 nil 时返回空字符串，不在读取时生成标识、创建 Span 或修改 Context。
func TraceID(ctx context.Context) string {
	// 1. 允许读取无请求上下文的进程状态，但不因此补造根 Context 或标识。
	if ctx == nil {
		return ""
	}
	// 2. 有效性与是否采样分开判断；未采集的有效 Span 仍需保留链路关联标识。
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		// 2.1. 标准上下文优先，即使私有 key 中保存了不同的值，也不使日志与当前 Span 分成两条链路。
		// SDK 统一输出 32 个小写十六进制字符，与标准 traceparent 中的链路标识一致。
		return spanContext.TraceID().String()
	}
	// 3. 还没有有效标准节点时，沿用入口已经生成或校验并保存的 TraceId，不伪造父 SpanId。
	id, _ := ctx.Value(traceIDContextKey{}).(string)
	return id
}

// SpanID 读取标准 SpanContext 中当前执行节点的标识，没有有效节点时返回空字符串。
func SpanID(ctx context.Context) string {
	// 1. nil 只表示没有可读取的执行节点，本方法不创建 Span 或修改任何请求状态。
	if ctx == nil {
		return ""
	}
	// 2. 读取最近的标准节点；子 Context 覆盖当前 Span，但调用方原 Context 中的节点保持不变。
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	// 3. SDK 将当前 SpanId 编码为 16 个小写十六进制字符，重复调用不会生成新的节点。
	return spanContext.SpanID().String()
}
