# 外部接口调用与 Client 规范

本规范覆盖 `app/integrations/` 下的两类调用：`company` 对接公司内部服务的 HTTP 接口，`thirdparty` 对接第三方产品的 HTTP 接口或 SDK。各业务 Service 通过对应 Client 使用外部能力，Client 负责协议和 SDK 适配。

## company：公司内部 HTTP 调用

公司内部服务的 HTTP Client 统一放在 `app/integrations/company/` 下，按目标服务、domain、controller 划分。

本文约定目录组织、初始化、获取、配置及 TraceId 透传方式。目前已提供公共构造方法、多环境配置读取、进程启动时的统一初始化和共享实例获取；两个 Image Client 已分别提供图片生成和图片编辑任务查询的 HTTP 调用示例，路径和字段仍待真实服务契约确认。

### 目录结构

服务和 domain 分别对应一级目录；每个 controller 在所属 domain 目录下对应一个独立的 Client 文件。

```text
app/integrations/company/
├── clients.go                    # 统一初始化、发布、获取及资源清理
├── config.go                     # 多环境配置读取与校验
├── base/
│   ├── client.go                 # 公共 Client 结构及构造方法
│   ├── config.go                 # 服务及 controller 的共享配置类型
│   ├── request.go                # 公共 GET/POST JSON 请求及 TraceId 透传
│   └── transport.go              # 服务共享 Transport 的构造方法
└── python-service-template/      # 目标服务包 pythonservicetemplate
    ├── clients.go                # 本服务的分组、注册校验和 Client 装配
    └── image/                    # 目标服务中的 domain
        ├── image_edit_client.go  # 图片编辑 controller 对应的 Client
        └── image_gen_client.go   # 图片生成 controller 对应的 Client
```

服务目录和 domain 目录沿用目标服务的名称及领域划分。Client 沿用对应 controller 的业务名称，将 `Controller` 后缀替换为 `Client`，文件名使用 snake_case，例如：

| 目标 Controller | Client 类型 | 文件名 |
| --- | --- | --- |
| `ImageEditController` | `ImageEditClient` | `image_edit_client.go` |
| `ImageGenController` | `ImageGenClient` | `image_gen_client.go` |

同一个 controller 下的多个 HTTP 接口，由同一个 Client 的不同方法提供。

### Transport 构造与共享

**每个目标服务创建并共享一个 `*http.Transport`。** 同一目标服务下不同 domain、controller 的 Client 使用同一个实例；不同目标服务分别创建自己的实例。

构造入口位于 `app/integrations/company/base/transport.go`：

```go
func NewTransport(connectTimeout time.Duration) (*http.Transport, error)
```

`connectTimeout` 对应服务级的 `connect_timeout`，必须大于零，限制 DNS 解析及 TCP 建立连接的时间。构造方法克隆 Go 默认 Transport，保留默认连接池、代理及 TLS 设置，并注入连接超时；构造过程不建立网络连接，不修改全局 `http.DefaultTransport`。

该方法每次返回独立的 Transport，共享由目标服务包的装配方法组织：每个目标服务调用一次，再把同一个返回值传入该服务所有 `base.New(config, transport)` 调用。各 `base.Client` 分别持有自己的 `http.Client`，按 Client 级 `request_timeout` 设置总请求超时。

例如，`python-service-template` 的 `ImageEditClient` 和 `ImageGenClient` 共用一个连接超时为 3 秒的 Transport，各自的请求总超时分别为 30 秒、60 秒。Transport 的共享粒度是目标服务，Client 的组织粒度仍是服务、domain、controller。

Transport 由 `company.Clients` 持有，`base.Client` 构造时只使用传入的实例。启动后续步骤失败时，`bootstrap` 释放本次创建的空闲连接；正常退出时，进程入口在 HTTP 及内部工作全部结束后调用 `CloseIdleConnections()`，再清理数据库。

### 初始化方式

Client 在进程启动阶段统一、同步初始化。`bootstrap.Init` 在部署环境初始化后，仅调用 `company.Init(configDir, environment)`，传入 `integrations/company` 配置叶子目录和当前环境，并处理初始化错误。

配置文件读取、地址占位符解析和基础参数校验放在 `company/config.go`；`company/clients.go` 按已注册服务分发装配，统一完成初始化、一次性发布、获取及 Transport 集合的最终清理。`bootstrap/init.go` 负责调用顺序和失败资源清理，保持这一入口不包含具体 Client 的构造逻辑。

每个目标服务在自己的包内维护分组和装配入口。例如 `company/python-service-template/clients.go` 属于 `pythonservicetemplate` 包，定义 `Clients`、`ImageClients`，并提供 `New(config base.ServiceConfig) (Clients, *http.Transport, error)`。该方法严格校验本服务已注册的 domain、controller，创建共享 Transport 和具体 Image Client，构造失败时清理本次资源，成功后将实例及 Transport 交给 `company`。

服务配置通过参数注入。共用的 `base.ServiceConfig`、`base.ControllerConfig` 定义在 `base/config.go`，保留配置字段的 YAML 映射；现有 `base.Config` 继续表示单个 Client 的构造配置。依赖方向保持 `company → 目标服务包 → 具体 Client`，目标服务包不反向导入 `company`；这只是调用装配分组，不增加业务 Service 层。

初始化遵循以下约定：

1. 每个 Client 在一个进程中只初始化一次，重复初始化应返回错误。
2. 全部 Client 创建成功后，才发布共享实例集合并开始处理业务请求；配置缺失或不合法时停止启动。
3. 初始化完成后，Client 实例允许并发复用，运行期间实例及其配置保持只读。
4. 配置修改后通过重启进程生效。

### 获取方式

通过 `company.Current()` 获取已初始化的共享实例集合，继续按服务、domain、controller 的层级访问具体 Client：

```go
editClient := company.Current().PythonServiceTemplate.Image.ImageEdit
genClient := company.Current().PythonServiceTemplate.Image.ImageGen
```

`Current()` 返回已发布集合的值副本，各 Client 指针继续复用；修改分组字段不会替换共享实例。未完成初始化时调用会 panic，表示程序装配错误。获取层级中的服务名表示被调用的目标系统，例如 `python-service-template`；Client 内部不增加业务 Service 层。

### 配置方式

配置保存在 `config/integrations/company/{dev,test,prod}.yml`，由 `company.Init` 按项目当前环境读取唯一文件，缺失时不回退其他环境。加载时拒绝重复字段、多份 YAML 文档、无效地址和非正超时；任何配置或装配失败都不会发布部分 Client 集合。

测试和生产的 `base_url` 分别引用 `${TEST_PYTHON_SERVICE_TEMPLATE_BASE_URL}`、`${PROD_PYTHON_SERVICE_TEMPLATE_BASE_URL}`。启动前注入相应环境变量，或在配置中填写实际地址；变量引用必须占据整个字段，缺失或为空时停止启动。

配置加载按服务名读取多个并列节点，由 `company` 将配置分发给已注册的目标服务包。当前 `pythonservicetemplate.New` 装配两个 Image Client；新增服务时在 `company` 注册，新增 domain 或 controller 时更新对应目标服务包的构造和装配逻辑，未注册的配置明确报错。

配置分为服务共享配置和 Client 独立配置：

| 配置字段 | 所属层级 | 含义 |
| --- | --- | --- |
| `base_url` | 服务 | 目标服务根地址，具体 controller 和接口路径由 Client 代码维护 |
| `connect_timeout` | 服务 | 建立连接阶段的超时上限 |
| `request_timeout` | Client | 一次 HTTP 请求的总超时上限，包含连接、发送、等待响应及读取响应正文 |

以当前两个 Client 为例：

```yaml
python-service-template:
  base_url: http://127.0.0.1:8000
  connect_timeout: 3s

  image:
    image_edit:
      request_timeout: 30s
    image_gen:
      request_timeout: 60s
```

上述超时值为示例，具体取值按目标接口确定。连接超时包含在请求总超时内，例如 `image_edit` 的总上限为 30 秒，连接阶段最多占用其中的 3 秒。[Go HTTP Client 说明](https://pkg.go.dev/net/http@go1.25.0#Client)

同一目标服务的多个 Client 共用服务地址和底层 HTTP 连接池，各自使用自己的请求超时。连接池参数由公共实现统一设置默认值；目标服务要求鉴权时，再补充服务级认证配置。

### TraceId 透传

公司内部 HTTP 请求统一使用 `TraceId` 请求头。入站中间件确定本次请求的 TraceId 并保存到标准 `context.Context` 后，Controller 通过 `ctx.Request.Context()` 传给 Service，Service 再将同一 Context 传给具体 Client。已有头像生成用例沿用这条传递链；不向 DTO、Params 或共享 Client 字段增加请求级 TraceId。

`base.Client.requestJSON` 从传入的 Context 调用 `traceid.FromContext(ctx)` 读取值，非空时设置出站 `TraceId` 请求头。`GetJSON`、`PostJSON` 及复用这两个方法的所有 company Client 统一生效，具体业务 Client 无须重复设置请求头。

Client 不自行生成 TraceId；Context 没有值时不附加该头。独立后台任务需要链路关联时，由任务入口通过 `traceid.New()` 生成一次，再用 `traceid.WithContext(ctx, id)` 保存并贯穿一次任务的所有调用。不得在调用过程中用 `context.Background()` 替换已有的执行 Context。

公共方法位于 [internal/traceid/trace_id.go](../../internal/traceid/trace_id.go)，入站与响应头规则见[接口处理流程](api-handle.md#24-traceid-的生成保存与透传)。当前阶段只透传自定义 `TraceId`，完整 Span、日志关联和标准链路上下文的后续规划见[日志与可观测性设计](application-log.md#4-统一标识与链路上下文)。

## thirdparty：第三方 HTTP 与 SDK 调用

**当前 Qwen、MiniMax 均已实现独立配置读取、Client 构造、单轮非流式文本生成和空闲连接清理，并按功能逐层接入进程启动装配。两个 Client 均有 dev、test、prod 三套 YAML 配置，共用项目现有的 OpenAI Go SDK 3.96 依赖；项目使用 Go 1.26。其他供应商仍为包占位。**

### 按功能组织目录

先按功能、子功能划分目录，最后通过文件名区分供应商或产品。同一功能目录内的供应商文件属于同一个 Go 包，类型名称包含具体产品名，避免同包重名。

```text
app/integrations/thirdparty/
├── clients.go
├── ai/
│   ├── clients.go
│   ├── llm/
│   │   ├── clients.go
│   │   ├── qwen.go
│   │   ├── gemini.go
│   │   ├── glm.go
│   │   └── minimax.go
│   ├── img_gen/
│   └── video_gen/
└── file_cloud_storage/
    ├── aliyun_oss.go
    └── tencent_cos.go
```

以 Qwen 为例，`ai/llm/qwen.go` 属于 `llm` 包，在该文件内组织 `QwenClient`、`QwenConfig`、`LoadQwenConfig(path)`、`NewQwenClient(config)`、`CloseIdleConnections`，以及用例专属的 `QwenGenerateTextParams`、`QwenGenerateTextResult` 和 SDK 适配。`minimax.go` 按相同边界组织 `MiniMaxClient`、`MiniMaxConfig` 及其 Loader、构造方法和用例类型。

### SDK 封装边界

Qwen 和 MiniMax Client 各自私有持有 OpenAI Go SDK 的 `ChatCompletionService`，分别通过供应商的 OpenAI 兼容接口调用。通过公开的 `NewChatCompletionService` 构造并显式注入配置，避免引入 `OPENAI_*` 环境默认头。当前覆盖单轮、非流式文本生成，将项目参数转换为 SDK 请求，并将 SDK 响应转换为只含 `Content` 的项目结果；读取候选结果前处理空候选情况，避免直接访问 `Choices[0]` 导致越界。Service 只使用项目 Client 及其参数、结果，不导入 SDK 类型。

`QwenGenerateTextParams` 包含 `Model string` 和 `Prompt string`。每次调用 `GenerateText` 都必须提供 Model，当前仅支持 `QwenModel38Max`（`qwen3.8-max`）、`QwenModel37Plus`（`qwen3.7-plus`）和 `QwenModel38Flash`（`qwen3.8-flash`）三个常量对应的精确值。模型为空、全空白或不受支持时在发送请求前报错，不裁剪空白、不设置默认模型。Qwen Client 不保存模型，支持的模型复用同一服务地址、认证配置及 Transport：

```go
params := llm.QwenGenerateTextParams{
    Model:  llm.QwenModel38Max,
    Prompt: "你是谁",
}
```

`MiniMaxGenerateTextParams` 同样要求每次传入非空白 Model，合法值原样发送，不设置默认模型。当前不额外限制 MiniMax 模型白名单；以下只是官方示例模型，不代表默认值或唯一支持的模型：

```go
params := llm.MiniMaxGenerateTextParams{
    Model:  "MiniMax-M3.1-Flash",
    Prompt: "你是谁",
}
```

### 配置注入

每个 SDK 独立声明配置类型和 Loader，`thirdparty` 顶层不维护全局 Config 或统一配置文件。配置目录按“环境 → 功能 → 子功能 → SDK 文件”组织，当前每个环境包含 Qwen、MiniMax 两份配置：

```text
config/integrations/thirdparty/
├── dev/ai/llm/
│   ├── qwen.yml
│   └── minimax.yml
├── test/ai/llm/
│   ├── qwen.yml
│   └── minimax.yml
└── prod/ai/llm/
    ├── qwen.yml
    └── minimax.yml
```

`QwenConfig`、`MiniMaxConfig` 和各自的 YAML 只保存四个连接及认证字段，不包含模型，也不添加功能层级包裹。Qwen 配置示例：

```yaml
base_url: https://llm-5kpwzn91x0zicspu.cn-beijing.maas.aliyuncs.com/compatible-mode/v1
api_key: "<实际 API Key>"
connect_timeout: 3s
request_timeout: 60s
```

MiniMax 配置示例：

```yaml
base_url: https://api.minimaxi.com/v1
api_key: "<实际 API Key>"
connect_timeout: 3s
request_timeout: 60s
```

同一 SDK 的三个环境使用相同字段结构；当前服务地址和超时值相同，API Key 直接明文写入各环境的对应 YAML，不写入日志。文档示例使用占位值，实际配置填写真实密钥。各 Loader 按字段原值读取和校验，不展开 API Key 环境变量；读取成功后通过构造参数注入 Client，调用方法不临时读取环境变量。`connect_timeout` 限制连接建立阶段，`request_timeout` 限制一次完整 SDK 调用，可按业务情况调整。兼容接口及地址格式见[阿里云官方文档](https://help.aliyun.com/en/model-studio/qwen-api-via-openai-chat-completions)和 [MiniMax 官方文档](https://platform.minimax.cn/docs/api-reference/text-openai-api)。

### 初始化、获取与资源清理

装配代码随功能目录逐层组织，只有根 `thirdparty` 维护全局 `Init`、`Current`，子层通过普通 `NewClients` 构造各自的分组：

1. `thirdparty/clients.go` 提供 `Init(configDir, environment)`，`configDir` 是 `integrations/thirdparty` 配置叶子目录。环境仅支持 `dev/test/prod`，根层选择 `<environment>/ai` 目录并调用 `ai.NewClients`，只管理一次性发布和整体资源生命周期，不读取具体供应商配置。
2. `ai/clients.go` 的 `ai.NewClients(configDir)` 接收已选环境的 `ai` 目录，调用 `llm.NewClients`，传入其下的 `llm` 目录，返回包含 LLM 的功能分组。
3. `ai/llm/clients.go` 的 `llm.NewClients(configDir)` 读取该目录中的 `qwen.yml`、`minimax.yml`，分别调用供应商 Loader 和构造方法，返回包含 Qwen、MiniMax 的 LLM 分组；后续构造失败时清理已经创建的实例。

`bootstrap.Init` 继续只负责根初始化入口的调用和失败清理，无须随供应商增加而引入具体 SDK 装配逻辑。

启动顺序为部署环境、company Client、thirdparty Client、JWT、雪花发号器、数据库、业务 Service。Client 在启动阶段同步构造，不发起模型请求。只加载已注册 Client 的配置，其配置缺失或非法时停止启动；仍为占位文件的其他 SDK 无须提前提供配置。

全部 Client 创建成功后，才在锁保护下发布共享集合；重复初始化返回错误，未初始化时调用 `Current()` 会 panic。`Current()` 返回集合的值副本，各 Client 指针继续复用；发布后的实例及配置保持只读并发复用，配置修改后重启生效。获取层级与功能目录保持一致：

```go
qwenClient := thirdparty.Current().AI.LLM.Qwen
miniMaxClient := thirdparty.Current().AI.LLM.MiniMax
```

HTTP 型 SDK 使用项目构造的 HTTP Client 和 Transport，通过 `option.WithHTTPClient` 注入并复用连接。根 `Clients.CloseIdleConnections()` 按 AI、LLM 分组逐层调用，最终清理 Qwen、MiniMax 各自的空闲连接，不取消在途请求。启动后续步骤失败或监听失败、尚未处理业务请求时，清理已创建的 company 和 thirdparty 空闲连接；正常退出时，仅在 HTTP 及内部工作均确认结束后执行清理。未完全排空时跳过主动关闭共享资源，沿用既有停机约定。

### 调用上下文、重试与错误

Controller、Service 和第三方 Client 继续传递标准 `context.Context`。两个 LLM Client 均在传入 Context 上派生带 `request_timeout` 的执行 Context，保留其中的 TraceId 等值，并将该 Context 交给 SDK；总预算覆盖一次完整调用。

首期显式使用 `option.WithMaxRetries(0)` 关闭 SDK 自动重试。后续需要重试时，再明确次数及完整操作预算，避免 SDK 与业务层重复重试。HTTP Client 注入和重试选项见[OpenAI Go SDK 官方文档](https://developers.openai.com/api/reference/go)。

SDK 或远端协议错误在对应 Client 边界适配为项目可识别的错误，提供可公开英文文案，内部按需保留原始 cause 和受控诊断信息；Service 收到后原样返回，不向 Controller 传递完整供应商正文或 SDK 对象。错误边界遵守[错误文案责任](api-error.md#23-错误文案的责任)。

MiniMax 还在 Client 边界适配供应商协议错误：即使 HTTP 为 200，响应中的 `base_resp.status_code` 非零也按失败处理，不转换成成功文本。

第三方调用保留 Context 中的 TraceId 供本地关联，默认不向供应商发送 company 使用的自定义 `TraceId` 请求头。供应商明确支持并需要某种关联头时，再在对应 Client 内适配。
