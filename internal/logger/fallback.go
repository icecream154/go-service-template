package logger

import (
	"context"
	"fmt"
	"os"

	"go-service-template/internal/tracing"
)

// reportWriteFailure 由四级打印方法在私有 write 返回非 nil 错误后调用，统一报告日志自身故障。
// 错误仅来自 Logger 的参数检查、编码或输出，不接收业务错误、请求数据或 SDK 异常。
// 本方法直接使用 stderr，不调用共享 Logger、不重试，也不生成 TraceId 或修改 Context。
func reportWriteFailure(ctx context.Context, err error) {
	// 1. 有请求上下文时通过统一入口读取 TraceId，优先标准节点；无 Context 时保留空标识。
	var id string
	if ctx != nil {
		// 1.1. 读取值不依赖请求是否已取消，日志基础设施故障也能关联到原有请求。
		id = tracing.TraceID(ctx)
	}
	// 2. 仅报告 Logger 自身的错误，TraceId 用引用格式转义，避免上游标识中的换行破坏输出。
	// stderr 故障不再递归报告或重试，不改变调用方的业务返回、HTTP 响应或 panic 传播。
	_, _ = fmt.Fprintf(os.Stderr, "Write application log failed: trace_id=%q error=%v\n", id, err)
}
