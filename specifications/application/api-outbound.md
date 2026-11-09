# 公司内部 HTTP Client 规范

公司内部服务的 HTTP Client 统一放在 `app/integrations/company/` 下，按目标服务、domain、controller 划分。

本文约定目录组织、初始化、获取及配置方式。目前已提供公共构造方法、多环境配置读取、进程启动时的统一初始化和共享实例获取；两个 Image Client 尚未提供实际接口调用方法。

## 目录结构

服务和 domain 分别对应一级目录；每个 controller 在所属 domain 目录下对应一个独立的 Client 文件。

```text
app/integrations/company/
├── clients.go                    # 统一初始化、发布、获取及资源清理
├── config.go                     # 多环境配置读取与校验
├── python_service_template.go    # python-service-template 的具体 Client 装配
├── base/
│   ├── client.go                 # 公共 Client 结构及构造方法
│   └── transport.go              # 服务共享 Transport 的构造方法
└── python-service-template/       # 目标服务
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

## Transport 构造与共享

**每个目标服务创建并共享一个 `*http.Transport`。** 同一目标服务下不同 domain、controller 的 Client 使用同一个实例；不同目标服务分别创建自己的实例。

构造入口位于 `app/integrations/company/base/transport.go`：

```go
func NewTransport(connectTimeout time.Duration) (*http.Transport, error)
```

`connectTimeout` 对应服务级的 `connect_timeout`，必须大于零，限制 DNS 解析及 TCP 建立连接的时间。构造方法克隆 Go 默认 Transport，保留默认连接池、代理及 TLS 设置，并注入连接超时；构造过程不建立网络连接，不修改全局 `http.DefaultTransport`。

该方法每次返回独立的 Transport，共享由 `company` 的初始化流程组织：每个目标服务调用一次，再把同一个返回值传入该服务所有 `base.New(config, transport)` 调用。各 `base.Client` 分别持有自己的 `http.Client`，按 Client 级 `request_timeout` 设置总请求超时。

例如，`python-service-template` 的 `ImageEditClient` 和 `ImageGenClient` 共用一个连接超时为 3 秒的 Transport，各自的请求总超时分别为 30 秒、60 秒。Transport 的共享粒度是目标服务，Client 的组织粒度仍是服务、domain、controller。

Transport 由 `company.Clients` 持有，`base.Client` 构造时只使用传入的实例。启动后续步骤失败时，`bootstrap` 释放本次创建的空闲连接；正常退出时，进程入口在 HTTP 及内部工作全部结束后调用 `CloseIdleConnections()`，再清理数据库。

## 初始化方式

Client 在进程启动阶段统一、同步初始化。`bootstrap.Init` 在部署环境初始化后，仅调用 `company.Init(configDir, environment)`，传入 `integrations/company` 配置叶子目录和当前环境，并处理初始化错误。

配置文件读取、地址占位符解析和参数校验放在 `company/config.go`；实例创建、一次性发布及获取放在 `company/clients.go`；具体目标服务的 Transport 与 Client 装配放在对应装配文件中。`bootstrap/init.go` 负责调用顺序和失败资源清理，保持这一入口不包含具体 Client 的构造逻辑。

初始化遵循以下约定：

1. 每个 Client 在一个进程中只初始化一次，重复初始化应返回错误。
2. 全部 Client 创建成功后，才发布共享实例集合并开始处理业务请求；配置缺失或不合法时停止启动。
3. 初始化完成后，Client 实例允许并发复用，运行期间实例及其配置保持只读。
4. 配置修改后通过重启进程生效。

## 获取方式

通过 `company.Current()` 获取已初始化的共享实例集合，继续按服务、domain、controller 的层级访问具体 Client：

```go
editClient := company.Current().PythonServiceTemplate.Image.ImageEdit
genClient := company.Current().PythonServiceTemplate.Image.ImageGen
```

`Current()` 返回已发布集合的值副本，各 Client 指针继续复用；修改分组字段不会替换共享实例。未完成初始化时调用会 panic，表示程序装配错误。获取层级中的服务名表示被调用的目标系统，例如 `python-service-template`；Client 内部不增加业务 Service 层。

## 配置方式

配置保存在 `config/integrations/company/{dev,test,prod}.yml`，由 `company.Init` 按项目当前环境读取唯一文件，缺失时不回退其他环境。加载时拒绝重复字段、多份 YAML 文档、无效地址和非正超时；任何配置或装配失败都不会发布部分 Client 集合。

测试和生产的 `base_url` 分别引用 `${TEST_PYTHON_SERVICE_TEMPLATE_BASE_URL}`、`${PROD_PYTHON_SERVICE_TEMPLATE_BASE_URL}`。启动前注入相应环境变量，或在配置中填写实际地址；变量引用必须占据整个字段，缺失或为空时停止启动。

配置加载按服务名读取多个并列节点，具体 Client 按已注册服务装配。当前注册 `python-service-template` 的两个 Image Client；新增服务或 controller 时，同步增加构造和装配逻辑，未注册的配置明确报错。

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
