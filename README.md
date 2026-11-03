# Go 后端工程规范与框架示例

提供部署环境读取、MySQL 命名连接初始化、HTTP 服务启动和退出清理的基础链路。数据库要求见 [MySQL 基线](specifications/databases/mysql/baseline.md)、[库表设计规范](specifications/databases/mysql/schema-design.md)和 [变更与数据维护规范](specifications/databases/mysql/change-and-maintenance.md)。所有运行时日志使用英文。

## 启动

使用 Go 1.25 或更高版本和 MySQL 8.4。启动前需要通过受控 SQL 创建业务库和表；程序不自动建库或迁移表结构。核对 `config/database/mysql/dev.yml` 的本地连接配置后，在项目根目录执行：

```sh
go run ./cmd/web
```

`APP_ENV` 支持 `dev/test/prod`，未设置或为空时默认 `dev`；`APP_REGION` 目前支持 `cn`，未设置或为空时默认 `cn`。环境名和区域值严格匹配，非法值停止启动。

按环境选择唯一的 `config/database/mysql/<environment>.yml`，文件缺失不回退其他环境。默认监听 `:8080`，初始化总时限为 30 秒，HTTP 收尾时限为 10 秒。可指定配置根目录、地址和时限：

```sh
go run ./cmd/web -config-dir /srv/app/config -addr 127.0.0.1:8081 -startup-timeout 30s -shutdown-timeout 10s
```

`-config-dir` 是包含 `database/mysql` 的配置根目录，默认 `config`。其他工作目录启动时应传入正确路径；TLS CA 相对路径以所选 YAML 所在目录为基准。

测试或生产部署先注入相应 YAML 中引用的环境变量及 CA 证书，再设置 `APP_ENV=test` 或 `APP_ENV=prod`。`${NAME}` 在 YAML 解码后展开，缺失或为空时停止启动；错误信息不会打印密码或 DSN。

启动顺序为：部署环境初始化 → 配置解码、占位符展开与校验 → 创建所有启用的数据库连接池 → 连接和会话校验 → 打印环境、区域 → 监听 HTTP。任一步失败均释放已创建的连接池并以非零状态退出。SIGINT/SIGTERM 触发 HTTP 收尾，请求结束后再关闭数据库；HTTP 收尾超时会强制关闭连接并返回错误。

## 请求与响应

```sh
curl -i http://localhost:8080/health
```

`GET /health` 返回 HTTP 200：

```json
{"status":"ok"}
```

健康检查表示 HTTP 进程存活；数据库在启动时校验，此接口不会持续探测数据库。

## 数据库连接

现有配置声明 `user`、`post`、`order`，分别默认访问 `user_service`、`post_service`、`order_service`。即使地址相同，各命名连接也拥有独立连接池；仓储必须明确选择连接。

`bootstrap.Init(ctx, configDir)` 成功返回 `Runtime`，装配业务服务时传入相应连接：

```go
userDB, err := runtime.Databases.Get("user")
if err != nil {
    return err
}
// 仓储使用 userDB.WithContext(ctx) 执行业务操作。
```

未知、禁用或已关闭的连接返回错误，不回退其他连接。普通业务连接不会自动组成跨库事务，需要跨库的流程遵守专用连接和同一事务的规范。

初始化会应用连接池与网络超时、每个新物理连接的字符集/比较规则/UTC 时区/隔离级别/SQL 模式，以及 GORM 行为和 UTC 毫秒时间函数。开发环境只允许本地回环连接关闭 TLS；测试和生产必须验证 CA 及服务器身份，禁止跳过验证或回退明文。

当前只支持 `Write` 主库。`IsOpenReadDb`、`SuppressRecordNotFound` 和 `AutoMigrate` 开启时明确报错；其余已有 GORM 开关按配置应用。未知字段、无法解码为目标字段的值及不符合基线的参数会在建立连接前被拒绝。

配置读取参考 tennis 数据库组件，直接使用 YAML 解码器和强类型结构体，不维护配置工厂、缓存或热更新。加载流程集中在 `config.go`：读取所选文件 → 解码并检查未知字段 → 显式展开字符串占位符 → 校验必要约束。字段含义定义在 `types.go`，纯校验逻辑放在 `validation.go`，便于按职责阅读。

新增配置字段时，先在 `types.go` 声明字段及对应的 YAML 标签，再按需要补充 `validation.go` 校验，并在 `client.go` 应用到驱动或 GORM。字符串字段需要支持 `${NAME}` 时，须加入 `config.go` 的显式展开列表；TLS 关闭或连接禁用时，不读取其未使用的凭据占位符。解析、校验、连接初始化和退出流程中的注释说明了各阶段的约束及资源归属。

## 职责与扩展

| 位置 | 职责 |
| --- | --- |
| `cmd/web` | 参数、信号、调用顺序、HTTP 监听与关闭、退出结果 |
| `bootstrap` | 组织初始化，返回 Runtime 和资源清理入口 |
| `env` | 部署环境和区域的加载、校验及只读访问 |
| `internal/database/mysql/config.go` | 选择并解码 YAML、展开环境变量、组织校验 |
| `internal/database/mysql/types.go` | 配置结构、YAML 字段映射与逐项含义 |
| `internal/database/mysql/validation.go` | 必填值、连接池、时间、TLS 及行为约束校验 |
| `internal/database/mysql/client.go` | 创建、验证、获取和关闭命名连接池 |
| `config/database/mysql` | 各部署环境的 MySQL 连接配置 |
| `app/domains/*/models`、`tables` | 业务对象与数据库表映射 |

新增环境时同步维护 `env` 和 MySQL 加载器的环境白名单，并增加对应 YAML；新增命名连接时增加 YAML 配置并由仓储显式获取。`env/envs.go` 中记录了环境和区域扩展步骤。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试覆盖现有 YAML 的锚点继承、环境变量展开及错误脱敏、配置校验、驱动与 TLS 参数、失败资源释放和连接管理并发访问。单元测试不依赖正在运行的 MySQL；完整启动、HTTP 响应和退出清理还应使用实际 MySQL 8.4 验证。
