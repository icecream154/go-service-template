# 公司内部 HTTP Client 规范

公司内部服务的 HTTP Client 统一放在 `app/integrations/company/` 下，按目标服务、domain、controller 划分。

本文约定目录组织、初始化、获取及配置方式，相关初始化入口、获取入口和配置加载能力尚未实现。

## 目录结构

服务和 domain 分别对应一级目录；每个 controller 在所属 domain 目录下对应一个独立的 Client 文件。

```text
app/integrations/company/
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

## 初始化方式

Client 在进程启动阶段统一、同步初始化。由 `bootstrap.Init` 加载并校验当前环境的 Client 配置，再调用 `company.Init(config)` 创建全部 Client，将解析后的配置注入对应实例。

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

`Current()` 直接返回已发布的实例集合，未完成初始化时调用视为程序装配错误。获取层级中的服务名表示被调用的目标系统，例如 `python-service-template`；Client 内部不增加业务 Service 层。

## 配置方式

配置按项目现有的 `dev/test/prod` 环境分别维护，由进程启动入口根据当前环境统一加载。

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
