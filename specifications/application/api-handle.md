# 一个接口的完整处理流程

本文面向第一次阅读本项目的开发者，说明一个 HTTP 请求如何进入系统、经过各层处理、访问数据库，再转换成最终响应。以 `createUser` 为主线，最后补充查询、分页、批量和更新接口的差异。

本文描述各层的调用关系，以及 HTTP 入口统一启用 TraceId 和响应等待超时后的处理约定。所有接口默认等待 3 秒，分页查询、创建用户和头像生成分别覆盖为 5 秒、10 秒、125 秒；超时只结束响应等待，不取消内部业务。路由与 DTO 的命名规则见[接口路由与命名规范](api-routing.md)，响应结构与错误码规则见[接口返回规范](api-response.md)。文中的源码路径均以仓库根目录为起点，点击文件链接即可继续阅读实现。

## 1. 先看整体链路

一个请求的主要处理顺序是：

```text
客户端
  → net/http.Server 接收请求
  → TraceId 中间件：沿用或生成标识，保存到 Request.Context，并设置响应头
  → HTTP 日志外层：保存开始时间和本次请求状态，等待 Gin 提供路由模板
  → HTTPHandler 按方法和路径选择响应等待配置
  → 响应等待超时中间件：控制客户端等待，不取消内部业务
  → Gin 匹配路由：绑定 HTTP 操作模板、记录 started，再执行后续公共中间件
  → /api 全局可选认证：无 Authorization 头匿名继续，有凭证则验证并保存 Principal
  → 对应用例的 Validator
      → Bind：HTTP 参数映射为 Request DTO
      → validateXxxRequest：校验请求字段
  → Controller：按接口要求调用 authz 检查身份，执行接口访问规则，再构造 Service 参数
  → Service：执行业务流程
  → Repository：执行数据库操作
  → MySQL
```

数据库操作完成后，结果沿 Go 函数调用返回：

```text
Repository 返回表结构或 error
  → Service 返回领域模型或用例 Result；明确业务失败返回业务错误，输入错误、技术故障和未知 error 原样返回
  → Controller 处理结果；createUser 成功后签发新用户 JWT
  → Controller 转换为 Response DTO 或接口错误
  → response.Success / response.Fail 编码并写入 JSON
  → 外层决定发送正常响应或超时响应；超时后丢弃晚到结果
  → HTTP 日志外层记录 finished：选定的状态及外层耗时，业务可能仍继续
  → 内部处理结束，释放本次执行资源
```

需要先区分两件事：**启动时注册处理链，请求到来后执行处理链。** `NewRouter`、`RegisterV1Routes` 等方法在启动时执行；每次请求不会重新注册路由、创建数据库连接池或创建默认 Service。

HTTP 响应与内部处理的结束时间分别观察：正常请求的内部处理通常先完成，响应等待到期时则可能先记录 HTTP `finished`，内部业务随后才结束。

## 2. 请求到来前，系统已经准备了什么

### 2.1 从进程入口到开始监听

进程入口是 [cmd/web/main.go](../../cmd/web/main.go)。`main` 处理退出信号并调用 `run`；`run` 解析启动参数、设置初始化时限，再调用 [bootstrap.Init](../../bootstrap/init.go)。

实际顺序如下：

```text
main → run
  → bootstrap.Init
      → env.Init：读取部署环境和区域
      → logger.Init：读取日志配置、生成实例标识并发布共享 Logger
      → company.Init：读取公司服务配置、创建共享 Transport 和 Image Client
      → thirdparty.Init：选择环境目录，经 AI、LLM 分组装配 Qwen、MiniMax 并发布共享实例
      → 加载 JWT 配置、校验密钥并初始化共享签发与认证组件
      → 取得雪花数据中心编号
      → 加载雪花配置并初始化共享发号器
      → 加载 MySQL 配置
      → runtime.Init：创建数据库连接和 Repository
      → composition.Init：创建默认 Service
  → routers.NewHTTPHandler
      → routers.New(authnjwt.Current())：传入共享认证器
          → 安装 HTTPRouteLogging，绑定匹配后的路由模板
          → /api 分组挂载一次 AuthenticateIfPresent
          → 创建 Controller、Validator 并注册各版本的领域路由
      → 为同一个 Gin Engine 装配默认及接口覆盖的响应等待包装器
      → 在请求分发外层装配 HTTPLogging，再在最外层装配 TraceId 中间件
  → net.Listen
  → http.Server.Serve：开始接收 HTTP 请求
```

环境、配置和数据库连接初始化成功后，程序才开始监听端口。默认地址为 `:8080`；启动方式和配置准备见 [README](../../README.md)。数据库及表结构需要提前准备，启动流程不自动建表，也不创建示例用户。

Logger 的启动装配中，`bootstrap.Init` 只调用 `logger.Init`，传入 `logger` 配置叶子目录和已有部署配置。`internal/logger` 读取所选环境的 `dev.yml`、`test.yml` 或 `prod.yml`，配置仅包含 `service_name` 和 `min_level`；开发使用 `DEBUG`，测试和生产使用 `INFO`。加载严格限定单文档和已知字段，不展开环境变量或回退其他环境。包内生成一次 32 位十六进制实例标识，构造向 stdout 同步写 JSON 的共享 Logger。初始化失败不发布实例，具体约定见[日志初始化与共享实例](application-log.md#启动初始化与共享实例)。

第三方 Client 的启动装配中，`bootstrap.Init` 只调用根 `thirdparty.Init(configDir, environment)` 并处理资源清理，其 `configDir` 为 `integrations/thirdparty` 叶子目录，环境仅支持 `dev/test/prod`。根层选择 `<environment>/ai` 后交给 `ai.NewClients`，AI 层将 `llm` 目录交给 `llm.NewClients`，由 LLM 层读取 `qwen.yml`、`minimax.yml` 并调用各自的 Loader、构造方法。根层不维护具体供应商文件名，子层不设置全局 `Init` 或 `Current`。注册配置缺失或非法时停止启动，不回退其他环境，也不在启动时调用模型；其他尚未注册的 SDK 占位文件不要求提前提供配置。

`cmd/web` 将 `routers.NewHTTPHandler` 返回的装配对象设置为 `http.Server.Handler`，并保存其 `Shutdown` 收尾入口。装配对象在启动时创建完整 Gin Engine、各个等待包装器，以及 TraceId、HTTP 日志外层；每次请求依次经过 TraceId、HTTP 日志外层、选定的等待包装器，再进入 Gin，不重新创建路由或中间件。HTTP 日志外层使用已经初始化的共享 Logger，Gin 在匹配路由后提供模板；元数据通过每请求的受锁状态传递，不跨 goroutine 读取 Gin Context。`routers.New(authenticator)` 接收已装配的认证器，只返回 Gin Engine；其模板绑定中间件在没有外层日志状态时直接透传，不隐式依赖 Logger 初始化。独立使用时，调用方按需要在初始化 Logger 后包装 `middleware.TraceID(middleware.HTTPLogging(engine))`；响应等待与收尾则另行装配。生产入口由 `NewHTTPHandler` 完成全部装配。配置和执行边界见第 5.4 节，日志细节见[HTTP 请求与响应日志](application-log.md#http-请求与响应日志)。

### 2.2 共享对象分别由谁持有

| 位置 | 启动时准备的对象 | 请求中如何使用 |
| --- | --- | --- |
| [internal/logger](../../internal/logger/runtime.go) | 已加载日志配置并生成实例标识的共享 Logger | HTTP 请求与响应日志已使用共享实例；日志方法接收标准 Context，其他业务日志尚未统一切换 |
| [internal/authn/jwt](../../internal/authn/jwt/runtime.go) | 共享 JWT 签发与认证组件 | 作为 `authn.Authenticator` 传给 Router 的全局可选认证中间件；创建成功后 Controller 取得组件签发凭证 |
| [HTTP 认证中间件](../../app/http/middleware/authentication.go) | 捕获已装配认证器的 Gin Handler | 认证已提供的凭证并保存 Principal；Controller 调用 authz，共用方法通过 `PrincipalFromContext` 读取 |
| [TraceId 中间件](../../app/http/middleware/trace_id.go)、[公共存储方法](../../internal/tracing/trace_id.go)、[统一读取入口](../../internal/tracing/context.go) | 包装请求分发的 `net/http` Handler；公共方法不保存全局请求状态 | 入站确定 TraceId，保存到标准 Request Context，并通过响应头和 company 请求头传递 |
| [internal/snowflake](../../internal/snowflake/snowflake.go) | 共享雪花发号器 | 创建用户时调用 `snowflake.NextID()` |
| [internal/runtime](../../internal/runtime/runtime.go) | DB 入口和 Repository | Service 调用 `appruntime.Current()` 获取 |
| [internal/composition](../../internal/composition/services.go) | 默认 `UserProfileService` | Controller 调用 `composition.Current().UserProfile` 获取 |
| [company Client 集合](../../app/integrations/company/clients.go) | 已完成装配的公司内部 HTTP Client | Service 通过 `company.Current()` 按服务、domain、controller 获取 |
| [thirdparty Client 集合](../../app/integrations/thirdparty/clients.go) | 经 AI、LLM 分组构造的 Qwen、MiniMax SDK Client | Service 通过 `thirdparty.Current().AI.LLM.Qwen` 或 `.MiniMax` 获取共享实例，传入标准 Context |
| [routers/http_handler.go](../../routers/http_handler.go) | 同一个 Gin Engine、HTTP 日志外层、默认与覆盖配置的等待包装器及收尾组件 | 按请求方法和路径选择一个等待包装器；对外响应日志由 HTTP 日志外层记录，停机时统一等待内部处理 |
| [用户领域 Router](../../app/domains/user/routers/router.go) | Controller 和各用例的 Validator | Gin 保存其处理方法，收到请求后调用 |

MySQL 组件为每个启用的命名连接创建独立连接池，完成连通性及会话检查后交给 GORM。用户资料 Repository 复用用户数据库入口；Repository 的构造函数不会另建连接池。

`Current()` 的含义是“取得已经初始化的共享对象”。它本身不创建 Service、Repository 或连接池，也不执行 SQL。初始化前调用会触发异常，因此启动顺序是请求能够正常工作的前提。

`thirdparty.Current()` 返回分组集合的值副本，各 SDK Client 指针仍指向同一共享实例；重复初始化报错，配置及实例发布后只读。修改配置通过重启生效；单次调用的模型由 Params 指定，不改变共享 Client。

`logger.Current()` 返回已经发布的 Logger 指针，初始化前调用会 panic，重复初始化返回错误。包级 `logger.Sync()` 未初始化时直接返回 nil，因此进程入口可以在初始化前登记最终同步；Logger 没有关闭 stdout 或替换全局标准库日志的隐式行为。

当前 Controller 和 Service 都不保存单次请求的数据。Request DTO、Service 参数、操作结果和 Response DTO 在每次请求中独立创建。

### 2.3 处理链中的文件在哪里

用户资料接口的 Validator 和 DTO 都按“版本 → Controller → 用例”组织，Controller 目录保持按版本划分：

```text
app/domains/user/
  controller/v1/
    user_profile_controller.go
  validator/v1/user_profile/
    create_user_validator.go
    ...其他用例的 Validator 文件
  dto/v1/user_profile/
    create_user.go
    ...其他用例的请求与响应文件
    user_profile.go
```

两个 `user_profile` 目录分别是独立的 `userprofile` 包。Router 使用 `userprofilevalidator` 别名导入 Validator 包；Validator 和 Controller 使用 `userprofiledto` 别名导入 DTO 包。每个 DTO 用例文件保留完整的 Request、Response 名称，同一分组共享的 `UserProfile` 结构放在 `user_profile.go`，各用例直接在同包引用。

目前这些 DTO 只在用户资料 Controller 内复用，因此不建立 `dto/v1/shared` 包。将来真正存在跨 Controller 且语义一致的 DTO 时，再按需建立版本内的共享包；具体约定见[接口路由与命名规范](api-routing.md)。

Controller 共用的身份要求和主体标识转换位于 [app/http/authz](../../app/http/authz/)：`principal.go` 检查是否已有指定身份，`member.go` 按项目成员约定转换用户 ID。这些是读取请求身份的普通函数，不在启动时创建新对象，也不作为额外中间件挂载；各接口的目标访问规则仍留在 Controller。

### 2.4 TraceId 的生成、保存与透传

1. **入站优先沿用 Context 中已确定的标识，再处理 `TraceId` 请求头。** [TraceId 中间件](../../app/http/middleware/trace_id.go) 在响应等待中间件之前运行，通过 `tracing.TraceID(ctx)` 读取已有标识；没有值时读取自定义请求头，头名由 `tracing.TraceIDHeaderName` 声明。候选值通过 `tracing.IsValidTraceID()` 检查是否恰好为 32 个小写十六进制字符且不全为零。合法值原样沿用；缺失、为空或非法时，通过 `tracing.NewTraceID()` 生成 16 字节密码学随机数并编码，排除全零值后取得新的合法标识。格式非法不拒绝业务请求，也不修剪、转换或回显原值。生成失败返回统一 HTTP 500，不继续业务处理。标准 `traceparent` 提取、注入及 Span 创建的公共能力已实现，实际 HTTP 及业务链路尚未接入。
2. **Context 存储与读取统一由 tracing 提供。** `tracing.WithTraceID(ctx, id)` 使用私有类型的 key 保存已经确定的合法值，中间件将新 Context 放回 Request；后续统一通过 `tracing.TraceID(ctx)` 读取。读取优先有效标准 SpanContext，缺失时使用私有 key 的值，两者都没有或 Context 为 nil 时返回空字符串。存储不隐式校验或生成标识，其他入口应先通过 `NewTraceID` 生成或 `IsValidTraceID` 校验；不创建 Span 或伪造 SpanId。仅写 `gin.Context.Set` 无法供现有 Service 和 company Client 读取，因此不作为 TraceId 的保存方式。
3. **成功确定 TraceId 后，通过同名响应头返回。** 响应头同样使用 `tracing.TraceID` 从派生 Context 读取，避免标准节点与兼容值冲突时输出两个标识。该头在外层设置，校验失败、认证失败、404、响应等待超时和停机拒绝仍可返回同一值；现有 `data/error` JSON 结构保持不变。TraceId 只用于链路关联，不参与认证或授权。
4. **Controller、Service 和 Client 显式传递执行 Context。** 所有 Service 公开业务方法统一以 `ctx context.Context` 为首参，Controller 使用内部 `ctx.Request.Context()`，Service 入口建立自己的日志操作作用域，再将派生 Context 继续传给其他 Service、具体 company 或 thirdparty Client；不向 DTO、Params、领域模型或共享实例字段添加 TraceId。现有 `GenerateUserAvatar` 先使用 Qwen 的 `QwenModel38Flash` 优化提示词，再调用 company 图片生成 Client，两次顺序调用沿用同一个执行 Context；Qwen 失败或优化结果为空白时立即结束，不发起图片生成。每个 Client 独立控制请求超时，头像路由按当前两个 60 秒预算加 5 秒处理余量设置为 125 秒响应等待。
5. **company 请求统一在公共层注入。** `base.Client.requestJSON` 读取 Context 中的 TraceId，非空时设置出站 `TraceId` 请求头，覆盖全部 `GetJSON`、`PostJSON` 调用。无值时不设置，也不在每次远调时生成；后台任务需要关联时由任务入口生成一次并写入 Context，贯穿该任务所有调用。

现有 `context.WithoutCancel` 保留 Context 值，因此 HTTP 等待超时或客户端断开后继续执行的内部工作仍使用相同 TraceId。HTTP 模板绑定以 Gin 当前的 Request Context 建立操作作用域，保留这层取消隔离；不能拿外层可取消 Context 替换内部 Context。公共 Logger 已完成启动初始化，HTTP 请求与响应日志自动读取 TraceId；其他业务日志尚未统一迁移，也未创建或导出 Span。日志字段及操作作用域见[请求日志规范](application-log.md)。

## 3. 跟随一次 createUser 请求

### 3.1 客户端发送请求

以首次注册用户为例：

```http
POST /api/v1/user/userProfile/createUser
Content-Type: application/json
```

```json
{
  "account_source": "example_identity",
  "external_account_id": "external-user-001",
  "user_name": "Example User",
  "avatar_url": null
}
```

客户端提交账户身份声明和初始资料。内部用户 ID、启用状态、版本及持久化时间由服务端生成。

`createUser` 允许匿名调用，不要求已有内部用户 ID 或身份凭证；匿名请求省略 Authorization 头。如果携带了错误、过期或格式不合法的凭证，会先被全局认证中间件拒绝。账户来源和外部账户标识仍只是请求中的声明，不会据此核验外部身份；创建后签发的凭证只对应新建的本地用户。具体契约见 [user 领域接口权限规范](../domains/user/api-authorization.md#4-创建用户与首次凭证签发)。

### 3.2 Gin 匹配路由并执行全局可选认证

[routers/web.go](../../routers/web.go) 的 `New(authenticator)` 创建 Gin Engine，安装 HTTP 日志模板绑定和异常恢复中间件；在 `/api` 分组挂载一次 `AuthenticateIfPresent(authenticator)`，再建立 `/v1` 等版本分组。版本及领域路由继承该中间件，不逐接口重复注册认证步骤。

随后，[用户领域 Router](../../app/domains/user/routers/router.go) 注册领域和 Controller 分组：

```go
user := v1.Group("/user")
userProfile := user.Group("/userProfile")
userProfile.POST("/createUser", r.createUserValidator.CreateUser)
```

合并后的完整路径就是 `/api/v1/user/userProfile/createUser`。这里注册的用例入口是 **Validator 的 `CreateUser` 方法**，执行前先经过 `/api` 分组继承的可选认证。

一次请求到来后，Gin 按 HTTP 方法和路径选择已注册的处理链。公共中间件围绕后续处理执行：

- `HTTPRouteLogging` 读取匹配后的 `FullPath()`，通过 `logger.WithHTTPOperation` 建立 `http_entry` 作用域，并让请求专属状态输出一次 `started`。外层 `HTTPLogging` 在对外响应结束时输出一次 `finished`，附实际选定的状态和外层耗时；原有 `gin.Logger()` 及响应等待层旧摘要已移除，避免重复记录或把内部缓冲状态当作对外状态。
- `gin.CustomRecovery` 捕获当前请求处理链中的未处理 `panic`，框架错误的返回方式见第 5 节。
- `AuthenticateIfPresent` 只在完全没有 Authorization 头时匿名放行；头显式为空、重复或格式错误时返回 400，凭证无效、过期或方案不支持时返回 401，技术故障返回 500。携带有效凭证时先保存 Principal，再进入 Validator；失败时不降级匿名，公开接口也遵守这一规则。

HTTP 操作标识使用注册模板，保留路径参数占位符，不包含 Query 或实际资源 ID。真正的 NoRoute 使用 `{METHOD} <unmatched>`；若响应已结束但 Gin 尚未提供模板，或自动重定向绕过模板绑定，则使用 `{METHOD} <unresolved>`。本次 HTTP 的开始、结束日志复用同一个确定后的标识，晚到处理不会修改已经记录的操作身份。

可选认证只确认身份，不决定接口访问资格。Validator 仍先检查请求参数，Controller 随后按接口规则读取身份并执行访问检查；匿名且参数非法的请求因此可以先返回 400，参数通过后才在受保护 Controller 返回缺少身份的 401。不存在的路由沿用 `NoRoute` 的 404，不通过 `/api` 分组补做认证。完整头部规则和身份读取方式见[HTTP 认证适配层](api-authentication.md#7-http-认证适配层)。

Gin 为本次请求准备 `*gin.Context`，其中的 `Request` 提供原始 HTTP 请求，`Writer` 用于写出响应。Validator、Controller 和响应方法接收的是同一个 `ctx`；它属于单次请求，不保存在共享对象中。这里的 Gin Context 与启动入口用于停止进程的 `context.Context` 是不同对象。

### 3.3 Validator 创建 DTO，Bind 填充字段

进入 [CreateUserValidator.CreateUser](../../app/domains/user/validator/v1/user_profile/create_user_validator.go) 后，首先为本次请求创建 DTO：

```go
var request userprofiledto.CreateUserRequest
if err := commonvalidator.Bind(ctx, &request); err != nil {
    response.Fail(ctx, response.InvalidArgument(err.Error()))
    return
}
```

[internal/validator/bind.go](../../internal/validator/bind.go) 是通用绑定入口：

| HTTP 方法 | 参数来源 | 映射实现 | DTO 标签 |
| --- | --- | --- | --- |
| GET | Query | Gin 的 `binding.MapFormWithTag` | `form` |
| POST | JSON 请求体 | 标准库的 `json.NewDecoder(...).Decode` | `json` |

POST 需要 `Content-Type: application/json`。绑定器不把 Query 和 JSON 合并，也不为每个用例手工解析字段。这两条映射路径都不调用 Gin 的自动校验器，也不修改进程全局的校验器设置。

[CreateUserRequest](../../app/domains/user/dto/v1/user_profile/create_user.go) 通过字段及标签描述参数，例如：

```go
AccountSource string  `form:"account_source" json:"account_source"`
AvatarURL     *string `form:"avatar_url" json:"avatar_url"`
```

GET 映射器或 JSON 解码器根据对应标签填入 DTO，并完成类型转换。JSON 语法错误、输入与字段类型不兼容等属于绑定失败，Validator 入口立即返回 HTTP 400，Controller 和 Service 都不会执行。

绑定只负责把输入变成 Go 数据。Request DTO 禁止使用 `binding` 等自动校验标签，也不自定义 `UnmarshalJSON`；字段是否必填、是否为空、长度或范围是否超限等规则，由下一步的私有校验函数显式判断，可继续调用 `check` 和 models 的规则。相关约束见[请求 DTO 规范](api-routing.md#81-request-dto-的标准绑定与输入语义)。

通用绑定沿用上述映射器、解码器的默认行为，不另外增加未知字段、重复参数、尾部 JSON 或请求体大小的拒绝策略。

### 3.4 私有校验函数判断输入是否有效

同一个 validator 文件内的 `validateCreateUserRequest` 组织该用例的字段校验，创建头像的可选分支在这里处理。账户、昵称和头像的完整字段规则统一调用 [models 的领域规则](../../app/domains/user/models/user_profile_validation.go)，由领域规则组合公共数据检查：

| 字段 | 当前规则 |
| --- | --- |
| `account_source` | 非空白、合法 UTF-8，最多 32 个 Unicode 码点 |
| `external_account_id` | 非空白、合法 UTF-8，最多 128 个 Unicode 码点 |
| `user_name` | 非空白、合法 UTF-8，最多 64 个 Unicode 码点 |
| `avatar_url` | 创建时可省略、为 `null` 或空字符串；非空时须为有效 HTTP/HTTPS 地址，最多 512 个 Unicode 码点 |

校验针对绑定后的字段，不改变原值，也不查询数据库。领域规则直接返回公共检查错误，Validator 确认属于输入违规后，使用当前请求字段名和错误的 `Error()` 构造 `response.FieldError`。问题按字段顺序汇总，每个字段最多一条。`RuleError` 或无法识别的错误通过第二个返回值交给入口处理，不混入参数明细。

入口方法根据校验结果决定是否继续：

```go
details, err := validateCreateUserRequest(&request)
if err != nil {
    log.Printf("Validate CreateUser request failed: error=%v", err)
    response.Fail(ctx, response.ErrorInfo{
        Code: response.CodeInternalError, Message: err.Error(),
    })
    return
}
if len(details) > 0 {
    response.Fail(ctx, response.InvalidArgument(details[0].Error(), details...))
    return
}
v.controller.CreateUser(ctx, &request)
```

因此，到达 Controller 时，传入的是已经完成绑定和请求校验的 `*CreateUserRequest`。DTO 直接作为方法参数传递，没有存入 Gin Context 再进行类型断言。

### 3.5 Controller 将 DTO 转成 Service 参数

[UserProfileController.CreateUser](../../app/domains/user/controller/v1/user_profile_controller.go) 接收 `ctx` 和已校验的 DTO，先调用 Service：

```go
ctx.Header("Cache-Control", "no-store")
profile, err := composition.Current().UserProfile.CreateUser(ctx.Request.Context(), userservice.CreateUserParams{
    AccountSource:     request.AccountSource,
    ExternalAccountID: request.ExternalAccountID,
    UserName:          request.UserName,
    AvatarURL:         request.AvatarURL,
})
if err != nil {
    failUserProfileOperation(ctx, err)
    return
}
```

Controller 先设置 `Cache-Control: no-store`，避免后续含令牌的响应被缓存；设置 Header 本身还没有写出正文。随后将请求字段组织为 [CreateUserParams](../../app/domains/user/service/user_profile_service.go)，调用启动时已经创建的 Service。创建失败时，通过本文件的 `mapUserProfileErrorCodes` 选择通用码和业务码，由 `failUserProfileOperation` 使用错误的 `Error()` 输出响应，立即返回，不签发凭证。创建成功后的签发及输出继续见第 3.8 节。

这一步把 HTTP 契约与业务调用分开。Service 统一接收标准 `context.Context` 首参和自己的 Params，不接收 Gin Context 或 Request DTO；Controller 负责接口访问检查、输入映射和后续响应转换，并始终传入当前内部 Request Context。创建接口允许匿名，因此可以直接构造创建 Params；要求身份的其他入口调用 `authz.RequirePrincipal` 或 `authz.RequireMemberUserID`，处理其返回错误，再执行接口自己的目标访问检查并构造业务 Params，具体见第 6 节。

Controller 的业务入参只有 Request DTO，`ctx` 是独立的 HTTP 上下文。Validator 不构造 Service Params；需要将 ID 文本转换为整数等操作时，由 Controller 在构造 Params 时完成。Validator 已按相同解析语义验证成功、且 DTO 值未被修改的纯类型转换，允许忽略错误返回值。例如，按 ID 查询接口在 Controller 中使用 `userID, _ := strconv.ParseInt(request.UserID, 10, 64)`。

### 3.6 Service 决定如何创建用户

[UserProfileService.CreateUser](../../app/domains/user/service/user_profile_service.go) 在入口对非 nil Context 调用 `logger.WithCurrentOperation` 建立 Service 操作作用域，再调用私有 `validateCreateUserParams`，用于保障非 HTTP 调用也遵守输入约束。同一 Service 文件顶部先声明共享及用例专属错误，再按用例依次组织 Params、可选 Result、输入常量、参数校验和处理方法；Result 紧接 Params，跨用例复用的实现放在文件尾部的 `shared` 区域。创建结果就是 `models.UserProfile`，因此这个用例直接返回模型，不声明 `CreateUserResult`。

当前六个用户资料 Service 公开业务方法均已统一标准 Context 首参，Controller 调用同步传递内部 `ctx.Request.Context()`；查询和写入也遵守这一约定。非 HTTP 入口负责提供本次执行的非 nil Context，纯参数校验、映射和构造函数无需机械添加。接收 Context 与建立作用域并不自动输出流程日志，现有标准库业务日志仍待迁移；也不自动将 Context 绑定到 Repository 或 GORM，既有 SQL 和事务执行方式保持不变。完整约定见[Service Context 约定](api-service-validator.md#service-context-约定)。

Validator 和 Service 分别组织各自入口的校验，共同调用 models 的字段规则，不各自维护长度或 URL 判断。用例的批量、分页和版本边界及分页默认值声明在 [Service 文件](../../app/domains/user/service/user_profile_service.go) 各用例的 Params 旁，由 Validator 和 Service 引用；默认值由入口准备，Service 不自动改写非法值。

输入通过后，Service 从 `appruntime.Current().Repositories.UserProfile` 取得共享仓储，并执行创建流程：

1. 调用 `snowflake.NextID()` 获取内部用户 ID。发号失败或返回非正 ID 时，终止本次创建。
2. 构造 `tables.UserProfile`，填入账户身份、昵称和头像。
3. 明确设置初始 `IsEnabled = true`、`Version = 0`。
4. 将头像转换为 `sql.NullString`，交给 Repository 写入。
5. 写入成功后，把回填后的表对象转换为 `models.UserProfile` 返回。

头像为 `nil` 时转换为 SQL `NULL`；显式空字符串保留为空字符串。这是创建接口当前允许的输入语义。

Service 没有在这个创建流程中显式开启跨步骤事务。单次 GORM 写入是否使用默认事务，由数据库连接配置决定；更新接口的显式事务流程见第 6 节。

### 3.7 Repository 执行数据库写入

[UserProfileRepository.CreateProfile](../../app/domains/user/repository/user_profile_repository.go) 使用启动时注入的 GORM 数据库入口，访问完整表名 `user_service.user_profile`。

它处理的是 [tables.UserProfile](../../app/domains/user/tables/user_profile.go)，流程为：

1. 复制传入的表对象，避免失败时修改调用方的数据。
2. 填写创建、更新时间，使用 UTC 并对齐毫秒精度。
3. 调用 GORM 的 `Create`，由 GORM 经数据库驱动和连接池向 MySQL 执行写入。
4. 写入成功后，才将本次写入结果回填到传入对象。

表及唯一索引定义见 [database/user/mysql/user_profile.sql](../../database/user/mysql/user_profile.sql)。其中 `(account_source, external_account_id)` 联合唯一，数据库负责保证并发创建时不会出现重复账户身份。

Repository 返回原始 GORM 或驱动错误，不在这里决定 HTTP 状态码、业务错误码，也不输出 JSON。

### 3.8 创建结果返回后签发凭证，再写成 JSON

1. **Service 返回已创建的资料，Controller 据此确定签发身份。** Service 使用 `profileModel` 将回填后的表对象转换为 [models.UserProfile](../../app/domains/user/models/user_profile.go)，成功路径不再查询一次数据库。Controller 使用固定的 `authn.PrincipalKindUser`、`identity.UserNamespaceMember` 和 `strconv.FormatInt(profile.ID, 10)` 构造 `Principal`，通过 `authnjwt.Current().Issue(ctx.Request.Context(), principal)` 签发 JWT。身份来源是本次创建结果，不能从请求中读取或覆盖；Service 和 Repository 不参与凭证签发。

2. **签发成功后，Controller 将资料与凭证组合为用例响应。** [CreateUserResponse](../../app/domains/user/dto/v1/user_profile/create_user.go) 是独立结构体，其中 `UserProfile` 字段复用同组 [UserProfile](../../app/domains/user/dto/v1/user_profile/user_profile.go) DTO。资料字段由 `userProfileDTO(profile)` 转换；`AccessToken`、`TokenType` 和 `ExpiresAt` 分别直接采用签发结果的 `Credential.Value`、`Credential.Scheme` 和 `ExpiresAt`。到期时间不由 Controller 再次计算。

3. **只有创建和签发都成功，才写出一次成功响应。** Controller 沿用入口处设置的 `Cache-Control: no-store`，由 [response.Success](../../internal/response/response.go) 完成外层包装：

   ```go
   result := userprofiledto.CreateUserResponse{
       UserProfile: userProfileDTO(profile),
       AccessToken: issued.Credential.Value,
       TokenType:   issued.Credential.Scheme,
       ExpiresAt:   issued.ExpiresAt,
   }
   response.Success(ctx, result)
   ```

   公共响应组件先将包含 `data/error` 的完整对象编码成 JSON 字节，再设置状态码、响应头并写入正文。写入先进入外层中间件的响应缓冲区，由外层决定是否发送；编码失败和写入失败的职责见第 5.3 节，超时后丢弃结果的规则见第 5.4 节。

   示例响应如下，用户 ID、令牌占位文本和到期时间仅用于说明结构，不是可用凭证：

   ```json
   {
     "data": {
       "user_profile": {
         "user_id": "1234567890123456789",
         "user_name": "Example User",
         "avatar_url": null,
         "is_enabled": true,
         "version": 0
       },
       "access_token": "<signed-jwt>",
       "token_type": "Bearer",
       "expires_at": "2026-11-06T10:00:00Z"
     },
     "error": null
   }
   ```

4. **响应字段按各自契约序列化，凭证正文不混入资料 DTO。** HTTP 状态码为 200，成功结果位于 `data`，`error` 为 `null`。`data.user_profile.user_id` 的十进制字符串由共享 DTO 的 `json:"user_id,string"` 标签控制；`data.expires_at` 使用 UTC RFC3339 字符串。账户来源、外部账户标识和持久化时间保留在内部数据中，不因签发凭证而新增到资料响应。统一响应由 `Success` 或 `Fail` 显式写出，框架不会在任意返回值外自动再包装一层 JSON。

5. **签发失败返回 500，但不撤销已创建的用户。** Controller 记录包含新建 `user_id` 和原始错误的英文日志，不记录令牌，然后返回 `INTERNAL_ERROR` 与原始 `err.Error()`，不附业务码，立即结束方法。不能先返回资料成功，再追加签发错误。重复账户在 Service 失败分支已终止，不会为已有用户补发凭证。

6. **等待超时后，创建和签发可以继续完成，但凭证响应会被丢弃。** 本接口外层等待 10 秒，超过期限返回 503；传入 `Issue` 的 Request Context 已由等待中间件隔离取消，不会因响应等待结束而取消签发。已经创建的记录不回滚，晚到凭证不发送给客户端。丢失结果时不能通过重复匿名注册取回令牌，本次不提供凭证恢复机制；其他接口收到后续请求时，仍须重新认证凭证并执行各自的访问检查。

## 4. 数据对象为什么会多次转换

创建接口的数据形态依次为：

```text
HTTP JSON
  → CreateUserRequest
  → CreateUserParams
  → tables.UserProfile
  → Repository / MySQL
  → 回填后的 tables.UserProfile
  → models.UserProfile
  → Controller 以新建 ID 构造 Principal，签发 IssuedCredential
  → CreateUserResponse
  → response.Response[T]
  → HTTP JSON
```

| 对象 | 表达什么 | 转换发生在哪里 |
| --- | --- | --- |
| Request DTO | 当前接口的输入字段、JSON/Query 格式 | 公共 Bind 通过 Query 映射或 JSON 解码填充 |
| Service Params | 业务用例接受的输入 | Controller 手工映射 |
| `authn.Principal` | 已验证的调用方类别、命名空间与主体标识 | 可选认证中间件验证成功后保存；Controller 读取并转换为业务所需的发起方参数 |
| `tables.UserProfile` | 数据库列、GORM 标签、SQL 可空值 | Service 构造，Repository 写入或读取 |
| `models.UserProfile` | 业务层返回的用户资料 | Service 的 `profileModel` 转换 |
| Service `<UseCase>Result` | 某个用例组合的业务结果，例如资料列表和总数、缺失 ID | Service 组织；类型紧接同文件对应 Params 声明 |
| `authn.IssuedCredential` | 签发凭证的原文、方案及有效时间 | Controller 根据新建资料构造身份，调用公共 JWT 签发器取得 |
| 用例 Response DTO | 当前接口允许对外返回的字段 | Controller 从模型或 Result 映射，资料字段由 `userProfileDTO` 转换 |
| `response.Response[T]` | 统一的 `data/error` 外层 | `response.Success` 或 `response.Fail` 构造 |

当前 Repository 直接使用 `tables`，不接收或返回 `models`。当前 `models.UserProfile` 是业务字段结构，其校验和业务流程由现有 Validator、Service 实现。

单条查询、创建和更新的 Service 结果就是 `models.UserProfile`，直接返回该模型。批量查询的 `BatchQueryUserProfilesResult` 将 `UserProfiles` 和 `MissingUserIDs` 放在一起；分页查询的 `QueryUserProfilePageResult` 包含 `UserProfiles` 和 `TotalCount`。这两个结构都声明在 Service 对应用例的 Params 下方，Controller 再将它们映射成各自的 Response DTO，HTTP 字段及外层结构保持一致。返回类型和空结果约定见[Service 的输出契约](api-service-validator.md#74-service-的输出契约)。

这些转换只有 HTTP 到 Request DTO 的部分交给公共 Bind 调用标准映射、解码能力完成。其余映射在 Controller 和 Service 中明确编写，阅读时可以直接跟随字段赋值查看数据去向。

## 5. 失败时，请求在哪里结束

失败可以在不同阶段发生，不需要把所有错误都交给 Service。

| 失败位置 | 示例 | 当前处理结果 | 是否继续后续业务调用 |
| --- | --- | --- | --- |
| Gin 路由匹配 | 方法或路径未匹配 | 404、`NOT_FOUND`、无业务码 | 不进入用例 Validator |
| 全局可选认证 | 已提供的 Authorization 格式错误、凭证无效或认证故障 | 分别返回 400、401 或 500 | 不进入 Validator；没有该头时匿名继续 |
| 通用绑定 | JSON 格式错误、字段类型不匹配 | 400、`INVALID_ARGUMENT` | 不进入 Controller |
| 请求字段校验 | 昵称为空、字段超长 | 400、`INVALID_ARGUMENT`、字段明细 | 不进入 Controller |
| Controller 接口访问检查 | 匿名调用受保护接口、身份不符或目标超出访问范围 | 缺少或无效身份返回 401，权限不足返回 403 | 不调用对应 Service |
| Service 业务处理 | 更新时实际版本与提交版本不同 | 409、`CONFLICT`、`USER_PROFILE_VERSION_CONFLICT` | Controller 输出错误 |
| Repository 查询 | 目标资料未找到 | 404、`NOT_FOUND`、`USER_PROFILE_NOT_FOUND` | Service 解释为资料不存在，Controller 分类并输出 |
| Service 或数据库 | 发号失败、未预期的 SQL 错误 | 500、`INTERNAL_ERROR`、无业务码 | Controller 直接输出收到的错误文案 |
| Controller 签发凭证 | 用户已创建，但 JWT 签发返回错误 | 500、`INTERNAL_ERROR`、无业务码，使用原始错误文案 | Controller 记录错误并结束，不回滚已创建的用户 |
| response 编码 | DTO 无法编码为 JSON | 尚未写出时尝试返回默认 500 | response 记录原因并终止后续处理 |
| response 写入 | 当前 Writer 返回写入错误或短写 | 不再追加错误响应 | response 记录原因并终止后续处理 |
| 外层网络写入 | 连接错误或正文未完整写入 | 不再追加错误响应 | 响应等待中间件记录实际发送失败 |
| 响应等待中间件 | 等待期限内未取得完整响应 | 503、`SERVICE_UNAVAILABLE`、结果尚未确认 | 不主动取消已开始的业务；后到响应丢弃 |
| 请求处理链 | 未处理的 `panic` | 交给公共异常恢复中间件 | 结束当前请求处理链 |

### 5.1 内部错误如何变成接口错误

1. **Service 解释业务失败，models 检查领域规则，两层都不声明新的 error 类型。** 需要命名错误时使用 `errors.New`，Service 的声明放在[文件顶部](../../app/domains/user/service/user_profile_service.go)。发号器成功返回非正 ID、用户状态不允许修改、提交版本与实际版本不一致，由 Service 自己判断；目标资料明确未命中，则由 Service 解释为资料不存在。公共检查和 models 已表达清楚的输入错误、数据库连接或执行故障、发号失败等原样返回，不增加包装或替换成统一失败错误。

2. **Controller 识别 Service 返回的错误，只选择通用码和业务码。** [Controller](../../app/domains/user/controller/v1/user_profile_controller.go) 在文件顶部声明业务码，由 `mapUserProfileErrorCodes` 集中分类。以资料查询未命中为例，Repository 返回 `gorm.ErrRecordNotFound`，Service 返回 `ErrUserProfileNotFound`；Controller 识别这个业务错误，选择 `NOT_FOUND` 和 `USER_PROFILE_NOT_FOUND`。`failUserProfileOperation` 使用收到的 `err.Error()`，不重新组织文案或补造字段明细。

   查询未命中的响应示例：

   ```json
   {
     "data": null,
     "error": {
       "code": "NOT_FOUND",
       "business_code": "USER_PROFILE_NOT_FOUND",
       "message": "user profile not found"
     }
   }
   ```

   HTTP 状态码为 404。`code` 表达通用类别，`business_code` 表达具体业务原因；通用类别与 HTTP 状态码的映射由 [internal/response/error.go](../../internal/response/error.go) 统一维护。

3. **重复键只有确认业务条件后，才能返回账户已存在。** Service 通过 [mysql.IsDuplicateKey](../../internal/database/mysql/error.go) 识别重复键；MySQL 编号和 GORM 错误兼容逻辑集中在数据库组件，Service 不自行比较魔法数字。该方法不判断具体约束或业务含义，也不修改原始错误。确认属于重复键后，Service 按账户来源及外部标识复查；成功找到该账户后，返回 `ErrAccountAlreadyExists`，由 Controller 输出 HTTP 409、`CONFLICT`、`USER_ACCOUNT_ALREADY_EXISTS` 和 `user account already exists`。这个判断确认账户已经存在，不声称识别了实际冲突索引。复查明确未命中时，原样返回最初的写入错误；复查发生技术故障时，原样返回复查错误，并在日志中记录两次失败原因，不用 `errors.Join` 合并错误。不能仅凭重复键类别将任意冲突归为账户已存在。

4. **执行故障和第三方文案沿同一条原样返回链路处理。** Service 可以在故障出口记录操作上下文，随后仍返回原错误。Controller 对无法识别的失败选择 `INTERNAL_ERROR`，响应消息直接采用收到的 `err.Error()`，包括第三方数据库错误的原始文案。项目自行声明的错误提供可公开的非空白英文说明；第三方文案不翻译、不脱敏、不替换。框架异常恢复仍使用默认的 `internal server error`。完整约定见[错误声明、传递与响应规范](api-error.md)。

### 5.2 Fail、return 和异常恢复各负责什么

`response.Fail` 先中止后续 Gin 处理链，再通过公共输出路径编码、写出统一错误 JSON。**它不会自动结束当前 Go 函数**，调用处仍需紧接 `return`，避免继续访问数据库或重复写出响应。`Success` 作为 Controller 的最后一步调用；正常成功输出不额外中止处理链，遇到输出故障时由 response 中止后续处理。

普通 `error` 通过上述分支显式处理；`panic` 由 `gin.CustomRecovery` 捕获。尚未写出响应时，恢复回调输出统一内部错误；响应头或正文已经写出时，只终止后续处理，不覆盖原状态或追加第二段 JSON。

### 5.3 响应编码和写入失败在哪里处理

1. **编码失败时，由 response 在写出前准备兜底响应。** 例如 DTO 包含 JSON 无法表达的值，完整响应编码会返回错误。response 记录原始原因并中止后续处理链；尚未写出时，尝试返回 HTTP 500 和默认的 `INTERNAL_ERROR`、`internal server error`，不把编码错误作为业务错误交回 Controller，也不递归生成兜底响应。

2. **写入当前 Writer 失败时，由 response 记录并停止输出。** 组件检查 Writer 返回的写入错误或短写，不重试、不再追加一份错误 JSON；进入输出入口时如果响应已经写出，同样只记录并终止。这一步写入的是缓冲区，缓冲成功不保证最终网络发送成功，更不证明客户端已完整收到响应。

3. **输出失败不改变已经完成的业务结果。** 编码或写入发生在 Service 返回之后，响应组件不会重新创建用户，也不能据此撤销已经提交的数据库操作。该阶段日志由 response 负责，Controller 无须重复记录；错误处理的完整约定见[接口返回规范第 7 节](api-response.md#7-响应编码与写出失败)。

4. **缓冲写入与实际网络写入分开负责。** response 仍负责 JSON 编码及写入当前 Writer；外层中间件负责把获选的完整响应发送到真实 HTTP Writer，并记录该阶段的写入错误或短写。例如用户创建成功后客户端关闭连接，网络发送失败由外层记录。已经超时而被丢弃的结果不再尝试网络发送；任何阶段都不能因输出失败追加第二份响应。

### 5.4 响应等待超时不取消内部业务

**响应等待超时只结束客户端对本次 HTTP 响应的等待，不决定业务成功、失败或回滚。** 这项能力适用于完整返回的普通 JSON 接口，不用于流式响应、SSE、WebSocket 或需要边处理边发送的接口。

1. **等待时间集中在 HTTP 装配层配置，每次请求只选择一个等待期限。** [routers/http_handler.go](../../routers/http_handler.go) 的 `defaultResponseTimeout` 声明统一默认值，`NewHTTPHandler` 中的 `overrides` 声明接口覆盖项；构造时调用 `NewResponseTimeout(timeout)`、`Wrap(next)`，为同一个 Gin Engine 预先创建包装器。每个时长必须是正的 `time.Duration`，零值和负值属于装配错误。

   | 适用请求 | 响应等待时间 |
   | --- | --- |
   | 未单独覆盖的请求 | 3 秒 |
   | `GET /api/v1/user/userProfile/queryUserProfilePage` | 5 秒 |
   | `POST /api/v1/user/userProfile/createUser` | 10 秒 |

   **覆盖项按 HTTP 方法和 `URL.Path` 精确匹配，Query 参数不参与匹配。** 例如分页查询无论使用哪一页或哪些筛选条件，都采用 5 秒；同一路径的其他方法不命中此覆盖项。未配置的接口采用默认 3 秒，未知路径也进入默认包装器，再由 Gin 返回路由不存在。覆盖包装器与默认包装器是并列选择，不能先套 3 秒再套 10 秒，否则创建接口仍会在 3 秒结束等待。新增接口通常只注册领域路由；只有需要不同等待时间时，才在中央配置增加覆盖项，修改后重启生效。

   **等待时间从标准库包装器开始处理本次请求时计算，不等于数据库执行时限。** 例如配置为 3 秒，绑定、校验、业务执行和响应准备共用这 3 秒；它不包含进入包装器之前读取请求头的时间。Controller、Validator、Service 和 Repository 不各自设置同一个 HTTP 等待期限。启动超时、HTTP 网络读写超时、数据库网络超时和进程收尾超时分别控制各自阶段，不能相互代替。

2. **计时、内部协程、响应缓冲和最终响应选择复用 `http.TimeoutHandler`。** 内部 Handler 的响应先进入标准库缓冲，外层只发送正常结果或超时结果中的一个；完成和超时同时就绪时也只选择一次，不承诺固定优先级。项目不再实现另一套定时器或缓冲 Writer，只适配统一 JSON、取消隔离、内部工作收尾和必要的输出日志。

   **中间件包装整个 Gin Handler，不让两个执行流程共享同一个 Gin Context。** 不能在 Gin 中间件中简单启动 `go ctx.Next()` 后提前返回，否则 Gin Context 可能在内部逻辑结束前被归还对象池。response 的 `Written()` 检查也不能代替并发同步或响应归属管理。

3. **超时返回 HTTP 503 和统一的 `data/error` 结构，明确业务结果尚未确认。** 使用既有通用类别 `SERVICE_UNAVAILABLE`，不附 `business_code` 或字段明细；固定消息为 `response wait timeout; operation outcome is unknown`。这是框架的响应等待结果，不是 Service 返回的业务错误，也不是参数错误。

   ```json
   {
     "data": null,
     "error": {
       "code": "SERVICE_UNAVAILABLE",
       "message": "response wait timeout; operation outcome is unknown"
     }
   }
   ```

   **客户端不能把这份响应当作业务失败或回滚证明。** 客户端展示“未及时取得结果，请稍后确认”；写接口按其契约提供幂等或结果确认方式，不能收到超时就无条件重复提交。客户端自行断开时，也不能保证超时响应仍可送达。

4. **响应等待期限和客户端断开不向已开始的业务传播取消。** 标准库创建的超时 Context 只控制外层等待；适配层通过 `context.WithoutCancel` 创建内部 Context，再用 `Request.Clone` 交给 Gin，保留 Context 值但隔离取消及截止时间。不把 HTTP 等待期限传给 Service、Repository，也不通过 Gin 的 `Abort` 打断业务。内部处理继续遵守原有校验、业务条件和事务规则：正常完成则返回结果，发生自身错误则终止适用步骤并回滚必要事务。

   **继续执行以已经具备完整业务输入为前提，不承诺最终一定完成。** 请求体尚未读取完整时，连接关闭可能导致绑定失败，此时不能声称业务已经开始。对于携带请求体的 HTTP/1 请求，中间件提前返回超时等框架错误时关闭连接复用，避免 HTTP 层为了消费剩余请求体而阻塞错误响应。数据库故障、独立执行限制、进程崩溃等也仍可能终止业务。多步骤写入的一致性由事务或明确的业务机制保障，不因采用响应等待超时而取消这些保障。

5. **已经发送超时响应后，后续结果只用于内部收尾，不再发送给本次客户端。** 标准库拒绝晚到正文；适配层把因超时或取消产生的预期写入拒绝视为丢弃，避免误记为网络故障。超时之前已经缓存的正文不保证立即释放，内部处理结束、对象不再被引用后才可回收。业务错误仍由原发生层记录必要上下文，不能因为客户端已经结束等待而忽略失败。

   **异常恢复仍由完整 Gin Handler 中的既有 Recovery 负责。** 适配层记录逃逸的 panic 后交回标准库处理，确保晚到异常仍有日志；它不提供任意原始 Handler 已输出部分正文后重新生成完整 500 的额外保证。

   **HTTP 日志表达选定的对外响应，内部处理结果仍需由其所属层记录。** `HTTPLogging` 包装响应等待层，观察外层 Writer；即使内部稍后准备了 200，也不会覆盖已经记录的对外 503，外层的 503 也可能来自业务或停机，不能一概归为超时。`finished` 表示外层响应处理结束，不代表业务结束或客户端必然收到正文。丢弃响应不触发重跑业务、不反向撤销已经提交的数据，也不保证客户端能够得知最终状态。

6. **内部执行的生命周期由框架跟踪，HTTP 响应结束不代表工作已经结束。** 中间件登记每次内部 Handler 的开始与退出，即使超时响应已经发送，也要保留仍在执行的工作记录，供停机收尾等待。处理器自行启动的额外后台任务不自动纳入这份记录，需要由对应任务生命周期另外管理；停机顺序见第 7 节。

## 6. 其余接口如何套用这条链路

五个用户资料接口都经过全局可选认证，再进入各自的 Validator；Validator 文件均按“结构体、构造函数、入口方法、私有校验函数”组织。各 `validateXxxRequest` 完整组合请求字段规则，基础类型检查复用 `internal/check`，领域字段复用 models，用例边界和默认值引用 Service Params 的声明。要求身份的 Controller 在字段通过后调用 `authz` 共用检查，再按 [user 领域权限矩阵](../domains/user/api-authorization.md#3-五个接口的权限矩阵)执行目标访问规则；公开创建入口不调用这些要求身份的方法。Validator 不读取身份，共用方法不决定对象归属。

| 接口 | Validator 入口 | 相比创建接口的主要差异 |
| --- | --- | --- |
| `queryUserProfileByUserId` | [QueryUserProfileByUserID](../../app/domains/user/validator/v1/user_profile/query_user_profile_by_user_id_validator.go) | GET Query 绑定目标 ID；Controller 调用 `RequireMemberUserID`，直接检查目标关系，再构造 `OperatorUserID` 和 `UserID`；Service 保障用例约束并查询 |
| `queryUserProfilePage` | [QueryUserProfilePage](../../app/domains/user/validator/v1/user_profile/query_user_profile_page_validator.go) | 绑定前填默认页码 1、每页 20；Controller 调用 `RequirePrincipal` 检查身份范围，再取得 Service 的资料列表和总数 |
| `batchQueryUserProfiles` | [BatchQueryUserProfiles](../../app/domains/user/validator/v1/user_profile/batch_query_user_profiles_validator.go) | 绑定 ID 字符串数组；Controller 调用 `RequirePrincipal` 并转换业务输入；Service 去重后返回资料与缺失 ID |
| `updateUserProfile` | [UpdateUserProfile](../../app/domains/user/validator/v1/user_profile/update_user_profile_validator.go) | Validator 校验目标、版本和完整资料；Controller 调用 `RequireMemberUserID` 并直接检查目标关系，以结果构造 `OperatorUserID`；Service 使用事务和版本条件更新 |

补充阅读时容易混淆的细节：

- 分页默认值仅在未提供参数时保留，显式零值会被校验拒绝。状态筛选先绑定成可选字符串，只允许 `true`、`false`，Controller 再映射为 Service 的可选布尔值。
- 分页总数与本页数据分别读取，固定按用户 ID 降序，不承诺并发写入期间来自同一个数据库快照。
- 批量查询的数量限制按去重前计算。部分或全部用户不存在仍是成功查询，缺失 ID 放在 Result 字段中；成功时资料列表和缺失 ID 列表均为非 nil 切片。分页无匹配记录时仍返回 Result，列表为空并保留实际总数。Service 执行失败返回 `nil, err`，不提供可继续使用的部分结果。
- 更新版本使用 `*int64`，缺失或 `null` 不合法，版本 `0` 合法。更新头像是必填的有效 URL 字符串，这个 HTTP 接口不允许清空；创建及查询结果中的可空头像仍保留原有语义。
- Request DTO 中已校验的目标 ID 文本允许按同一规则再次解析并忽略错误；Principal 的 `SubjectID` 未经过该 Validator，由 `RequireMemberUserID` 明确检查转换及正数要求，Controller 必须处理其错误。查询和更新 Params 的 `OperatorUserID` 来自该成功结果，不能接受请求自报值。
- `authz` 方法不写响应或设置挑战头。它返回身份或错误后，Controller 决定错误码、业务码和 `WWW-Authenticate`，输出失败后立即 `return`；目标不是本人等规则由 Controller 直接判断，共用方法不会根据目标 ID 拒绝访问。
- 更新 Service 先判断本人操作，再在事务中锁读用户，检查启用状态及实际版本，执行带版本条件的更新，读取更新结果，提交成功后才返回。事务内的 Repository 使用本次 `tx`，不会切回普通连接。

## 7. 请求完成与进程退出

### 7.1 HTTP 响应结束与内部处理结束

1. **正常路径在内部处理完成后输出响应，并释放本次执行资源。** Controller 和 Validator 依次返回，Gin 记录内部处理状态与耗时，由外层发送获选响应。Writer 返回成功不保证客户端已完整收到结果，输出失败仍按第 5.3 节处理。

2. **超时路径先结束 HTTP 等待，内部处理结束后才完成本次业务收尾。** 超时后的处理仍可能持有事务、数据库连接及本次请求的内存；中间件要等内部 Handler 退出后，才从待收尾记录中移除。共享 Service、Repository 和连接池继续供运行中的请求使用，不能在发送超时响应时关闭。

### 7.2 停止接收、等待收尾与关闭资源

1. **进程退出先停止接收新请求，再依次等待 HTTP 请求和内部处理。** [进程入口](../../cmd/web/main.go) 先调用 `http.Server.Shutdown(ctx)`，关闭监听并等待普通在途 HTTP 请求；随后调用 `HTTPHandler.Shutdown(ctx)`，遍历默认配置与接口覆盖使用的所有响应等待组件，停止接纳内部执行，并等待已经登记的 Handler 退出。即使 HTTP 收尾失败，也必须继续调用内部收尾入口；某个组件等待失败时，也不能漏掉其他组件。已经返回超时但仍在运行的业务由这一步继续跟踪。

2. **两次收尾共用一个独立总预算，不复用已取消的进程通知或 HTTP 等待 Context。** `-shutdown-timeout` 指定总预算，默认 10 秒；入口创建独立的标准 Context，依次传给 HTTP 服务和装配对象的 `Shutdown`，不会给每个组件重新分配完整预算。响应等待时间只影响单次响应，不能决定数据库连接池什么时候关闭；HTTP 层排空也不能代替内部处理排空。

3. **共享资源只在 HTTP 和内部处理均确认结束后正常关闭。** 两部分收尾均成功时，入口才调用 company、thirdparty 集合的 `CloseIdleConnections()` 清理 HTTP 空闲连接，并关闭数据库。thirdparty 按 AI、LLM 分组逐层清理 Qwen、MiniMax 的空闲连接，不取消在途调用；装配后续实例失败时也清理已创建的实例。启动装配、路由或监听失败、尚未开始处理请求时，应正常释放已创建的资源。HTTP 收尾失败可以强制关闭 HTTP 连接，但这不能取消内部跟踪，更不能据此提前关闭仍被业务使用的连接池。

   `run` 在应用初始化前登记最终 `logger.Sync()`，在已有 Client、数据库清理后同步日志；初始化失败或取消时也会同步已经创建的 Logger，尚未初始化时同步为空操作。共享 Logger 没有内部缓冲，stdout 只暴露写入能力，同步不关闭 stdout，也不调用终端或管道不支持的文件系统同步。

4. **预算耗尽后仍有内部工作时，按异常停机处理，不报告业务已完成。** 入口记录未完成工作，跳过主动关闭 company、thirdparty 和数据库的共享资源，并跳过 Logger 同步，避免等待仍在写日志的内部工作；随后以失败状态退出。进程退出会释放连接，但不保证执行中的业务完成，也不证明事务已回滚。有限停机预算无法保证内部业务在进程终止后继续执行，客户端仍需按结果未知处理。`HTTPHandler` 同时提供请求处理和收尾入口，进程必须持有同一个装配对象，不能只保存其处理函数后遗失收尾责任。

## 8. 第一次看代码时的阅读顺序

建议先完整跟随 `createUser`，再对照其他接口：

| 顺序 | 文件 | 重点查看 |
| --- | --- | --- |
| 1 | [cmd/web/main.go](../../cmd/web/main.go) | `run`、`serve`：依赖初始化与 HTTP 服务启动、关闭 |
| 2 | [bootstrap/init.go](../../bootstrap/init.go) | `Init`：启动顺序 |
| 3 | [internal/runtime/runtime.go](../../internal/runtime/runtime.go)、[internal/composition/services.go](../../internal/composition/services.go) | DB、Repository、Service 分别在哪里创建与保存 |
| 4 | [routers/http_handler.go](../../routers/http_handler.go)、[routers/web.go](../../routers/web.go) | 默认和接口覆盖的等待时间、外层 Handler、Gin、中间件、API 前缀和版本 |
| 5 | [用户领域 Router](../../app/domains/user/routers/router.go) | 完整路径如何拼接、实际注册哪个 Validator 方法 |
| 6 | [create_user_validator.go](../../app/domains/user/validator/v1/user_profile/create_user_validator.go)、[bind.go](../../internal/validator/bind.go) | 请求绑定、字段校验、失败时提前返回 |
| 7 | [create_user.go](../../app/domains/user/dto/v1/user_profile/create_user.go)、[user_profile.go](../../app/domains/user/dto/v1/user_profile/user_profile.go) | 请求字段类型及标签、专属响应类型及同组共享字段 |
| 8 | [user_profile_controller.go](../../app/domains/user/controller/v1/user_profile_controller.go)、[共用身份检查](../../app/http/authz/principal.go)、[成员 ID 转换](../../app/http/authz/member.go) | 创建流程、身份要求与目标检查的分工、Controller 的错误输出 |
| 9 | [user_profile_service.go](../../app/domains/user/service/user_profile_service.go) | 共享仓储查找、发号、创建流程及模型转换 |
| 10 | [user_profile_repository.go](../../app/domains/user/repository/user_profile_repository.go)、[user_profile.go](../../app/domains/user/tables/user_profile.go) | SQL 的执行入口、表映射及回填 |
| 11 | [JWT Provider](../../internal/authn/jwt/provider.go)、[共享实例](../../internal/authn/jwt/runtime.go) | Controller 如何取得签发器，新建身份如何形成凭证和有效期 |
| 12 | [Controller 错误映射](../../app/domains/user/controller/v1/user_profile_controller.go)、[response.go](../../internal/response/response.go) | `mapUserProfileErrorCodes` 如何分类，最终 JSON 如何写出 |
| 13 | [response_timeout.go](../../app/http/middleware/response_timeout.go) | 标准库超时包装、统一响应适配、取消隔离与内部处理收尾 |

沿这个顺序阅读，可以把一个请求的入口、数据形态、业务执行、数据库访问和最终响应对应到具体方法，而无需先遍历整个项目。
