package tracing

import (
	"context"
	"crypto/rand"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// contextIDGenerator 在创建根 Span 时兼容入口已经确定的 TraceId，不保存单次请求状态。
// 它只提供标识，不伪造远端父 SpanContext，也不将外来 TraceId 声明为必然随机。
type contextIDGenerator struct{}

// 编译期确认兼容 SDK 的标识生成接口，不公开新的业务创建入口。
var _ sdktrace.IDGenerator = contextIDGenerator{}

// NewIDs 为 SDK 的根 Span 提供 TraceId 和本次执行独立的 SpanId。
// 有有效上游标识时由 SDK 自行沿用父链路，不通过本方法重建父子关系。
func (generator contextIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	// 1. 通过统一读取入口取得已经确定的标识，只有符合公共格式时才允许沿用。
	// 普通 HTTP 入口会先生成或校验并保存 TraceId，因此根 Span 不会再换成另一个链路标识。
	id := TraceID(ctx)
	var traceID trace.TraceID
	if IsValidTraceID(id) {
		// 1.1. 公共校验已经保证 32 个小写十六进制字符且非全零，SDK 转换不会失败。
		traceID, _ = trace.TraceIDFromHex(id)
	} else {
		// 2. 没有合法兼容标识的独立根调用，使用 128 位密码学随机数并排除全零。
		for {
			// 2.1. SDK 接口没有 error 返回位；随机源故障不能通过固定或空标识伪造成功。
			// Go 1.26 的 crypto/rand.Read 正常返回时保证填满缓冲区且 err 为 nil。
			if _, err := rand.Read(traceID[:]); err != nil {
				panic("generate tracing trace ID failed")
			}
			// 2.2. 极低概率产生全零时重新读取，直至满足标准标识有效性要求。
			if traceID.IsValid() {
				break
			}
		}
	}
	// 3. 无论 TraceId 来自兼容值还是随机生成，本次根 Span 都取得独立的有效 SpanId。
	// 不修改追踪标志，SDK 根据父上下文与采样器决定 flags，不能把任意外来值标成随机标识。
	return traceID, generator.NewSpanID(ctx, traceID)
}

// NewSpanID 为根或子 Span 生成 64 位密码学随机且非全零的执行标识。
// 不从 TraceId 派生 SpanId，也不复用父节点标识；Context 只遵循 SDK 接口签名，不保存或改写。
func (contextIDGenerator) NewSpanID(_ context.Context, _ trace.TraceID) trace.SpanID {
	// 1. 为本次执行准备独立缓冲区，生成器可并发复用，不共享可变随机状态。
	var spanID trace.SpanID
	for {
		// 2. 用密码学随机源填满 8 字节；接口无法返回错误，因此不能用全零值掩盖随机源故障。
		if _, err := rand.Read(spanID[:]); err != nil {
			panic("generate tracing span ID failed")
		}
		// 3. 拒绝标准禁止的全零标识；其余值交给 SDK 组成当前执行的 SpanContext。
		if spanID.IsValid() {
			return spanID
		}
	}
}
