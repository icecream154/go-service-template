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

**当前仅创建目录和 Go 包骨架，尚未实现配置读取、Client 构造、初始化或实际调用，也未引入 SDK 依赖。以下内容是后续接入约定。**

### 按功能组织目录

先按功能、子功能划分目录，最后通过文件名区分供应商或产品。同一功能目录内的供应商文件属于同一个 Go 包，类型名称包含具体产品名，避免同包重名。

```text
app/integrations/thirdparty/
├── config.go
├── clients.go
├── ai/
│   ├── llm/
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

以 Qwen 为例，`ai/llm/qwen.go` 属于 `llm` 包，后续在该文件内组织 `QwenClient`、`QwenConfig`、构造方法以及用例专属的参数、结果和 SDK 适配。参数与结果可使用 `QwenGenerateTextParams`、`QwenGenerateTextResult` 等带产品限定的名称。

### SDK 封装边界

Qwen Client 私有持有 OpenAI Go SDK Client，通过阿里云的 OpenAI 兼容接口调用 Qwen。首期覆盖单轮、非流式文本生成，将项目参数转换为 SDK 请求，并将 SDK 响应转换为项目结果；读取候选结果前处理空候选情况，避免直接访问 `Choices[0]` 导致越界。Service 只使用项目 Client 及其参数、结果，不导入 SDK 类型。SDK 版本在实际接入时核对项目 Go 版本并锁定。

### 配置注入

配置计划保存在 `config/integrations/thirdparty/{dev,test,prod}.yml`，按当前环境选择唯一文件；配置层解析环境变量并校验后，通过构造参数注入 Client，调用方法不临时读取环境变量。本次不创建这些配置文件。

配置层级与功能目录、产品文件对应，例如 `ai.llm.qwen`：

```yaml
ai:
  llm:
    qwen:
      base_url: https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1
      api_key: ${DASHSCOPE_API_KEY}
      connect_timeout: 3s
      request_timeout: 60s
      model: qwen3.8-max
```

`{WorkspaceId}` 须替换为实际工作空间 ID；API Key 由部署环境提供，不写入日志。超时值是示例，按具体业务调整。`connect_timeout` 限制连接建立阶段，`request_timeout` 限制一次完整 SDK 调用。兼容接口及地址格式见[阿里云官方文档](https://help.aliyun.com/en/model-studio/qwen-api-via-openai-chat-completions)。

### 初始化、获取与资源清理

接入时由 `thirdparty/config.go` 负责配置读取、解析与校验，`thirdparty/clients.go` 负责统一初始化、发布和获取实例。`bootstrap.Init` 只调用 `thirdparty.Init` 并管理顺序与失败清理；各 Client 在启动阶段同步构造，启动不发起模型调用。

沿用一次性初始化约定：全部 Client 创建成功后才发布共享集合，配置缺失或非法时停止启动；发布后的实例及配置保持只读并发复用，配置修改后重启生效。获取层级与功能目录保持一致：

```go
client := thirdparty.Current().AI.LLM.Qwen
```

HTTP 型 SDK 使用项目构造的 HTTP Client 和 Transport，Qwen 通过 `option.WithHTTPClient` 注入并复用连接。初始化后续步骤失败时清理本次创建的空闲连接；正常退出时，待 HTTP 请求及内部工作结束后，再清理这些资源。

### 调用上下文、重试与错误

Controller、Service 和第三方 Client 继续传递标准 `context.Context`。Qwen Client 在传入 Context 上派生带 `request_timeout` 的执行 Context，保留其中的 TraceId 等值，并将该 Context 交给 SDK；总预算覆盖一次完整调用。

首期显式使用 `option.WithMaxRetries(0)` 关闭 SDK 自动重试。后续需要重试时，再明确次数及完整操作预算，避免 SDK 与业务层重复重试。HTTP Client 注入和重试选项见[OpenAI Go SDK 官方文档](https://developers.openai.com/api/reference/go)。

SDK 或远端协议错误在对应 Client 边界适配为项目可识别的错误，提供可公开英文文案，内部按需保留原始 cause 和受控诊断信息；Service 收到后原样返回，不向 Controller 传递完整供应商正文或 SDK 对象。错误边界遵守[错误文案责任](api-error.md#23-错误文案的责任)。

第三方调用保留 Context 中的 TraceId 供本地关联，默认不向供应商发送 company 使用的自定义 `TraceId` 请求头。供应商明确支持并需要某种关联头时，再在对应 Client 内适配。
