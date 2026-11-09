# Go 后端工程规范与框架示例

提供部署环境读取、公共 Logger、JWT 签发与认证组件初始化、雪花发号器初始化、MySQL 命名连接初始化、HTTP 服务启动和退出清理的基础链路。数据库要求见 [MySQL 基线](specifications/databases/mysql/baseline.md)、[库表设计规范](specifications/databases/mysql/schema-design.md)和 [变更与数据维护规范](specifications/databases/mysql/change-and-maintenance.md)。所有运行时日志使用英文。已实现 TraceId 的入站生成、Context 保存、响应头回传和 company 请求透传；公共 Logger 已支持 JSON 写入、操作作用域和启动初始化，生产 HTTP 请求与响应日志已接入，其他业务日志尚未统一迁移，见[请求日志规范](specifications/application/application-log.md)。性能采集方案见[应用性能数据采集方案](specifications/application/application-performance.md)。

公司内部 HTTP Client 按服务、domain、controller 组织，已支持多环境配置读取、共享 Transport、启动初始化和统一获取，详见[外部接口调用与 Client 规范](specifications/application/api-outbound.md)。两个 Image Client 已提供图片生成和图片编辑任务查询的 HTTP 调用示例，真实服务契约仍待确认。

第三方 Client 按功能、子功能及供应商文件组织，装配也由 thirdparty、AI、LLM 逐层完成。当前 Qwen、MiniMax 已支持独立配置读取、SDK 构造和进程启动初始化，通过 `thirdparty.Current().AI.LLM.Qwen`、`thirdparty.Current().AI.LLM.MiniMax` 获取共享实例；其他供应商仍为包占位。每次文本生成须指定模型，不在配置中保存默认模型。

生产 HTTP 入口在响应等待中间件之前处理 `TraceId`：请求头不存在或为空时生成 16 字节随机数编码的 32 位十六进制值；已有非空值时原样沿用。该值保存到 `Request.Context()` 并通过同名响应头返回。Controller、Service 和 company Client 传递标准 Context，公共 `GetJSON`、`PostJSON` 自动设置出站 `TraceId`；无值的独立调用不由 Client 补生成。请求与响应日志通过共享 Logger 输出 `started`、`finished`，自动填充 `component=http_entry` 和方法、注册路由模板组成的 `operation`；外层记录实际选定的响应状态及等待耗时，超时后的内部工作仍沿用 TraceId。直接使用 `routers.New(authenticator)` 时，HTTP 模板绑定在没有外层日志状态时直接透传；如需 TraceId 与请求、响应日志，应在 Logger 初始化后依次包装 `middleware.TraceID(middleware.HTTPLogging(engine))`，响应等待及收尾另行装配。详细约定见[TraceId 的生成、保存与透传](specifications/application/api-handle.md#24-traceid-的生成保存与透传)及[HTTP 请求与响应日志](specifications/application/application-log.md#http-请求与响应日志)。

## 启动

使用 Go 1.26 或更高版本和 MySQL 8.4。启动前需要通过受控 SQL 创建业务库和表；程序不自动建库或迁移表结构。核对 `config/logger/dev.yml` 的服务名称和最低日志级别、`config/database/mysql/dev.yml` 的本地连接配置、`config/snowflake/dev.yml` 中为本进程分配的机器号、`config/authn/jwt/dev.yml` 的 JWT 配置，以及 `config/integrations/thirdparty/dev/ai/llm/` 下的 `qwen.yml`、`minimax.yml` 后，在项目根目录执行：

```sh
go run ./cmd/web
```

JWT 签名密钥直接填写在 `config/authn/jwt/<environment>.yml` 的 `signing_key` 字段中，使用标准 Base64 编码，解码后至少 32 字节。准备配置时可通过 `openssl rand -base64 32` 生成随机密钥，将输出填入该字段；后续启动复用配置中的值，不在每次启动时重新生成。JWT 配置不读取或展开密钥环境变量，密钥缺失、为空或无效时停止启动。

同一部署的多个实例应使用相同的 `signing_key`，不同环境使用独立密钥。修改配置后重启生效，不在线重载；更换密钥会使旧凭证无法通过新实例验证。`Config` 包含密钥，不记录整个配置对象，错误及日志不回显密钥。JWT 固定使用 HS256，当前环境配置中的 `token_ttl` 为 `24h`；组件初始化本身不启用 HTTP 认证，业务入口通过显式调用使用签发能力，详细约定见[公共身份认证与 JWT 实现](specifications/application/api-authentication.md)。

`APP_ENV` 支持 `dev/test/prod`，未设置或为空时默认 `dev`；`APP_REGION` 目前支持 `cn`，未设置或为空时默认 `cn`。环境名和区域值严格匹配，非法值停止启动。

Logger 在部署环境初始化后读取唯一的 `config/logger/<environment>.yml`，仅包含 `service_name`、`min_level`；环境复用 `env.Current()`，实例标识在本次初始化中生成 16 字节随机数并编码为 32 位十六进制字符串。开发环境最低级别为 `DEBUG`，测试和生产为 `INFO`。配置仅接受单份 YAML 文档和已知字段，不展开环境变量，也不回退其他环境；缺失或无效时停止启动。共享实例通过 `logger.Current()` 获取，重复成功初始化报错。共享 Logger 固定向 stdout 同步写单行 JSON，由运行环境保存日志；当前尚未批量替换既有业务日志。

按环境选择唯一的 `config/database/mysql/<environment>.yml`，文件缺失不回退其他环境。默认监听 `:8080`，初始化总时限为 30 秒，停机收尾总时限为 10 秒。可指定配置根目录、地址和时限：

```sh
go run ./cmd/web -config-dir /srv/app/config -addr 127.0.0.1:8081 -startup-timeout 30s -shutdown-timeout 10s
```

`-config-dir` 是包含 `logger`、`integrations/company`、`integrations/thirdparty`、`authn/jwt`、`snowflake`、`database/mysql` 的配置根目录，默认 `config`。其他工作目录启动时应传入正确路径；TLS CA 相对路径以所选 YAML 所在目录为基准。

公司 Client 配置读取 `config/integrations/company/<environment>.yml`。开发环境使用本地地址；测试和生产分别设置 `TEST_PYTHON_SERVICE_TEMPLATE_BASE_URL`、`PROD_PYTHON_SERVICE_TEMPLATE_BASE_URL`，或直接填写配置中的 `base_url`。地址缺失、超时无效或 Client 未注册时停止启动，初始化不调用远端 HTTP 接口。

LLM 分组读取 `config/integrations/thirdparty/<environment>/ai/llm/` 下的 `qwen.yml`、`minimax.yml`，环境仅支持 `dev/test/prod`，已注册 Client 的配置缺失或非法时停止启动。两份文件分别直接保存 `base_url`、`api_key`、`connect_timeout`、`request_timeout` 四个字段；API Key 直接填写在各环境 YAML 中，不展开环境变量，也不写入日志。MiniMax 服务地址为 `https://api.minimaxi.com/v1`，当前连接及请求超时分别为 3 秒、60 秒。启动只构造 SDK Client，不调用模型。每次调用均须提供 Model：Qwen 仅支持 `llm.QwenModel38Max`、`llm.QwenModel37Plus`、`llm.QwenModel38Flash`；MiniMax 要求非空白模型名，当前不另设模型白名单。

测试或生产部署先注入 MySQL YAML 中引用的环境变量及 CA 证书，再设置 `APP_ENV=test` 或 `APP_ENV=prod`。MySQL 配置中的 `${NAME}` 在 YAML 解码后展开，缺失或为空时停止启动；错误信息不会打印密码或 DSN。

测试环境的 RDS 地址、账号和 CA 路径已经配置在 `test.yml`；运行时设置 `TEST_MYSQL_PASSWORD` 注入密码，并通过 `APP_ENV=test` 选择此配置。四个命名连接继承同一测试账号和密码，CA 使用仓库中的 `ApsaraDB-CA-Chain/ApsaraDB-CA-Chain.pem`，路径相对于该 YAML 文件解析；部署配置文件时须一并包含这个证书目录。

测试 RDS 须提前在控制台为配置中的连接地址开启 SSL；客户端开启 TLS 和提供 CA 文件不会自动开启服务端 SSL。配置同时验证证书链和连接域名，服务端不支持 SSL 时连接会失败。控制台操作说明及实例重启影响见[阿里云 RDS SSL 文档](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/configure-a-cloud-certificate-to-enable-ssl-encryption)。

启动顺序为：部署环境初始化 → logger 包读取日志配置、生成实例标识并发布共享 Logger → company 包读取并校验环境配置、创建共享 Transport 和两个 Image Client → thirdparty 选择环境目录，经 AI、LLM 分组加载并构造 Qwen、MiniMax，全部成功后发布共享实例 → 加载 JWT 配置、验证密钥并发布签发与认证组件 → 取得数据中心编号 → 加载所选环境的雪花配置并初始化发号器 → MySQL 配置解码、占位符展开与校验 → 创建并校验数据库连接池 → 创建 Repository → 创建并发布 Service → 创建领域 Router 和 Controller、注册版本化路由 → 装配默认及接口覆盖的响应等待中间件 → 打印环境、区域 → 监听 HTTP。初始化失败清理本次已创建的 company、thirdparty HTTP 空闲连接和数据库连接池，并由进程入口最后同步已初始化的 Logger，以非零状态退出。

SIGINT/SIGTERM 触发停机，在同一个独立收尾预算内，先停止接收并等待 HTTP 请求，再等待已超时但仍在运行的内部处理。HTTP 收尾失败也会继续执行内部收尾；只有 HTTP 和内部处理均确认结束后，才清理 company、thirdparty 的 HTTP 空闲连接并正常关闭数据库，最后同步 Logger。启动后的路由或监听失败、尚未处理业务请求时，也按此顺序清理。预算耗尽、未能确认全部结束时，记录收尾失败，跳过主动关闭共享资源及 Logger 同步，并以失败状态退出，避免同步等待与仍在写日志的内部工作相互阻塞；由进程退出释放连接，这不代表业务完成或事务已回滚。

当前 `bootstrap.Init` 只初始化应用资源，启动不写入用户数据。

## 雪花 ID

`internal/snowflake` 参考 tennis 的生成方式，使用固定纪元 `2017-01-01T00:00:00Z`（1483228800000 毫秒）。ID 为正 int64：最高符号位为 0，其余依次为 41 位毫秒时间差、3 位数据中心编号、7 位机器编号、12 位同毫秒序列。数据中心编号由 `env.Current().DataCenterID()` 提供，范围 0～7，当前 `cn` 对应 0；与机器编号组合后形成 10 位节点编号。

机器编号只从 `config/snowflake/<environment>.yml` 读取，不使用默认值、随机值或环境变量覆盖。`LoadConfig` 接收直接包含这些文件的 `snowflake` 叶子目录，环境仅支持 `dev/test/prod`，只读取所选文件，缺失或无效时停止启动，不回退其他环境。配置必须是单份非空 YAML 文档，只能显式填写 `machine_id` 整数字段，范围 0～127，0 有效；遗漏、null、浮点数、未知字段、重复字段及多文档均拒绝。

```yaml
machine_id: 1
```

仓库三个环境文件中的 `1` 是部署模板，不能直接复制给多个进程。同一 ID 分配范围、同一数据中心内，所有运行进程（包括跨服务、后台任务及共享此范围的不同环境）必须分配不同的机器编号。部署方负责该分配，组件不提供分布式租约。

`bootstrap.Init` 在数据库初始化前调用 `snowflake.Init(dataCenterID, machineID)`，成功发布进程共享的发号器；重复初始化会返回错误，运行中不重新加载配置。调用方通过 `snowflake.NextID() (int64, error)` 取得 ID，并处理错误。互斥锁保护同一进程内的并发调用，同一毫秒序列范围为 0～4095，耗尽后每次等待 1 毫秒、最多重试 10 次，时钟仍未推进则返回序列耗尽错误；时钟回拨、时间早于纪元或超出时间位容量时也返回错误。

节点复用与跨重启的历史时钟高水位由部署方保障：复用同一数据中心及机器编号前，须确认旧进程已停止，并确保当前时间严格超过此前成功发号的最高毫秒。当前组件不持久化该高水位，不会自动保证跨重启避碰。

## 请求与响应

用户资料接口统一位于 `/api/v1/user/userProfile` 下：

| HTTP 方法 | 业务用例 | 用途 |
| --- | --- | --- |
| GET | `queryUserProfileByUserId` | 按内部用户 ID 查询资料 |
| POST | `createUser` | 匿名首次注册，生成内部用户 ID，成功后返回用户资料及该用户的 JWT |
| POST | `updateUserProfile` | 检查版本并完整替换昵称、有效头像地址，不支持清空头像 |
| POST | `batchQueryUserProfiles` | 按 ID 批量查询，返回已存在资料和缺失 ID |
| GET | `queryUserProfilePage` | 按昵称、启用状态筛选并分页查询 |

所有接口已启用响应等待超时，配置集中在 [routers/http_handler.go](routers/http_handler.go)：

| 适用请求 | 等待时间 |
| --- | --- |
| 未单独覆盖的请求 | 3 秒 |
| `GET /api/v1/user/userProfile/queryUserProfilePage` | 5 秒 |
| `POST /api/v1/user/userProfile/createUser` | 10 秒 |

覆盖配置按 HTTP 方法与 `URL.Path` 精确匹配，Query 参数不参与选择；每个请求只经过一个等待包装器。修改默认时间或增减覆盖项后重新启动服务即可生效，无需修改 Controller、Validator 或 Service。

超过等待时间时返回 HTTP 503、`SERVICE_UNAVAILABLE`，消息为 `response wait timeout; operation outcome is unknown`。已经开始的内部业务继续执行，最终响应被丢弃；客户端应稍后确认结果，不能把超时当作业务失败或回滚证明。响应等待时间与 `-startup-timeout`、`-shutdown-timeout` 分别控制不同阶段。

请求与响应字段见[用户资料 DTO](app/domains/user/dto/v1/user_profile/)，处理链路与错误约定见[接口处理流程](specifications/application/api-handle.md)和[接口错误规范](specifications/application/api-error.md)。`createUser` 不要求已有发起方用户 ID 或凭证；Controller 在创建成功后为新建 ID 签发 `user/member` JWT，HTTP 200 的 `data` 包含嵌套的 `user_profile`、`access_token`、`token_type` 和 `expires_at`。令牌类型为 `Bearer`，到期时间直接使用签发结果并输出 UTC RFC3339 字符串，成功响应设置 `Cache-Control: no-store`。

创建失败或账户重复时不签发凭证；创建完成后签发失败返回 500，并保留已创建的用户。10 秒等待超时后内部继续，晚到凭证不再发送；不能通过重复匿名注册补发令牌，本次不提供凭证恢复。来源与外部账户字段仍是请求声明，不证明外部身份，具体契约及各接口权限见 [user 领域接口权限规范](specifications/domains/user/api-authorization.md)。

`/api` 分组统一挂载 `AuthenticateIfPresent`：完全没有 Authorization 头时匿名放行，显式空头或格式错误返回 400，无效、过期或方案不支持的凭证返回 401，认证故障返回 500；公开创建接口携带错误凭证时也会被拒绝。后续依次执行 Validator 和 Controller，Controller 决定是否要求身份以及允许的访问范围。匿名且参数非法时可以先返回 Validator 的 400，参数通过后才在受保护 Controller 返回 401。查询、更新的 Service 发起方参数来自 Principal；更新 DTO 已移除 `operator_user_id`，客户端不能自报操作人。

Controller 通过 [app/http/authz](app/http/authz/) 复用身份检查：`RequirePrincipal` 要求指定类别与命名空间，`RequireMemberUserID` 按项目约定取得成员的正 int64 用户 ID。两者只读取身份并返回值或错误，不写响应、不查数据库；Controller 继续执行目标访问规则，选择错误码及挑战头，并决定是否调用 Service。具体契约见[Controller 共用的身份检查](specifications/application/api-authentication.md#8-controller-共用的身份检查)。

`GET /api/v1/user/userProfile/queryUserProfileByUserId?user_id=123` 按内部用户 ID 查询基本资料。Query 参数 `user_id` 必填，须为大于 0 的 64 位有符号整数；将下面的 ID 和 `<access_token>` 替换为创建成功后返回的同一用户 ID 与凭证：

```sh
curl -i "http://localhost:8080/api/v1/user/userProfile/queryUserProfileByUserId?user_id=1234567890123456789" \
  -H "Authorization: Bearer <access_token>"
```

查询成功返回 HTTP 200：

```json
{
  "data": {
    "user_id": "1234567890123456789",
    "user_name": "Example User",
    "avatar_url": null,
    "is_enabled": true,
    "version": 0
  },
  "error": null
}
```

顶层 `data`、`error` 始终存在：成功时 `data` 是该用例的 Response DTO，`error` 为 `null`；失败时 `data` 为 `null`，`error` 是统一错误对象。调用方从 `data` 读取业务结果，从必填的 `error.code` 判断通用错误类别，按需使用可选的 `error.business_code` 识别具体业务原因；`error.message` 直接使用收到的错误说明，项目自行声明的错误使用英文，第三方故障保留原始文案；`error.details` 为可选字段明细。HTTP 状态码由通用码的固定映射决定，Controller 不独立设置状态码。

`data.user_id` 使用十进制字符串，避免雪花 ID 精度丢失。`data.avatar_url` 始终保留：未提供头像时为 `null`，数据库中明确保存空字符串时为 `""`。`data.is_enabled` 表示当前启用状态，`data.version` 用于下一次更新时提交 `expected_version`。

| 情况 | HTTP 状态 | JSON 响应 |
| --- | --- | --- |
| ID 缺失、格式错误、非正数或超出 int64 范围 | 400 | `{"data":null,"error":{"code":"INVALID_ARGUMENT","message":"user_id: must be a positive 64-bit integer","details":[{"field":"user_id","reason":"must be a positive 64-bit integer"}]}}` |
| 用户资料不存在 | 404 | `{"data":null,"error":{"code":"NOT_FOUND","business_code":"USER_PROFILE_NOT_FOUND","message":"user profile not found"}}` |
| 路由不存在 | 404 | `{"data":null,"error":{"code":"NOT_FOUND","message":"route not found"}}` |
| 用户资料查询执行失败，例如下层返回 `driver: bad connection` | 500 | `{"data":null,"error":{"code":"INTERNAL_ERROR","message":"driver: bad connection"}}` |
| 未处理异常 | 500 | `{"data":null,"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}` |

五个接口均使用 Router → 全局可选认证 → 对应用例 Validator → Controller 链路。领域 Validator 为每次请求创建 Request DTO，调用 `internal/validator.Bind` 完成 HTTP 参数映射，再通过同文件内的私有函数显式校验，可复用 `check` 和 models 的规则。已知字段问题汇总为 `details`，消息使用第一条字段错误的 `Error()`；规则错误和未知失败单独返回 500。校验全部通过后才将 DTO 传给 Controller，由 Controller 执行接口访问检查。通用 `Bind` 只做映射：GET 使用 Gin 的 `binding.MapFormWithTag` 读取 Query，POST 使用标准库 JSON 解码器读取请求体；不执行自动校验，DTO 禁止使用 `binding` 等校验标签。解析或类型转换失败时使用绑定错误文案并终止，不继续检查部分 DTO。完整链路见[接口处理流程](specifications/application/api-handle.md)。

业务调用链为 Controller → Service → Repository。Controller 的业务入参只有 Request DTO，由其内部构造 Service Params；Validator 已保证成功的纯类型转换可以忽略错误返回值。Service 从仓储取得表结构，再映射为领域实体；单条资料用例直接返回 `models.UserProfile`，批量和分页用例分别返回 `BatchQueryUserProfilesResult`、`QueryUserProfilePageResult`。Controller 根据模型或 Result 构造用例专属响应 DTO，通过 `response.Success` 放入 `data`。查询未命中时，Service 将仓储的 `gorm.ErrRecordNotFound` 转为用户资料不存在的业务错误；Controller 在自己的文件中选择通用码和可选业务码，直接使用收到的 `err.Error()` 输出。数据库连接、SQL 执行、发号等技术故障由 Service 记录上下文后原样返回，不包装或替换为统一失败错误。

所有 Service 公开业务方法统一以 `ctx context.Context` 为首参，业务输入继续通过 Params 显式传递。Controller 使用当前内部 `ctx.Request.Context()`，Service 入口建立自己的日志操作作用域，并向其他 Service、Client 及需要流程上下文的辅助函数继续传递；纯校验、映射和构造无需机械添加 Context。非 HTTP 入口负责提供本次执行的非 nil Context，业务层不替换为 `context.Background()`、不把请求状态存进共享实例。当前六个用户资料用例及其 Controller 调用方均已统一；这不自动迁移现有 `log.Printf`，也不改变 Repository、SQL 取消或事务行为，详见[Service Context 约定](specifications/application/api-service-validator.md#service-context-约定)。

Service 自己声明的错误集中在对应 Service 文件顶部，用包级 `var` 和 `errors.New` 表达本层独立判断的失败或明确的业务语义映射；Service 和 models 不声明新的 error 类型。每个用例在同一区域依次组织 Params、可选 Result、输入常量、输入校验和处理方法，Result 紧接 Params，跨用例私有方法位于尾部 `shared`。用例专属 Result 留在 Service，领域模型直接复用，具体见[Service 输出契约](specifications/application/api-service-validator.md#74-service-的输出契约)。Controller 文件维护自己的业务码和映射；models 直接复用公共检查错误，只为自身独立判断的领域失败声明错误。内部错误与对外业务码不要求一一对应；同一个业务码的通用错误类别保持固定。公共响应组件维护八个通用类别及 HTTP 状态映射，合法错误的 `Message`、`Details` 原样输出，具体约定见[接口错误规范](specifications/application/api-error.md)及[接口返回规范](specifications/application/api-response.md)。

顶层 `routers.New(authenticator)` 创建 Gin Engine、HTTP 日志模板绑定、异常恢复中间件和 `/api` 分组，在 `/api` 统一挂载可选认证，再建立 `/v1` 并调用领域 Router 注册接口。`cmd/web` 使用 `routers.NewHTTPHandler()`，该方法将共享 JWT 认证器传给 `New`，在完整 Gin Engine 外装配响应等待中间件，再按 TraceId、HTTP 日志的顺序包装请求分发入口，并保存收尾入口；独立调用 `New` 需显式传入认证器。HTTP 日志使用共享 Logger，`gin.Logger()` 和等待层旧对外摘要已移除；`finished` 表示对外响应结束，内部业务可能仍继续。未匹配的路由保持 `NoRoute` 的 404，不补做认证。接口路径统一为 `/api/v1/{domain}/{controller}/{业务用例}`：domain 严格沿用 `app/domains/<domain>` 的目录名，controller 由具体 Controller 类型名去除 `Controller` 后缀后转换为 lowerCamelCase。`app/domains/user/routers/router.go` 创建并持有 `UserProfileController`，在 `/user/userProfile` 分组下注册上述用例。完整约定见[接口路由与命名规范](specifications/application/api-routing.md)及[接口返回规范](specifications/application/api-response.md)。

Controller 保留在对应领域的 `controller/v1` 中；Validator 与 DTO 先按版本、再按 Controller 分组，用户资料分别位于 `validator/v1/user_profile` 和 `dto/v1/user_profile`。每个用例独立组织文件，两个分组包均命名为 `userprofile`，导入时分别使用 `userprofilevalidator`、`userprofiledto` 别名。同一 Controller 内复用的 `UserProfile` 放在该 DTO 分组的 `user_profile.go` 中；只有跨 Controller 复用且语义一致的结构，才按需建立 `dto/v1/shared` 包。统一响应外层由 `internal/response` 提供。

新增 V2 时单独注册 `/api/v2`，在相关领域增加相应 Controller、Validator、DTO 和注册方法，Validator 与 DTO 延续版本下按 Controller 分组的组织方式；Service、领域模型、Repository 和表结构继续按业务组织，不随接口版本复制。

## 数据库连接

现有普通连接为 `user`、`post`、`order`，分别默认访问 `user_service`、`post_service`、`order_service`。另有通用的 `transaction` 连接，不选择默认库，供同一 MySQL 实例中授权范围相同的跨库流程使用。即使地址和账号相同，各命名连接也拥有独立连接池；仓储必须明确选择连接。

`bootstrap.Init(ctx, configDir)` 负责加载环境与配置，再调用 `internal/runtime.Init` 创建资源，全部成功后发布默认实例。`Runtime.DB` 保存 `User`、`Post`、`Order` 三个业务库入口，`Runtime.Repositories` 保存已初始化的用户领域仓储。使用 `appruntime` 别名导入 `internal/runtime`，通过 `appruntime.Current()` 读取默认实例：

```go
profile, err := appruntime.Current().Repositories.UserProfile.GetProfileByID(userID)
```

Service 不保存 Repository 字段，方法内获取默认仓储，执行查询并把表结构转为领域模型。Runtime 只管理 DB 和 Repository；`bootstrap.Init` 在 Runtime 初始化后调用 `internal/composition.Init`，统一创建并发布默认 Service。Controller 不保存 Service 字段，处理请求时通过 `composition.Current().UserProfile` 调用默认服务；领域 Router 创建并复用无状态 Controller。启动入口调用 `routers.NewHTTPHandler()`，由其传入共享认证器并完成路由及等待组件的装配，不再逐个装配 Service 和 Controller。停止业务访问后，仍由启动入口调用 Runtime 的 `Close()`。

`internal/composition` 只依赖 Service，Service 依赖 `internal/runtime`，Controller 依赖 composition；Runtime 不导入 Service，composition 不导入 Controller，保持包依赖单向。默认服务通过普通指针保存，只在启动阶段由单个协程初始化一次；初始化完成后才启动业务协程，运行期间只读、不替换实例或字段。未初始化时调用 `composition.Current()` 会用英文 panic 提示顺序错误。

未知、禁用或已关闭的连接返回错误，不回退其他连接。普通业务连接不会自动组成跨库事务，需要跨库的流程遵守专用连接和同一事务的规范。

事务由每次业务操作显式 `Begin` 创建，将同一个 `tx` 传给所有参与的数据库操作，全部成功后 `Commit`，失败则 `Rollback`。连接池按进程复用，事务不保存在 `Runtime` 中。`NewUserProfileRepository(tx)` 由仓储内部定位用户表；直接使用 GORM 操作其他表时，需明确指定完整库表名，例如 `order_service.membership_order`。事务内使用局部创建的 Repository；通过 `Current().Repositories` 取得的默认仓储仍使用普通连接，不会自动加入当前事务。需要跨方法共享事务时，显式传递同一个 `tx` 或对应的局部 Repository。

`DataBase: ""` 表示不选择默认库，业务 SQL 必须指定完整库表名；启动检查会正确处理 `DATABASE()` 返回的 SQL `NULL`。普通连接保留各自默认库，供直接使用未限定表名的操作；仓储内部已指定完整库表名的操作不依赖默认库。跨库事务涉及的库须位于同一个 MySQL 主实例，目标表均使用 InnoDB。

独立连接池不要求独立账号。本地和测试环境的 `transaction` 都通过 `*writer` 继承已有账号密码，无需额外创建账号；测试环境只需注入一个 `TEST_MYSQL_PASSWORD`。生产保留通用事务凭据 `PROD_MYSQL_TRANSACTION_USERNAME/PASSWORD`，由部署方按获准流程配置库表权限；若现有账号的授权符合相同范围，也可以复用该账号。配置不会自动创建账号或扩大权限。

新增同一实例内、权限范围相同的跨库流程时，复用 `transaction` 并在业务入口管理事务，无需按库表组合新增连接。目标实例或权限范围不同时，另配命名连接；多个连接分别开启的事务不会自动合并。

通用事务池的初始容量与各环境普通业务池一致：dev/test 为最大连接数 10、最大空闲连接数 2，prod 为 20 和 5。四个池合计的上限分别为每进程 40、40、80 条连接；部署时仍须结合副本数、事务耗时、连接等待情况和数据库额度调整。

初始化会应用连接池与网络超时、每个新物理连接的字符集/比较规则/UTC 时区/隔离级别/SQL 模式，以及 GORM 行为和 UTC 毫秒时间函数。开发环境只允许本地回环连接关闭 TLS；测试和生产必须验证 CA 及服务器身份，禁止跳过验证或回退明文。

当前只支持 `Write` 主库。`IsOpenReadDb`、`SuppressRecordNotFound` 和 `AutoMigrate` 开启时明确报错；其余已有 GORM 开关按配置应用。未知字段、无法解码为目标字段的值及不符合基线的参数会在建立连接前被拒绝。

配置读取参考 tennis 数据库组件，直接使用 YAML 解码器和强类型结构体，不维护配置工厂、缓存或热更新。加载流程集中在 `config.go`：读取所选文件 → 解码并检查未知字段 → 显式展开字符串占位符 → 校验必要约束。字段含义定义在 `types.go`，纯校验逻辑放在 `validation.go`，便于按职责阅读。

新增配置字段时，先在 `types.go` 声明字段及对应的 YAML 标签，再按需要补充 `validation.go` 校验，并在 `client.go` 应用到驱动或 GORM。字符串字段需要支持 `${NAME}` 时，须加入 `config.go` 的显式展开列表；TLS 关闭或连接禁用时，不读取其未使用的凭据占位符。解析、校验、连接初始化和退出流程中的注释说明了各阶段的约束及资源归属。

## 用户资料仓储

仓储按表组织：`tables/user_profile.go` 声明表结构，`repository/user_profile_repository.go` 定义 `UserProfileRepository`、构造函数及该表的数据操作。后续其他表也使用各自的 `XxxRepository` 和 `xxx_repository.go`，按实际需要增加方法。

`repository.NewUserProfileRepository(db)` 接收已初始化的连接或事务，提供以下数据操作：

| 方法 | 用途 |
| --- | --- |
| `GetProfileByID(id)` | 按内部 ID 查询 |
| `GetProfileByIDForUpdate(id)` | 在调用方事务中锁定目标记录，供版本校验和更新使用 |
| `GetProfileByAccount(source, externalID)` | 按来源及外部标识查询，作为内部能力 |
| `GetProfilesByIDs(ids)` | 按一组内部 ID 查询，由 Service 整理输入顺序和缺失项 |
| `QueryProfilePage(...)` | 应用筛选、ID 降序及分页，返回匹配总数和本页记录 |
| `CreateProfile(profile)` | 新增资料记录 |
| `UpdateProfile(id, expectedVersion, changes)` | 检查启用状态与旧版本，替换昵称、头像并递增版本 |

完整库表名 `user_service.user_profile` 由仓储文件内的私有常量统一维护，普通访问与跨库事务使用相同的方法，不要求调用方指定表名。构造函数只派生干净的 GORM 会话，不建立新连接或开启事务；每次方法调用自行建立表和查询条件，保留注入连接的事务、context 和时间配置。

方法直接接收或返回 `tables.UserProfile`，不做 DDD 模型转换，不接收 `context.Context`，不依赖 Web 请求。连接可以来自普通服务、脚本或后台任务，账号须具备目标表权限；连接池由持有方统一管理。

账户查询同时匹配来源和外部标识，保留原字符串，不默认过滤停用用户。参数合法性统一由外层领域模型或调用方保证，仓储不重复校验 ID、初始版本、UTF-8、长度或字段内容。注册业务明确设置初始版本和 `IsEnabled=true`，持久化方法也支持显式保存停用状态。新增成功后回填自动时间，失败不修改传入对象。

`UpdateProfile(id, expectedVersion, tables.UserProfile{...})` 只取传入结构的 `UserName` 和 `AvatarURL`，完整替换昵称和头像，并递增版本。更新只作用于启用且旧版本匹配的记录；传入结构里的 ID、账户身份、启用状态、Version 和时间不参与修改。`AvatarURL` 使用 `sql.NullString`：`Valid=false` 写 SQL `NULL`，`Valid=true` 则保存 `String`，包括空字符串。它不是 PATCH，未提交字段由调用方提前处理。

仓储不维护自定义错误层，直接返回 GORM 或驱动提供的原始错误。查询未命中可检查 `gorm.ErrRecordNotFound`；重复键由 [mysql.IsDuplicateKey](internal/database/mysql/error.go) 统一识别，兼容驱动原始错误和 GORM 通用错误，Service 不直接比较错误编号。该判断不解释账户等业务含义，也不修改原错误；仓储不额外分类、包装或记录业务日志。

`UpdateProfile` 返回 `(rowsAffected int64, err error)`。调用方先处理 `err`，再判断影响行数；`0, nil` 表示 SQL 执行正常但条件未匹配，可能是用户不存在、停用或版本过期，由外层决定业务含义。版本和启用条件仍在 SQL 中原子判断，不能仅依赖写入前的内存检查。

用户资料更新 Service 在同一事务中锁读目标记录、检查启用状态及版本、执行受保护更新，再读取本次更新结果。事务提交成功后才返回资料；失败则回滚。分页仓储分别读取匹配总数及本页记录，不承诺并发写入时的快照一致性。

同库或跨库事务中都可直接将 `tx` 传给 `NewUserProfileRepository`，由事务发起方统一提交或回滚；仓储不会切换回普通连接。事务中的创建回填仅表示该语句成功，调用方须在最终提交成功后再对外使用结果；回滚不会自动恢复内存对象。跨库流程仍遵守专用连接和同一事务的规范。

用户领域其余三张表也分别拥有单表仓储，均使用完整库表名、直接返回 `tables` 结构，并保留 `NewXxxRepository(tx)` 的局部事务用法：

| Runtime 中的仓储 | 基础操作 |
| --- | --- |
| `Repositories.UserFollow` | 按 ID 或双方用户 ID 查询、创建关注、按关系 ID 和双方用户 ID 物理删除 |
| `Repositories.UserMembership` | 按 ID 或用户 ID 查询、创建会员权益、按 ID 和用户 ID 更新权益字段 |
| `Repositories.MembershipChangeRecord` | 按 ID 或订单 ID 查询、追加会员变更历史 |

会员权益到期后保留记录；会员变更历史仅提供查询和追加。各仓储的参数合法性由外层保证，未命中、重复键等错误原样返回，更新和删除同时返回影响行数。

## 职责与扩展

| 位置 | 职责 |
| --- | --- |
| `cmd/web` | 参数、信号、调用顺序、HTTP 监听与关闭、退出结果 |
| `bootstrap` | 管理依赖初始化顺序，调用 logger 初始化入口及 company、thirdparty Client 装配入口，处理启动失败时的资源清理 |
| `internal/logger` | 加载所选环境日志配置、生成实例标识、发布共享 Logger，提供操作作用域及同步 JSON 写入 |
| `app/integrations/company` | 加载公司服务配置、创建共享 Transport 和具体 Client、发布默认实例并提供获取入口 |
| `app/integrations/thirdparty` | 选择环境目录，经 AI、LLM 分组逐层装配已注册 Client；根层发布共享实例，清理按功能分组逐层执行 |
| `internal/runtime` | 创建并持有 DB、Repository，提供默认实例访问和连接池关闭 |
| `internal/composition` | 创建并持有默认 Service，提供 Current 访问入口 |
| `internal/response` | 定义统一响应外层、通用错误枚举及固定 HTTP 状态映射、Gin 输出函数 |
| `app/domains/*/service/*_service.go` | 顶部声明自身错误，按用例组织 Params、可选 Result、输入常量、校验和处理，尾部保存共享实现 |
| `routers/web.go` | 接收认证器，创建 Gin Engine，在 `/api` 挂载可选认证，再注册版本及领域路由 |
| `routers/http_handler.go` | 集中配置默认及接口覆盖的响应等待时间，选择一个包装后的 Gin Handler，汇总内部处理收尾 |
| `internal/traceid` | 生成 TraceId，通过标准 Context 保存和读取，集中声明 HTTP 头名称 |
| `app/http/middleware/trace_id.go` | 在响应等待前确定 TraceId，保存到请求 Context，并写入同名响应头 |
| `app/http/middleware/response_timeout.go` | 适配标准库响应等待超时，隔离业务取消、丢弃晚到响应并跟踪内部工作 |
| `app/http/middleware/authentication.go`、`principal.go` | 可选认证；按请求保存、读取可信身份，认证失败时统一输出 |
| `app/http/authz/principal.go`、`member.go` | 为 Controller 复用身份要求检查和项目主体标识转换，只返回值或错误 |
| `app/domains/*/routers` | 创建领域 Controller，在指定版本组中依次注册 domain、Controller 和业务用例 |
| `app/domains/*/controller/v1` | 检查接口身份及访问范围，构造可信业务参数，维护业务码和错误映射 |
| `app/domains/*/validator/v1/<controller>` | 按 Controller 分组的 V1 请求绑定与校验，每个用例一个 Validator 文件 |
| `app/domains/*/dto/v1/<controller>` | 按 Controller 分组的 V1 请求、响应定义；同组共享结构按具体业务概念组织文件 |
| `env` | 部署环境和区域的加载、校验及只读访问 |
| `internal/snowflake` | 严格加载机器号配置，初始化并提供进程共享的雪花发号器 |
| `config/snowflake` | 各部署环境的机器号配置模板，部署时按进程分配 |
| `internal/database/mysql/config.go` | 选择并解码 YAML、展开环境变量、组织校验 |
| `internal/database/mysql/types.go` | 配置结构、YAML 字段映射与逐项含义 |
| `internal/database/mysql/validation.go` | 必填值、连接池、时间、TLS 及行为约束校验 |
| `internal/database/mysql/client.go` | 创建、验证、获取和关闭命名连接池 |
| `config/database/mysql` | 各部署环境的 MySQL 连接配置 |
| `app/domains/*/models`、`tables` | 业务对象与数据库表映射 |
| `app/domains/user/repository/*_repository.go` | 每张表独立的仓储结构和数据库操作，直接使用 tables 结构 |

新增环境时同步维护 `env`、雪花配置加载器和 MySQL 加载器的环境白名单，并增加对应 YAML；新增命名连接时增加 YAML 配置并由仓储显式获取。`env/envs.go` 中记录了环境和区域扩展步骤。

## 验证

```sh
go test -race ./...
go vet ./...
```

现有测试覆盖统一响应、合法错误内容保留、无效错误兜底、路由错误、查询参数校验和异常恢复，不依赖正在运行的 MySQL。实际 MySQL 8.4 的约束、事务行为与完整启动链路仍需数据库联调验证。
