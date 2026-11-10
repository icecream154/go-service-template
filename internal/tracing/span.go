package tracing

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span 是单次执行的受限生命周期句柄，不重新定义 OpenTelemetry 的 Span 数据模型。
// Context 中的实际节点由 SDK 管理；句柄不公开底层 TracerProvider 或任意属性、事件写入入口。
// 创建后应在所属边界直接使用 defer span.End()，保证所有 return 和 panic 路径均结束 Span。
type Span struct {
	// span 是 SDK 创建的实际节点，负责线程安全、结束时间和重复 End 的幂等处理。
	span trace.Span
	// httpMethod 仅由 StartHTTP 在返回句柄前设置，之后保持只读；其他边界为空。
	httpMethod string
	// companyHTTP 仅由 StartCompanyClient 设置，限制公司 HTTP 结果只能提交到对应的调用节点。
	companyHTTP bool
}

// Fail 将本次执行标记为失败，description 必须是调用边界提供的受控说明。
// 不传入原始 error.Error()、panic 值、SDK 对象、凭证或请求响应正文；本方法不结束 Span。
func (s *Span) Fail(description string) {
	// 1. 空句柄没有可更新的节点，直接返回，不隐式创建 Span 或改变业务返回值。
	if s == nil || s.span == nil {
		return
	}
	// 2. 仅写入明确的错误状态及受控说明，不调用 RecordError 自动序列化原始异常。
	// SDK 对已经结束的 Span 忽略更新；本方法不会把已有错误状态重置为成功。
	s.span.SetStatus(codes.Error, description)
}

// End 结束本次 Span；重复调用遵循 SDK 的幂等约定，不重复提交同一个执行节点。
// 正常结束不强制标记 OK，保留此前 Fail 写入的错误状态。
// 直接用于 defer span.End() 时会标记 panic 异常退出，并重新抛出同一值，不记录原始 panic 内容。
// 若外层用闭包间接调用 End，recover 无法观察外层 panic，届时异常标记由该边界自行负责。
func (s *Span) End() {
	// 1. recover 必须位于直接被 defer 的方法内，才能观察正在离开当前边界的 panic。
	// 普通主动结束或重复结束时返回 nil，不改变正常执行流程。
	panicValue := recover()
	// 2. 异常退出只记录固定说明，避免供应商异常或业务 panic 携带的敏感内容进入追踪系统。
	if panicValue != nil {
		s.Fail("Operation terminated by panic")
	}
	// 3. SDK 记录结束时间并把已采集数据交给其处理器；不在此处启动、刷新或关闭导出器。
	if s != nil && s.span != nil {
		s.span.End()
	}
	// 4. 结束追踪后继续传播原 panic，保持原有 Recovery 和业务异常处理路径。
	if panicValue != nil {
		panic(panicValue)
	}
}

// EndHTTP 在外层响应完成时保存 HTTP 结果，并按该时刻结束入站 Span。
// statusCode 只传实际选定的状态，未知时传 0；failureDescription 仅接受公共边界提供的受控说明。
// finishedAt 须由外层响应返回时取得，避免把等待路由锁或同步输出结束日志的时间计入 Span。
// 本方法不 recover、不修改响应，也不等待内部业务；外围仍直接 defer End 作为异常收尾保障。
func (s *Span) EndHTTP(statusCode int, failureDescription string, finishedAt time.Time) {
	// 1. 只接受 HTTP 入口句柄，不创建新 Span，也不允许借用其他边界提交 HTTP 结果。
	if s == nil || s.span == nil || s.httpMethod == "" {
		return
	}
	// 2. 只记录实际取得的状态码；0 表示未知，不伪造 200、500 或取消专用状态。
	if statusCode != 0 {
		s.span.SetAttributes(attribute.Int("http.response.status_code", statusCode))
	}
	// 3. 写入、取消或异常终止以明确失败说明为准；单纯 4xx 不作为服务端 Span 故障。
	switch {
	case failureDescription != "":
		// 3.1. 不记录原始错误对象、panic 值或任意请求数据，只复用边界已确认的固定说明。
		s.Fail(failureDescription)
	case statusCode >= 500 && statusCode <= 599:
		// 3.2. 5xx 标记 Error；原因已经可以从 HTTP 状态推断，不重复填写状态描述。
		s.span.SetStatus(codes.Error, "")
	}
	// 4. 在结束日志之前提交实际结束时刻；后续日志仍能从 Context 读取本次 Span 的标识。
	// SDK 忽略重复 End，外围的 defer End 不会延长已经确定的执行区间。
	s.span.End(trace.WithTimestamp(finishedAt))
}

// EndCompanyHTTP 保存一次公司 HTTP 调用的协议结果，并按公共请求层取得的结束时刻结束 Span。
// statusCode 只传实际取得的响应状态，未知时传 0；失败说明由 base 按校验、发送及协议处理阶段提供。
// HTTP 200 后的读取、大小检查或解码失败也须提供受控说明，不根据成功状态码覆盖本次失败。
// 本方法不输出日志、不处理业务错误或 recover；调用边界仍直接 defer End 保障意外退出。
func (s *Span) EndCompanyHTTP(statusCode int, failureDescription string, finishedAt time.Time) {
	// 1. 只接收 StartCompanyClient 创建的句柄；nil Context 的原参数错误路径没有 Span，直接跳过。
	// 不允许把 Controller、Service、入站 HTTP 或第三方 SDK 节点当作本次公司调用的结果载体。
	if s == nil || s.span == nil || !s.companyHTTP {
		return
	}
	// 2. 保存真实响应状态；没有收到响应时不写入 0，也不从网络错误中推测 HTTP 状态。
	if statusCode != 0 {
		s.span.SetAttributes(attribute.Int("http.response.status_code", statusCode))
	}
	// 3. 公共请求层已经按完整 JSON 协议确定成败，只使用其固定说明，不记录原始异常或远端正文。
	// 正常结束不强制设置 OK，保留此前已有的错误；失败标记本身不产生额外日志。
	if failureDescription != "" {
		s.Fail(failureDescription)
	}
	// 4. 与调用耗时共用同一结束时刻，在输出 finished 日志前结束，排除结束日志写入的开销。
	// SDK 对重复 End 幂等，直接登记的 defer End 不会延长已完成的调用节点。
	s.span.End(trace.WithTimestamp(finishedAt))
}

// SetHTTPRoute 在路由匹配后更新 HTTP Span 名称，不创建新 Span 或修改调用方的 operation Context。
// routeTemplate 由 HTTP 路由层提供；真正未匹配时传 <unmatched>，尚未确定时传空或 <unresolved>。
// 只对 StartHTTP 创建的句柄生效，不允许借此重命名 Controller、Service 或 Client Span。
func (s *Span) SetHTTPRoute(routeTemplate string) {
	// 1. 空句柄或其他边界没有 HTTP 方法元信息，不更新其名称，也不推测请求类型。
	if s == nil || s.span == nil || s.httpMethod == "" {
		return
	}
	// 2. 使用创建时的方法和当前已确认的注册模板更新同一个节点，SDK 忽略结束后的更新。
	// 路由层负责与 HTTP 日志使用相同的冻结规则，不能让晚到的结果改写已完成的请求身份。
	s.span.SetName(httpOperation(s.httpMethod, routeTemplate))
	// 3. 只将实际注册模板写入路由属性；unmatched、unresolved 继续由名称表达，不伪造真实路由。
	if routeTemplate != "" && routeTemplate != "<unmatched>" && routeTemplate != "<unresolved>" {
		s.span.SetAttributes(attribute.String("http.route", routeTemplate))
	}
}

// httpOperation 按日志规范生成稳定的 HTTP 操作名，不读取原始 URL 或 Query。
func httpOperation(method, routeTemplate string) string {
	// 1. 创建 Span 时可能尚未匹配路由，使用明确的占位模板，不能提前断言为未知接口。
	if routeTemplate == "" {
		routeTemplate = "<unresolved>"
	}
	// 2. 方法与模板之间固定一个空格；实际模板和 <unmatched> 等状态由 HTTP 层确定。
	return method + " " + routeTemplate
}
