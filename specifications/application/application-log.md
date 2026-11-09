| 字段 | 含义 | 定义方 | 示例 |
| --- | --- | --- | --- |
| `timestamp` | 本条日志发生的时间，包含时区和毫秒等精度 | 公共 Logger 在写日志时自动生成 | `2026-11-09T06:05:06.123Z` |
| `level` | 日志级别，取值为 `DEBUG`、`INFO`、`WARN`、`ERROR` | 调用方选择对应级别的日志方法，公共 Logger 写入 | `INFO` |
| `message` | 本条日志的英文说明，便于阅读具体发生了什么 | 写日志的组件或业务代码指定 | `Avatar prompt optimized` |
| `component` | 日志来源的代码职责类别，统一取值为 `http_entry`、`controller`、`service`、`client`、`repository`；外部依赖调用组件统一归为 `client`；不适用或无法识别时省略 | 公共日志规范统一约定分类，框架在操作入口识别并写入派生 Context，Logger 自动读取 | `client` |
| `event` | 操作的生命周期节点，首期统一为 `started`、`finished`；`finished` 只表示操作结束，结果通过 `error`、`status_code` 等字段表达；普通说明性日志可省略 | 公共日志规范统一定义，各组件和业务代码复用相同取值 | `finished` |
| `trace_id` | 当前请求所属链路的标识；没有请求上下文的进程日志省略 | HTTP 中间件沿用请求头或生成并存入 Context，公共 Logger 自动读取；后台任务由任务入口提供 | `4bf92f3577b34da6a3ce929d0e0e4736` |
| `service_name` | 产生日志的服务名称 | 应用配置显式提供，初始化 Logger 时统一注入 | `go-service-template` |
| `environment` | 当前进程所在的部署环境 | 启动流程读取部署环境，初始化 Logger 时统一注入 | `dev` |
| `instance_id` | 当前服务进程的标识，用于区分副本和重启；同一进程内保持一致 | 启动流程生成一次随机标识，初始化 Logger 时统一注入 | `2f7b6d598dbd4d61a4d4ab8652e22d74` |
| `caller` | 实际调用日志方法的代码位置；无法取得时省略 | 公共 Logger 自动提取，并处理日志封装带来的调用层级 | `service/user_profile_service.go:324` |
| `operation` | 本条日志对应的稳定操作标识，格式按下表随 `component` 确定；同一操作的 `started`、`finished` 及正常、错误路径使用相同值；不含实参、资源 ID、结果或生命周期后缀，不适用或无法识别时省略 | 框架在操作入口根据路由或方法信息生成并写入派生 Context，Logger 自动读取；命名空间沿用项目声明的领域或目录名称，层级统一以 `.` 分隔 | `example.ResourceService.QueryResource` |
| `duration_ms` | 本条事件对应操作的实际耗时，单位为毫秒；不适用时省略 | 负责执行该操作的组件记录开始和结束时间，计算后传给 Logger | `123.45` |
| `status_code` | 本次 HTTP 交互实际取得的状态码；没有 HTTP 状态时省略 | 入站请求由 HTTP 响应层记录；出站调用从远端响应读取；SDK 仅在确实提供状态码时记录 | `200` |
| `error` | 经过受控处理的失败信息；成功时省略，不记录密钥、完整供应商响应或敏感实参 | 故障发生层提供可记录的错误信息，交给公共 Logger 输出 | `Qwen text generation request failed` |

| component | operation 格式 | 取值规则 | 示例 |
| --- | --- | --- | --- |
| `http_entry` | `{HTTP 方法} {注册路由模板}` | 方法大写，中间一个空格；使用注册的完整路由模板，保留路径参数占位符，不包含 Query。在路由匹配后确定该操作的生命周期标识并复用；真正未匹配的请求使用 `{HTTP 方法} <unmatched>`，不能将尚未匹配视为未匹配 | `GET /api/v1/example/resources/:resource_id` |
| `controller` | `{领域名}.{Controller 类型名}.{方法名}` | 领域名取所属领域目录名，类型名和方法名取项目实际声明的名称；不包含接收者指针符号或调用实参 | `example.ResourceController.QueryResource` |
| `service` | `{领域名}.{Service 类型名}.{方法名}` | 领域名取所属领域目录名，类型名和方法名取本项目业务 Service 的实际声明名称 | `example.ResourceService.QueryResource` |
| `client` | `{集成命名空间}.{Client 类型名}.{方法名}` | 集成命名空间取 `app/integrations/` 下 Client 所在的相对目录路径，将目录分隔符替换为 `.`；保留目录名原有拼写；类型和方法取本项目封装的 Client 声明 | `company.example-service.resource.ResourceClient.QueryResource`、`thirdparty.ai.llm.ProviderClient.GenerateText` |
| `repository` | `{领域名}.{Repository 类型名}.{方法名}` | 领域名取所属领域目录名，类型名和方法名取本项目 Repository 的实际声明名称；不使用 SQL 正文或查询条件作为操作标识 | `example.ResourceRepository.GetByID` |

## operation 自动识别预期（待实现）

每个操作入口接入一次公共封装，日志调用点只传 Context、消息和额外字段，不手动填写 `component`、`operation`，也不手写操作名称字符串。

| component | 自动识别方式 | 接入位置 |
| --- | --- | --- |
| `http_entry` | 根据请求方法和匹配后的 `Gin.FullPath()` 生成；真正未匹配时使用固定占位标识 | HTTP 日志中间件 |
| `controller`、`service`、`repository` | 入口公共封装通过 `runtime.CallersFrames` 取得函数符号，再解析包路径、接收者类型和方法名，按约定目录及上述格式生成 | 对应组件的方法入口 |
| `client` | 按具体 Client 公开方法的包路径、接收者类型和方法名生成 | 具体 Client 的公开方法入口，不在共享 HTTP 或 SDK 底座中推断具体接口 |

拟定入口用法为 `ctx = logger.WithCurrentOperation(ctx)`，该方法尚未实现。当前路由绑定的是 Validator，HTTP Handler 名称不能直接视为 Controller 名称；Controller 应从自己的操作入口识别。

入口识别结果可按方法信息缓存，后续日志从 Context 读取，避免每条日志重新扫描调用栈。首期识别范围为项目约定目录下的普通具名方法；识别失败时省略无法确定的字段，不猜测名称，也不将上层操作身份当作新操作身份。

辅助函数沿用所属操作的 Context；进入新的 Controller、Service、Client 或 Repository 操作时建立自己的派生 Context，并保留 TraceId。下游方法返回后，上层 Context 的操作信息保持原值。请求级 Context 不存入共享实例字段。

如需连入口封装调用也自动生成，后续须通过调用代理或生成包装代码，并将实际调用接入包装；只生成元数据不会拦截现有的直接方法调用。本阶段先采用入口公共封装的预期。

## 当前数据结构范围

日志数据结构统一放在 `internal/logger/types.go`：`Level` 声明四种日志级别，`Component` 声明五种职责类别，`Event` 声明 `started`、`finished`，`Record` 对应字段表中的 14 个字段及 JSON 名称。

`Timestamp` 使用 `time.Time`。`DurationMS` 使用 `*float64`，`StatusCode` 使用 `*int`：`nil` 表示未记录，非 nil 的零值仍保留，避免将零耗时与未采集混淆。`Error` 保存已处理的错误文本，不直接序列化 Go `error` 对象。`Component`、`Event`、`TraceID`、`Caller`、`Operation`、`Error` 无值时省略；服务名称、部署环境、进程标识作为公共元信息保留。

当前仅声明类型、枚举和 JSON 标签，尚未实现 Logger 输出、字段自动填充、方法入口识别、配置加载或启动接入，也未新增测试用例。
