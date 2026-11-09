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

// WithHTTPOperation 根据 HTTP 框架已确定的方法和路由模板建立 http_entry 操作作用域。
// 本方法由 HTTP 日志基础设施调用，业务日志只需沿用返回的 Context，不手写操作标识。
// method 和 routeTemplate 按框架提供的原值拼接，不读取原始 URL，也不通过调用栈猜测路由。
// 缺少任一项时建立空作用域以遮蔽父操作；未匹配或尚未解析的路由占位值由 HTTP 中间件决定。
// 返回值保留父 Context 的 TraceId、取消和其他数据；不生成标识、不记录事件或计算耗时。
func WithHTTPOperation(ctx context.Context, method, routeTemplate string) context.Context {
	// 1. 为没有传入 Context 的基础设施调用准备可派生的根，保持与方法入口作用域的 nil 处理一致。
	// 已有 Context 则直接作为父节点，保留其取消、截止时间和其他 key 对应的数据。
	if ctx == nil {
		ctx = context.Background()
	}
	// 2. 默认使用空操作信息；缺失方法或模板时不借用父操作，也不猜测真实请求路径。
	// 这里不判断空模板究竟表示未匹配还是尚未匹配，该状态只能由掌握路由过程的中间件确认。
	var operation operationInfo
	if method != "" && routeTemplate != "" {
		// 2.1. 只有两项都已提供时，使用固定组件类别及“方法 + 一个空格 + 路由模板”的操作格式。
		// 不修剪、转换大小写或替换占位符，确保开始、结束及内部沿用的 HTTP 作用域保持同一标识。
		operation = operationInfo{component: ComponentHTTPEntry, name: method + " " + routeTemplate}
	}
	// 3. 将本次 HTTP 元数据写入派生 Context；零值也明确遮蔽父层相同 key，父节点本身保持不变。
	// TraceId 使用独立的 key，因此本次覆盖操作身份不会更改链路标识或原有执行控制。
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
