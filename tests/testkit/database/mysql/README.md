# MySQL 隔离测试环境

测试代码调用本包的 `Create`，即可得到一个已启动、已建库建表、可以连接的独立 MySQL 环境，供集成测试与业务测试使用。用例再通过返回的数据库连接插入前置数据、查询执行结果，结束后调用 `Close` 删除环境。应用启动、SDK Mock 和具体用例的数据准备由调用方负责。

## 文件职责

| 文件 | 具体做什么 |
| --- | --- |
| [environment.go](environment.go) | 提供测试代码直接调用的方法：`Create` 创建 MySQL 容器并建立连接；`DB` 和 `ConfigDir` 返回连接与配置目录；`Reset` 清空业务表；`Close` 关闭连接并删除容器、网络和临时文件。 |
| [materials.go](materials.go) | 生成测试用 CA、服务端证书和私钥；生成供 MySQL 容器读取的服务端配置，以及供应用读取的数据库连接配置。 |
| [schema.go](schema.go) | 扫描 `database/<业务域>/mysql/` 下的 SQL，先执行建库文件，再执行其他文件；根据实际创建的库生成授权，重置时查询各业务库中的表并清空数据。库名、表名和数量都不在代码中预先列举；同时检查 MySQL 的版本和运行配置是否符合项目要求。 |
| [entrypoint.sh](entrypoint.sh) | 在容器启动时设置服务端私钥文件的属主和访问权限，使 MySQL 用户能读取私钥，再调用官方镜像的启动脚本。由 `Create` 自动放入容器并执行。 |
| [environment_test.go](environment_test.go)、[materials_test.go](materials_test.go) | 测试 testkit 自身的参数校验、清理错误处理、证书生成和配置生成逻辑；不启动容器。 |
| [schema_test.go](schema_test.go)、[schema_execution_test.go](schema_execution_test.go) | 检查新增目录和 SQL 能否自动被发现、执行顺序是否正确、授权和清空操作是否使用实际库表，以及失败时是否报告对应文件或操作；不启动容器。 |
| [environment_integration_test.go](environment_integration_test.go) | 用临时 SQL 目录创建两个真实 MySQL 环境，在两次创建之间新增业务域和表，检查新环境是否自动包含新增结构、TLS 连接和读写是否可用，以及清空或关闭一个环境是否影响另一个。 |

测试代码通过 `environment.go` 调用这套功能，证书生成、建库建表和容器启动脚本由它统一调用。Testcontainers 负责创建和删除 Docker 资源，无需手工执行 Compose 或启停脚本；数据库连接使用项目的 `internal/database/mysql`，由该组件检查 TLS 连接和数据库会话参数。

## 前提与运行

- 使用满足仓库 `../../../../go.mod` 的 Go 工具链。
- 本机 Docker 已启动，当前用户有权连接 Docker socket；首次运行需要能获取 MySQL 及 Testcontainers 清理服务的镜像。
- 当前方案针对本机 Docker，包括 macOS Docker Desktop。端口只绑定 Docker 主机的 `127.0.0.1`；远程 Docker 和容器内测试执行器需要另行设计可达网络，不能直接套用这套端口发布策略。
- 每个 MySQL 实例的数据目录使用上限 1 GiB 的 tmpfs，容器内存上限为 2 GiB。双环境验收会同时启动两个实例，请为 Docker 留出足够内存。

从仓库根目录运行 testkit 自身的单元测试，不启动 Docker：

```sh
go test ./tests/testkit/database/mysql -count=1
```

运行需要真实 MySQL 容器的测试，检查创建、读写、数据隔离、清空和关闭功能：

```sh
go test -tags=integration ./tests/testkit/database/mysql \
  -run '^TestEnvironmentIsolationResetAndClose$' -count=1 -timeout=10m -v
```

Docker 不可用时，上述容器测试会报错，不会跳过。它测试的是 MySQL 环境创建与清理功能，不会启动业务应用或调用业务接口。默认 `go test ./...` 不启动 Docker。

## 用例中使用

以下代码以仓库现有的 `user` 业务域为例，放在带 `integration` 或 `business` 构建标签的测试文件中。`RepositoryRoot` 为空时从当前工作目录向上定位本仓库；也可以显式传入项目绝对路径。示例中的业务域和查询表名由用例选择，不是 testkit 支持范围的固定清单。

```go
//go:build integration

package integration_test

import (
    "context"
    "testing"
    "time"

    mysqltest "go-service-template/tests/testkit/database/mysql"
)

func TestExample(t *testing.T) {
    env, err := mysqltest.Create(t.Context(), mysqltest.Options{})
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() {
        // 不复用已取消的用例 Context；实际接口测试须先排空并停止应用。
        ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        defer cancel()
        if err := env.Close(ctx); err != nil {
            t.Errorf("close MySQL environment %s: %v", env.RunID(), err)
        }
    })

    db, err := env.DB("user")
    if err != nil {
        t.Fatal(err)
    }
    // 用例自行在独立连接中装载 Fixture，写入后提交，再调用待测接口。
    // 此处仅检查正式表已经存在且初始为空。
    var count int
    if err := db.QueryRowContext(t.Context(),
        "SELECT COUNT(*) FROM user_service.user_profile").Scan(&count); err != nil {
        t.Fatal(err)
    }
    if count != 0 {
        t.Fatalf("initial user count = %d; want 0", count)
    }
}
```

| 方法 | 调用后会发生什么 |
| --- | --- |
| `Create(ctx, Options{})` | 启动独立 MySQL 容器，生成证书和配置，建库建表并验证连接；成功后返回 `*Environment`。任何一步失败都会返回错误，并尝试清理已创建的资源。 |
| `env.DB(name)` | 按 `database/<name>/mysql/` 中的业务域目录名返回 `*sql.DB`，连接到该目录的建库 SQL 实际创建的数据库，用于插入前置数据和查询结果；连接由 `env.Close` 统一关闭。 |
| `env.ConfigDir()` | 返回临时配置根目录的路径，目录中包含 `database/mysql/test.yml`，供应用读取测试数据库的连接配置。启动完整应用所需的其他配置由调用方补齐。 |
| `env.RunID()` | 返回本次环境的唯一标识，便于在测试报错或清理失败时定位对应资源。 |
| `env.Reset(ctx)` | 查询本次初始化的所有业务库，清空其中实际存在的表，保留表结构和连接。新增表不需要登记清理清单；调用前须确认上一条用例的请求、事务和后台任务均已结束。 |
| `env.Close(ctx)` | 关闭本对象持有的数据库连接，删除本次创建的容器、网络和临时文件。调用方须先停止应用，并关闭应用自行创建的连接；清理失败会返回错误，再次调用可重试尚未删除的资源。 |

`Create` 的可选参数如下；传入 `Options{}` 即使用默认值：

| 参数 | 作用与默认值 |
| --- | --- |
| `Options.RepositoryRoot` | 指定项目根目录，用于读取 `../../../../database` 中的建库建表 SQL。默认从测试执行目录向上查找本项目。 |
| `Options.Image` | 指定 MySQL 镜像，默认 `mysql:8.4.4`。可改为精确的 `8.4.x` 标签或 `sha256` digest；启动后仍会检查数据库版本和配置是否符合项目要求。 |
| `Options.StartupTimeout` | 限制创建环境所用的总时间，包含拉取镜像、启动、建库建表和连接验证，默认 3 分钟。首次拉取较慢时可以增加；调用方的 Context 和 `go test -timeout` 也需要允许相应时长。 |

创建失败后的清理使用独立的 90 秒超时，不复用已经取消或超时的创建 Context。

配置使用 `test_app` 测试账号、随机密码、独立 CA 和服务器名称验证。应用账号获得本次初始化的业务库上的 `SELECT`、`INSERT`、`UPDATE`、`DELETE` 权限；建库和重置使用环境私有的管理连接。初始化与应用配置都不读取仓库内的部署账号、RDS 地址或生产证书，不打印完整配置与密码。

## SQL 目录扫描与执行

1. 扫描 `../../../../database` 的业务域子目录。只有 `mysql/` 根目录中存在 `create_database.sql` 的业务域才会参与初始化，扫描器会递归读取其 `.sql` 文件。空目录和缺少建库脚本的目录会被跳过。至少需要一个符合条件的业务域。
2. 每个参与初始化的业务域对应一个业务库。连接名取业务域目录名，须符合项目的小写 snake_case 命名规则。
3. 先按业务域名称排序，执行全部建库文件。每份文件必须创建一个此前不存在的数据库；通过查询 MySQL 获取实际库名，不将目录名拼成 `<业务域>_service`，也不解析 SQL 文本来猜测库名。不同业务域不能共用同一个数据库。
4. 全部库创建成功后，依次处理各业务域。先在执行 SQL 的连接中选择该业务库，再按文件的相对路径字典序执行其余 SQL，包括子目录中的文件。脚本有先后依赖时，用目录或文件名前缀明确顺序，例如 `001_create_item.sql`、`002_add_index.sql`。
5. 每个文件包含一条 SQL，执行失败会报告文件路径和数据库错误。参与初始化的目录中出现空 SQL 文件或子目录中有同名建库脚本时，扫描会报错。当前功能用于从空实例初始化结构，已有数据库的版本迁移需另行接入正式迁移工具。

新增表时，将 SQL 放入对应 `mysql/` 目录；新增业务域时，增加目录、建库文件和所需的其他 SQL。下次 `Create` 会重新扫描，自动完成初始化、连接配置和授权，无需修改 testkit 的 Go 代码。已运行的环境保持原样。

`Reset` 查询 `information_schema.TABLES`，只清空本次初始化的业务库中的 `BASE TABLE`，不处理系统库或视图，也不根据文件名推测表名。

## 生命周期与范围

一组串行用例可以复用同一环境，在确认前一条用例已完全结束后 `Reset` 并准备下一条数据；并行执行时每组各自 `Create`。仅更换用户 ID 不能隔离全表计数、分页等行为。

`Reset` 使用 `TRUNCATE`，不是跨表事务，也不会代替应用排空工作。任一步失败应结束该环境的后续用例并销毁，不继续复用可能只有部分表被清空的状态。按照项目规范，业务表不使用物理外键；若未来改变该约定，需同步调整重置策略。

实际项目的 SQL 直接来自仓库 [database](../../../../database)。不会用 AutoMigrate 或简化测试表代替正式结构；testkit 自身的测试使用临时 SQL 目录，检查扫描和初始化功能能否适应不同的库表数量与名称。

本包通过应用真实数据库组件检查 TLS、每连接会话配置，并核对实例的版本、字符集、排序规则、UTC、SQL 模式、隔离级别、表名大小写和日志参数。两个环境可以使用相同固定库名、表名和主键，隔离由不同 MySQL 实例保证。

tmpfs 在容器停止后丢失数据，适合普通功能测试，不用于验证重启持久性。Docker 镜像、日志和缓存仍可能占用磁盘。正常及创建失败路径主动清理，Ryuk 提供 Docker 资源回收补充；宿主机或 Docker 崩溃不构成绝对无残留保证。发生清理故障时保留运行标识，按 `go-service-template.testkit.run` 标签定向排查，不执行全局 prune。

应用、HTTP 入口和 SDK Mock 的后续装配遵守[集成测试规范](../../../../specifications/test/integration-test.md)和[业务测试规范](../../../../specifications/test/business-test.md)。
