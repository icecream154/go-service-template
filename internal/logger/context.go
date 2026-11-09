package logger

import (
	"context"
	"runtime"
)

// operationContextKey 仅供本包读写操作信息，与 TraceId 等其他 Context 字段隔离。
// 使用独立的私有类型作为 key，避免与其他包保存的字符串 key 或结构体 key 相互覆盖。
type operationContextKey struct{}

// WithCurrentOperation 在普通具名方法入口识别直接调用者，返回携带操作信息的派生 Context。
// 辅助函数沿用传入 Context；进入新的操作时再调用本方法，不扫描父调用栈猜测操作。
// 识别失败时写入空操作信息，遮蔽上层操作；原 Context 的 TraceId、取消和其他值继续保留。
// ctx 为 nil 时使用 Background，不生成 TraceId，也不自动记录事件或耗时。
func WithCurrentOperation(ctx context.Context) context.Context {
	// 1. 为无请求上下文的调用提供可派生的根 Context。
	// Background 没有 TraceId、截止时间或取消信号；已有的非 nil Context 则完整沿用。
	if ctx == nil {
		ctx = context.Background()
	}
	// 2. 先准备表示“未识别”的零值和只容纳一个程序计数器的数组。
	// 如果运行时未返回调用位置，或者后续符号解析失败，最终仍会建立空操作作用域。
	var operation operationInfo
	var pcs [1]uintptr
	// 3. 读取直接调用本方法的入口位置，不继续向上查找其他业务方法。
	// skip=2 依次跳过 runtime.Callers 和 WithCurrentOperation，自第三个逻辑调用帧开始取值。
	if runtime.Callers(2, pcs[:]) != 0 {
		// 3.1. 将程序计数器还原为包含函数符号的逻辑帧。
		// CallersFrames 会处理内联函数及返回地址调整，避免直接解析 PC 时取错函数归属。
		frame, _ := runtime.CallersFrames(pcs[:]).Next()
		// 3.2. 按入口函数符号取得已缓存或新解析的组件、操作标识。
		// 解析失败返回零值；这里不回退到父 Context 的操作，防止错误归属。
		operation = resolveOperation(frame.Function)
	}
	// 4. 将本次识别结果按值放入派生 Context，并返回给当前方法后续使用。
	// 相同私有 key 的空值也会遮蔽父操作；父 Context 本身不变，其他 key 的 TraceId、
	// 截止时间和取消信号仍可沿父链访问，因此下层作用域不会改写上层操作信息。
	return context.WithValue(ctx, operationContextKey{}, operation)
}

// operationFromContext 只读取最近一层操作信息，不在日志写入时重新解析调用栈。
func operationFromContext(ctx context.Context) operationInfo {
	// 1. nil 表示调用方没有提供上下文，直接返回空元数据，交给日志编码层省略字段。
	if ctx == nil {
		return operationInfo{}
	}
	// 2. 按本包私有 key 查找最近的作用域，并进行不会 panic 的类型断言。
	// 没有设置该 key 时得到零值；已经设置的空作用域则明确遮蔽更上层的操作。
	operation, _ := ctx.Value(operationContextKey{}).(operationInfo)
	// 3. 返回只包含字符串的值副本；调用方读取结果不会修改 Context 中的元数据。
	return operation
}
