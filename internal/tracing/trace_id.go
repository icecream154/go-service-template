package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// TraceIDHeaderName 是现有入站、响应及 company 出站请求统一使用的自定义 TraceId 头名称。
// 标准 traceparent、tracestate 由 Extract 和 Inject 处理，不使用本常量替代。
const TraceIDHeaderName = "TraceId"

// traceIDContextKey 保存尚未关联标准 SpanContext 时的 TraceId，与 SDK 及 operation 的 key 隔离。
type traceIDContextKey struct{}

// NewTraceID 生成符合 W3C Trace Context 标识格式的 TraceId。
// 使用 16 字节密码学随机数，编码为 32 个小写十六进制字符，排除全零值。
// 本方法只生成链路标识，不创建 Span，也不构造 traceparent 请求头。
func NewTraceID() (string, error) {
	// 1. 为本次生成准备独立的 128 位缓冲区，不保存或复用其他请求的标识。
	var value [16]byte
	// 2. 生成有效标识后才返回；极低概率出现的全零随机值需要重新生成。
	for {
		// 2.1. 使用密码学随机源填满缓冲区，随机源失败时原样返回错误，由调用入口处理。
		if _, err := rand.Read(value[:]); err != nil {
			return "", err
		}
		// 2.2. W3C 不允许全零 TraceId，因此不能将该值编码后交给日志或下游服务。
		if value == ([16]byte{}) {
			continue
		}
		// 2.3. 每个字节编码为两个小写十六进制字符，得到符合格式的 32 字符标识。
		return hex.EncodeToString(value[:]), nil
	}
}

// IsValidTraceID 检查 TraceId 是否严格满足 32 个小写十六进制字符且不全为零。
// 不修剪空白、不转换大小写，也不修改输入；空字符串及不符合上述格式的旧值返回 false。
func IsValidTraceID(id string) bool {
	// 1. 文本形式必须恰好为 32 字节；只允许 ASCII，因此字节数也就是合法字符数。
	if len(id) != 32 {
		return false
	}
	// 2. 逐字节检查字符范围，同时记下是否出现非零字符，不分配解码缓冲区。
	hasNonZero := false
	for index := 0; index < len(id); index++ {
		// 2.1. 只接受数字和小写 a 到 f；大写字符、空白、连字符及非 ASCII 字符均非法。
		character := id[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
		// 2.2. 合法字符中任何一个不为 0，就说明整个 128 位标识不是全零。
		if character != '0' {
			hasNonZero = true
		}
	}
	// 3. 所有字符合法且至少存在一个非零字符时才接受该标识。
	return hasNonZero
}

// WithTraceID 返回保存 TraceId 的派生 Context，不修改传入的 Context。
// ctx 必须非 nil；调用方须通过 NewTraceID 取得标识，或先通过 IsValidTraceID 校验外来值。
// 本方法只存储已确定的标识，不生成 SpanId、不创建 Span，也不改写标准 SpanContext。
// 已有有效标准 SpanContext 时，TraceID 仍优先返回其中的标识，不受本次保存值影响。
func WithTraceID(ctx context.Context, id string) context.Context {
	// 1. 使用本包私有 key 写入派生节点，保留父 Context 的取消、截止时间及其他值。
	// 该值作为标准 SpanContext 缺失时的兼容来源，统一通过 TraceID 读取。
	return context.WithValue(ctx, traceIDContextKey{}, id)
}
