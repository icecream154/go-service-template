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
// 将 userDB 传给对应表的仓储构造函数，例如 NewUserProfileRepository(userDB)。
```

未知、禁用或已关闭的连接返回错误，不回退其他连接。普通业务连接不会自动组成跨库事务，需要跨库的流程遵守专用连接和同一事务的规范。

初始化会应用连接池与网络超时、每个新物理连接的字符集/比较规则/UTC 时区/隔离级别/SQL 模式，以及 GORM 行为和 UTC 毫秒时间函数。开发环境只允许本地回环连接关闭 TLS；测试和生产必须验证 CA 及服务器身份，禁止跳过验证或回退明文。

当前只支持 `Write` 主库。`IsOpenReadDb`、`SuppressRecordNotFound` 和 `AutoMigrate` 开启时明确报错；其余已有 GORM 开关按配置应用。未知字段、无法解码为目标字段的值及不符合基线的参数会在建立连接前被拒绝。

配置读取参考 tennis 数据库组件，直接使用 YAML 解码器和强类型结构体，不维护配置工厂、缓存或热更新。加载流程集中在 `config.go`：读取所选文件 → 解码并检查未知字段 → 显式展开字符串占位符 → 校验必要约束。字段含义定义在 `types.go`，纯校验逻辑放在 `validation.go`，便于按职责阅读。

新增配置字段时，先在 `types.go` 声明字段及对应的 YAML 标签，再按需要补充 `validation.go` 校验，并在 `client.go` 应用到驱动或 GORM。字符串字段需要支持 `${NAME}` 时，须加入 `config.go` 的显式展开列表；TLS 关闭或连接禁用时，不读取其未使用的凭据占位符。解析、校验、连接初始化和退出流程中的注释说明了各阶段的约束及资源归属。

## 用户资料仓储

仓储按表组织：`tables/user_profile.go` 声明表结构，`repository/user_profile_repository.go` 定义 `UserProfileRepository`、构造函数及该表的数据操作。后续其他表也使用各自的 `XxxRepository` 和 `xxx_repository.go`，按实际需要增加方法。

`repository.NewUserProfileRepository(userDB)` 接收已初始化的 `user` 连接，提供 `GetProfileByID(id)`、`GetProfileByAccount(source, externalID)`、`CreateProfile(profile)` 和 `UpdateProfile(id, expectedVersion, changes)`。方法直接接收或返回 `tables.UserProfile`，不做 DDD 模型转换，不接收 `context.Context`，不依赖 Web 请求。连接可以来自普通服务、脚本或后台任务；连接池由持有方统一管理。

账户查询同时匹配来源和外部标识，保留原字符串，不默认过滤停用用户。参数合法性统一由外层领域模型或调用方保证，仓储不重复校验 ID、初始版本、UTF-8、长度或字段内容。注册业务明确设置初始版本和 `IsEnabled=true`，持久化方法也支持显式保存停用状态。新增成功后回填自动时间，失败不修改传入对象。

`UpdateProfile(id, expectedVersion, tables.UserProfile{...})` 只取传入结构的 `UserName` 和 `AvatarURL`，完整替换昵称和头像，并递增版本。更新只作用于启用且旧版本匹配的记录；传入结构里的 ID、账户身份、启用状态、Version 和时间不参与修改。`AvatarURL` 使用 `sql.NullString`：`Valid=false` 写 SQL `NULL`，`Valid=true` 则保存 `String`，包括空字符串。它不是 PATCH，未提交字段由调用方提前处理。

仓储不维护自定义错误层，直接返回 GORM 或驱动提供的原始错误。查询未命中可检查 `gorm.ErrRecordNotFound`，重复键按当前数据库配置检查驱动错误或 GORM 翻译结果；仓储不额外分类、包装或记录业务日志。

`UpdateProfile` 返回 `(rowsAffected int64, err error)`。调用方先处理 `err`，再判断影响行数；`0, nil` 表示 SQL 执行正常但条件未匹配，可能是用户不存在、停用或版本过期，由外层决定业务含义。版本和启用条件仍在 SQL 中原子判断，不能仅依赖写入前的内存检查。

同库事务中可将 `tx` 传给 `NewUserProfileRepository`，由事务发起方统一提交或回滚；仓储不会切换回普通连接。事务中的创建回填仅表示该语句成功，调用方须在最终提交成功后再对外使用结果；回滚不会自动恢复内存对象。跨库流程仍遵守专用连接和同一事务的规范。

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
| `app/domains/user/repository/*_repository.go` | 每张表独立的仓储结构和数据库操作，直接使用 tables 结构 |

新增环境时同步维护 `env` 和 MySQL 加载器的环境白名单，并增加对应 YAML；新增命名连接时增加 YAML 配置并由仓储显式获取。`env/envs.go` 中记录了环境和区域扩展步骤。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试覆盖现有 YAML 的锚点继承、环境变量展开及错误脱敏、配置校验、驱动与 TLS 参数、失败资源释放和连接管理并发访问。单元测试不依赖正在运行的 MySQL；完整启动、HTTP 响应和退出清理还应使用实际 MySQL 8.4 验证。

`user_profile_repository_test.go` 使用 sqlmock 和真实 GORM MySQL 方言，覆盖表结构直接读写、账户参数绑定、`NULL` 与空串、版本及启用条件、创建失败保护和错误原因保留，不依赖正在运行的数据库。
