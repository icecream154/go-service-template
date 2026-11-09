| 字段 | 含义 | 定义方 | 示例 |
| --- | --- | --- | --- |
| `timestamp` | 本条日志发生的时间，使用 UTC，保留高精度时间信息 | 公共 Logger 在写日志时自动生成 | `2026-11-09T06:05:06.123Z` |
| `level` | 日志级别，取值为 `DEBUG`、`INFO`、`WARN`、`ERROR` | 调用方选择对应级别的日志方法，公共 Logger 写入 | `INFO` |
| `message` | 本条日志的英文说明，便于阅读具体发生了什么 | 写日志的组件或业务代码指定 | `Avatar prompt optimized` |
| `component` | 日志来源的代码职责类别，统一取值为 `http_entry`、`controller`、`service`、`client`、`repository`；外部依赖调用组件统一归为 `client`；不适用或无法识别时省略 | 公共日志规范统一约定分类，框架在操作入口识别并写入派生 Context，Logger 自动读取 | `client` |
| `event` | 操作的生命周期节点，首期统一为 `started`、`finished`；`finished` 只表示操作结束，结果通过 `error`、`status_code` 等字段表达；普通说明性日志可省略 | 公共日志规范统一定义，各组件和业务代码复用相同取值 | `finished` |
| `trace_id` | 当前请求所属链路的标识；没有请求上下文的进程日志省略 | HTTP 中间件沿用请求头或生成并存入 Context，公共 Logger 自动读取；后台任务由任务入口提供 | `4bf92f3577b34da6a3ce929d0e0e4736` |
| `service_name` | 产生日志的服务名称 | 应用配置显式提供，初始化 Logger 时统一注入 | `go-service-template` |
| `environment` | 当前进程所在的部署环境 | 复用已有 `env.Config.Environment`，由装配方传入 Logger；日志 Config 不单独声明 | `dev` |
| `instance_id` | 当前服务进程的标识，用于区分副本和重启；同一进程内保持一致 | 启动流程生成一次随机标识，初始化 Logger 时统一注入 | `2f7b6d598dbd4d61a4d4ab8652e22d74` |
| `caller` | 实际调用日志方法的代码位置；无法取得时省略 | 公共 Logger 自动提取，并处理日志封装带来的调用层级 | `service/user_profile_service.go:324` |
| `operation` | 本条日志对应的稳定操作标识，格式按下表随 `component` 确定；同一操作的 `started`、`finished` 及正常、错误路径使用相同值；不含实参、资源 ID、结果或生命周期后缀，不适用或无法识别时省略 | 框架在操作入口根据路由或方法信息生成并写入派生 Context，Logger 自动读取；命名空间沿用项目声明的领域或目录名称，层级统一以 `.` 分隔 | `example.ResourceService.QueryResource` |
| `duration_ms` | 本条事件对应操作的实际耗时，单位为毫秒；不适用时省略 | 负责执行该操作的组件记录开始和结束时间，计算后传给 Logger | `123.45` |
| `status_code` | 本次 HTTP 交互实际取得的状态码；没有 HTTP 状态时省略 | 入站请求由 HTTP 响应层记录；出站调用从远端响应读取；SDK 仅在确实提供状态码时记录 | `200` |
| `error` | 经过受控处理的失败信息；成功时省略，不记录密钥、完整供应商响应或敏感实参 | 故障发生层提供可记录的错误信息，交给公共 Logger 输出 | `Qwen text generation request failed` |

| component | operation 格式 | 取值规则 | 示例 |
| --- | --- | --- | --- |
| `http_entry` | `{HTTP 方法} {注册路由模板}` | 方法取请求值，中间一个空格；使用注册的完整路由模板，保留路径参数占位符，不包含 Query 或实际资源 ID。真正未匹配的请求使用 `{HTTP 方法} <unmatched>`；响应结束前仍未取得模板时使用 `{HTTP 方法} <unresolved>`。同一请求的开始、结束记录复用已确定的标识 | `GET /api/v1/example/resources/:resource_id` |
| `controller` | `{领域名}.{Controller 类型名}.{方法名}` | 领域名取所属领域目录名，类型名和方法名取项目实际声明的名称；不包含接收者指针符号或调用实参 | `example.ResourceController.QueryResource` |
| `service` | `{领域名}.{Service 类型名}.{方法名}` | 领域名取所属领域目录名，类型名和方法名取本项目业务 Service 的实际声明名称 | `example.ResourceService.QueryResource` |
| `client` | `{集成命名空间}.{Client 类型名}.{方法名}` | 集成命名空间取 `app/integrations/` 下 Client 所在的相对目录路径，将目录分隔符替换为 `.`；保留目录名原有拼写；类型和方法取本项目封装的 Client 声明 | `company.example-service.resource.ResourceClient.QueryResource`、`thirdparty.ai.llm.ProviderClient.GenerateText` |
| `repository` | `{领域名}.{Repository 类型名}.{方法名}` | 领域名取所属领域目录名，类型名和方法名取本项目 Repository 的实际声明名称；不使用 SQL 正文或查询条件作为操作标识 | `example.ResourceRepository.GetByID` |

## operation 自动识别与 Context 作用域

每个操作入口接入一次公共封装，日志调用点只传 Context、消息和额外字段，不手动填写 `component`、`operation`，也不手写操作名称字符串。

| component | 自动识别方式 | 接入位置 |
| --- | --- | --- |
| `http_entry` | HTTP 外层与 Gin 模板绑定中间件协作，根据请求方法和匹配后的 `Gin.FullPath()` 生成，使用 `WithHTTPOperation` 建立作用域 | 生产 `NewHTTPHandler` 已接入；无法取得模板时按下文保留明确的占位标识 |
| `controller`、`service`、`repository` | `WithCurrentOperation` 通过 `runtime.CallersFrames` 取得直接调用入口的函数符号，解析包路径、接收者类型和方法名，按目录及类型后缀生成 | 对应组件的普通导出方法入口 |
| `client` | 按具体 Client 公开方法的包路径、接收者类型和方法名生成 | 具体 Client 的公开方法入口；共享底座的排除范围见下文 |

入口用法为 `ctx = logger.WithCurrentOperation(ctx)`。例如，在 `app/domains/example/service` 中的具名方法入口建立操作作用域：

```go
func (s *ResourceService) QueryResource(ctx context.Context) {
    ctx = logger.WithCurrentOperation(ctx)
    // 后续日志及下游调用使用这个 ctx。
}
```

此入口生成 `component=service`、`operation=example.ResourceService.QueryResource`，四级日志方法自动从 Context 读取。`caller` 仍表示实际打印日志的代码位置，不用于每条日志重新推断操作。当前路由绑定的是 Validator，HTTP Handler 名称不能直接视为 Controller 名称；Controller 从自己的操作入口识别。

识别范围限于本模块约定目录下的普通导出具名方法，支持指针和值接收者。领域组件按 `app/domains/{领域名}/controller`、`service`、`repository` 的目录及对应类型后缀识别，Controller 版本子目录不进入操作标识；Client 按 `app/integrations/` 下的目录生成命名空间。普通函数、闭包、方法值包装、泛型符号和未知目录不作为操作入口识别，也不向上搜索其他调用帧来猜测名称。

现有 HTTP 公共底座 `company.base.Client` 已显式排除，SDK 公共底座因类型未导出而不符合入口识别条件。新增共享底座时须同步维护识别规则，不能仅凭目录或类型后缀保证自动区分所有底座与具体 Client。

入口识别结果按函数符号缓存，缓存仅保存组件和操作标识，不保存 Context、实参或请求数据。识别失败时建立空操作作用域，遮蔽父 Context 中的操作身份，避免把上层操作误认为新操作；日志省略 `component`、`operation`，原有 TraceId 继续保留。

辅助函数不重复调用入口封装，沿用所属操作的 Context；进入新的 Controller、Service、Client 或 Repository 操作时建立自己的派生 Context，并保留 TraceId、取消和截止时间。下游方法返回后，上层 Context 的操作信息保持原值。传入 nil Context 时以 `context.Background()` 为基础，不生成 TraceId。请求级 Context 不存入共享实例字段。

方法入口封装只识别并携带操作信息，不自动记录 `started`、`finished` 或计算耗时；需要生命周期日志时由操作边界调用日志方法。HTTP 的请求与响应边界由下文的中间件统一记录。

如需连入口封装调用也自动生成，后续须通过调用代理或生成包装代码，并将实际调用接入包装；只生成元数据不会拦截现有的直接方法调用。本阶段先采用入口公共封装的预期。

## 当前数据结构与基础写入范围

日志数据结构统一放在 `internal/logger/types.go`：`Level` 声明四种日志级别，`Component` 声明五种职责类别，`Event` 声明 `started`、`finished`，`Record` 对应字段表中的 14 个字段及 JSON 名称。`Fields` 只包含调用方可按需补充的 `Event`、`DurationMS`、`StatusCode`、`Error`，自动字段由 Logger 维护。

`Timestamp` 使用 `time.Time`。`DurationMS` 使用 `*float64`，`StatusCode` 使用 `*int`：`nil` 表示未记录，非 nil 的零值仍保留，避免将零耗时与未采集混淆。`Error` 保存已处理的错误文本，不直接序列化 Go `error` 对象。`Component`、`Event`、`TraceID`、`Caller`、`Operation`、`Error` 无值时省略；服务名称、部署环境、进程标识作为公共元信息保留。

基础 Logger 通过 `New(config Config, deployment env.Config, output io.Writer)` 构造。日志专属 `Config` 仅声明 `ServiceName`、`InstanceID` 和 `MinLevel`；部署环境复用现有 `env.Config`，装配方可在 `env.Init()` 成功后传入 `env.Current()`。Logger 按值保存部署配置，不重复读取环境变量或依赖全局初始化调用。服务名称、实例标识及传入的环境名称必须非空白，最低级别必须使用已声明的合法值；部署环境的允许值仍由 `env` 包管理。构造过程不读取配置文件、不推断服务名称，也不生成实例标识。

`Debug`、`Info`、`Warn`、`Error` 接收标准 Context、消息及可选的一个 `Fields`，返回 `error`；不需要额外字段时直接省略 `Fields`。每条日志自动补充 UTC 时间、对应级别、Context 中的 TraceId、实际调用位置和三项公共元信息。Context 为 nil 或未保存 TraceId 时省略 `trace_id`，Logger 不补生成标识。

最低级别以下的日志被过滤；其余日志同步输出为单行 JSON，不抽样、不建立异步队列。编码、写入及短写故障通过返回值交给调用方处理。`Fields` 不接受原始 Go `error` 或 SDK 对象，错误文本须由调用方先做受控处理；耗时必须为有限的非负数，HTTP 状态码来自实际交互。

输出 Writer 由调用方提供并管理。`Sync()` 在输出器支持 `Sync() error` 时转交同步，不关闭 Writer；普通 `io.Writer` 的文件、连接或额外缓冲生命周期仍由调用方管理。

以下为基础用法，示例中的公共信息由应用装配方提供：

```go
log, err := logger.New(logger.Config{
    ServiceName: "example-service",
    InstanceID:  "example-instance",
    MinLevel:    logger.LevelInfo,
}, env.Current(), os.Stdout) // 调用方已完成 env.Init()。
if err != nil {
    return err
}
if err := log.Info(ctx, "Operation completed"); err != nil {
    return err
}
```

需要补充事件、耗时或状态时，传入一份 `Fields`；以下数值用于说明字段形式，实际调用应使用采集结果：

```go
durationMS := 123.45
statusCode := 200
if err := log.Info(ctx, "HTTP call completed", logger.Fields{
    Event:      logger.EventFinished,
    DurationMS: &durationMS,
    StatusCode: &statusCode,
}); err != nil {
    return err
}
```

当前已实现基础 Logger 构造、四级日志方法、基础字段自动填充和 JSON 写入，以及 `WithCurrentOperation` 的方法入口识别和 Context 作用域。四级日志方法从 Context 自动读取 `component`、`operation`，未建立可识别作用域时省略。共享 Logger 已接入启动流程，生产 HTTP 请求与响应、公司 HTTP Client 调用日志已统一使用共享 Logger；第三方 SDK、业务步骤、数据库与其他异常日志尚未全部迁移，未新增测试用例。

## HTTP 请求与响应日志

生产入口按 `TraceID → HTTPLogging → 响应等待包装器 → Gin` 装配。`HTTPLogging` 使用已初始化的共享 Logger，为每个请求保存独立的开始时间和路由元数据，观察外层实际选定的响应；Gin 的 `HTTPRouteLogging` 在路由匹配后读取 `FullPath()`，向本次请求的状态提供注册模板。

| 日志边界 | event | operation 来源 | 结果字段 |
| --- | --- | --- | --- |
| 请求开始 | `started` | 模板首次可知时确定 `{HTTP 方法} {注册路由模板}`；在认证、Validator 和 Controller 执行前记录 | 不预填 HTTP 状态或最终耗时 |
| 对外响应结束 | `finished` | 复用本次请求已确定的 HTTP 操作标识 | 记录从 HTTP 外层接管请求起计算的 `duration_ms`、实际已选定的 `status_code`，以及可确认的写入、取消或异常终止信息 |

两条日志均使用 `component=http_entry`，自动携带同一 TraceId 和进程公共信息。`started` 的写出时刻是模板首次可知时，计时起点仍由外层在请求进入时保存。HTTP 入口不默认记录原始 URL、Query、请求正文、响应正文或认证头。

真正执行 `NoRoute` 时，Gin 已确认没有匹配模板，使用 `<unmatched>`。若外层先结束、停机拒绝请求，或者 Gin 自动重定向绕过全局中间件，则尚未取得模板，使用 `<unresolved>`；此时按顺序补出 `started` 和 `finished`，冻结同一个标识，晚到的内部处理不能改写已记录的 HTTP 操作身份。未知与尚未解析分别表达，不把所有缺少模板的情况都称为 404。

路由元数据只通过本次请求的受锁状态共享，跨 goroutine 不读取或保存 Gin Context。Gin 为下游建立 HTTP 操作作用域时，以其当前 `Request.Context()` 为基础，保留响应等待层已经应用的取消隔离、TraceId 和其他值；后续方法作用域可以在自己的派生 Context 中覆盖操作身份，外层响应日志仍使用自己的 HTTP 作用域。

`finished` 只表示外层响应处理结束。等待超时返回 503 后，内部业务可以继续执行，不能用晚到内部的 200 覆盖本次 HTTP 日志；503 也可能由业务或停机产生，不仅凭状态码判定为等待超时。正常处理未显式写状态时遵循 HTTP 的隐式 200；若请求已取消或处理异常终止且没有选定状态，则不伪造状态码。本地写入成功也不保证客户端已收到完整正文。

原有 `gin.Logger()` 和响应等待层的对外摘要、重复写入故障输出已移除，请求与响应由统一入口各记录一次。现有 Recovery、内部逃逸 panic 及其他业务日志继续按其原有边界处理，本阶段不宣称全部日志来源已经完成关联。日志输出失败使用标准错误输出兜底，不递归写同一个 Logger，也不替换业务响应。

独立调用 `routers.New(authenticator)` 只会安装 Gin 的模板绑定中间件；没有外层 HTTP 日志状态时直接透传，不隐式取得共享 Logger。生产 `NewHTTPHandler` 负责外层装配，要求先完成启动初始化；独立使用完整 HTTP 日志时，也应在初始化 Logger 后按上述顺序包装。

## 公司 HTTP Client 调用日志

具体 Client 的公开业务方法在入口对非 nil Context 调用 `logger.WithCurrentOperation`，再将派生 Context 传给公共 `GetJSON` 或 `PostJSON`。公共请求层统一记录调用生命周期，不重复识别操作名，也不将公共方法名作为远端操作标识。当前两个 image Client 均已接入，例如图片生成使用 `component=client`、`operation=company.python-service-template.image.ImageGenClient.GenerateImage`；后续新增具体 Client 方法时沿用同一入口方式。

| 日志边界 | level | event | 结果字段 |
| --- | --- | --- | --- |
| 进入公共请求流程 | `INFO` | `started` | 沿用具体 Client 的操作作用域和 TraceId，不预填状态或最终耗时 |
| 公共请求流程成功返回 | `INFO` | `finished` | 记录 `duration_ms` 和实际收到的 `status_code`，省略 `error` |
| 公共请求流程失败或异常终止 | `ERROR` | `finished` | 记录 `duration_ms`、固定的失败阶段说明；已经取得 HTTP 响应时同时保留实际状态码 |

开始和结束由 `company/base` 的公共请求流程统一配对，包含请求前校验、预取消、JSON 编码、请求构造等提前失败路径。具体 Client 不重复输出这些生命周期日志，也不在共享实例字段中保存请求 Context。日志沿用传入 Context 的 TraceId；HTTP 请求头继续透传同一标识，无标识时不额外生成。nil Context 保留原有的参数错误语义，具体 Client 不通过入口封装将其改成可执行请求的 Background Context。

`duration_ms` 从进入公共请求边界开始计算，覆盖校验、编码、构造、发送、响应读取、解码及响应体关闭；开始日志为同步输出，其开销也可能计入耗时。结束时先截取耗时，再输出 `finished`，结束日志写入时间不计入该数值。它表示本地一次 Client 调用的耗时，不能直接视为远端服务内部的执行耗时。

状态码在取得 HTTP 响应时保存，不以最终是否解码成功决定是否保留。即使 HTTP 200 的正文读取失败、超过读取上限或无法解码，结束日志仍记录 `status_code=200`，同时通过 `ERROR` 和 `error` 表达本地调用失败；非 200 同样记录实际状态。未取得响应的校验、连接、取消等失败不伪造状态码。

失败信息使用固定的阶段说明，不将原始错误对象、URL、Query、请求或响应正文、认证头写入日志；底层错误仍按现有方式返回业务方并保留错误链。调用日志不改变仅接受 HTTP 200、响应读取上限为 16 MiB、不额外重试、原有响应体关闭及关闭错误忽略等调用约定。异常终止会记录失败的 `finished`，panic 继续沿原有路径传播，不在日志层吞掉。

公共请求层通过 `logger.Current()` 取得启动时发布的共享 Logger，输出仍为 stdout 单行 JSON，并遵循最低日志级别过滤。发起公司 HTTP 调用前必须完成 Logger 初始化；未初始化调用仍按共享实例契约 panic，不隐式创建日志器。日志编码或输出失败时使用独立 stderr 兜底，TraceId 采用引用转义格式，不递归写入同一个 Logger，也不替换调用的业务结果。

操作作用域应由具体 Client 入口建立。直接调用公共 `base.Client` 不会自动推断具体业务方法或生成 Client 操作名；公共层只使用传入 Context 中已有的作用域。本阶段仅覆盖公司 HTTP Client，第三方 SDK 及其他组件的调用日志另行接入。

## 启动初始化与共享实例

`bootstrap.Init` 在 `env.Init()` 成功后、其他应用依赖创建前调用 `logger.Init(configDir, deployment)`。这里的 `configDir` 为配置根目录下的 `logger` 叶子目录，`deployment` 直接使用已有 `env.Current()`。配置选择、读取、实例标识生成、Logger 构造及共享实例发布均由 `internal/logger` 完成，bootstrap 只负责调用和初始化顺序。

配置文件为 `config/logger/dev.yml`、`test.yml`、`prod.yml`，只声明以下两个字段，不重复保存部署环境或进程实例标识：

```yaml
service_name: go-service-template
min_level: INFO
```

开发环境的 `min_level` 为 `DEBUG`，测试和生产为 `INFO`。只读取所选环境的文件，缺失时不回退；加载仅接受一份 YAML 文档及已声明字段，不展开环境变量。非法配置阻止启动，不发布部分实例。

`Init` 为本次初始化生成 16 字节随机数并编码为 32 位十六进制 `instance_id`，与配置中的服务名称、最低级别及复用的部署环境一起注入 `New`。共享 Logger 固定向 stdout 同步写入单行 JSON，不创建本地文件，也不接管 stdout 的关闭责任。启动装配只向输出适配器暴露 `Write`，当前没有内部缓冲，因此共享实例同步时不对终端或管道执行文件系统同步；独立调用 `New` 时，调用方仍可注入自己的 Writer，并遵循其原有 `Sync()` 契约。

初始化全部成功后才一次性发布共享实例，后续通过 `logger.Current()` 获取。初始化前调用 `Current()` 会 panic，重复成功调用 `Init` 返回错误，运行中不重新加载配置或替换实例。包级 `logger.Sync()` 在尚未初始化时返回 nil；已初始化时先取得共享实例快照、释放共享锁，再执行实例同步。

进程入口 `run` 在初始化前登记最终日志同步，覆盖初始化失败、主动取消及正常退出路径；成功启动后，先等待 HTTP 和内部工作结束，再清理 Client 和数据库，最后调用 `logger.Sync()`。未完全排空时沿用异常退出流程，跳过共享资源清理及日志同步，避免等待仍在写日志的后台工作；同步不关闭 stdout，也不代表采集平台已持久化数据。最终故障报告继续使用现有标准库日志兜底，不递归写入发生故障的 Logger。
