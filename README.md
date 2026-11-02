# Go 后端工程规范与框架示例

以 `moralspace-tennis-backend` 为参考，实现从应用初始化到健康请求响应、退出清理的完整链路。框架规范见 [应用基线](specifications/application/baseline.md)，数据库要求见 [MySQL 基线](specifications/databases/mysql/baseline.md)。

## 启动

使用 Go 1.25 或更高版本，在项目根目录执行：

```sh
go run ./cmd/web
```

默认配置为 `config/app.yml`，监听 `:8080`。可指定配置或覆盖地址：

```sh
go run ./cmd/web -config config/app.yml -addr 127.0.0.1:8081
```

其他工作目录启动时，通过 `-config` 指定配置文件的正确路径。配置加载、校验或监听失败会以非零状态退出；SIGINT/SIGTERM 触发 HTTP 收尾，再释放资源并同步日志。

## 请求与响应

```sh
curl -i -H 'TraceId: local-check' http://localhost:8080/health
curl -i http://localhost:8080/v1/health
```

两条路径共用业务用例与响应协议，返回 HTTP 200 和 TraceId 响应头：

```json
{"code":200,"msg":"Success","data":{"status":"ok"}}
```

健康检查表示进程存活。HEAD 返回相同状态且无响应体，错误方法返回 405，未知路径返回 404。参数错误沿用 tennis 的 HTTP 200 / code -400300，内部错误使用 HTTP 500 / code -500100，错误 data 为 null。

## 职责与扩展

| 位置 | 职责 |
| --- | --- |
| `cmd/web` | 参数、信号、调用顺序、HTTP 监听与关闭、退出结果 |
| `bootstrap` | 初始化配置、日志和业务运行图，提供清理入口 |
| `app/composition` | 纯业务服务装配，共享 Runtime |
| `routers` | Gin、中间件、前缀、版本组和领域挂载清单 |
| `app/domains/health/routers` | 健康 URL 声明与请求绑定 |
| `controllers/v1`、`transport` | 读取请求并转换业务调用上下文 |
| `services` | 不依赖 HTTP 的业务用例 |
| `responses/v1`、`app/utils/response` | 对外 DTO 和统一 JSON 封装 |

新增领域时扩展 Runtime 和主路由挂载清单；新增同域端点修改该域的请求、用例、控制器、响应及路由。初始化模块不注册路由，路由层不创建业务资源。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试覆盖配置和生命周期错误、实际 HTTP 协议、请求追踪、错误恢复及业务调用边界。
