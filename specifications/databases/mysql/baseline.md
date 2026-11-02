# MySQL 8.4 阿里云 RDS 基线规范

公司 Go 后端统一采用 MySQL 8.4 LTS 作为数据库能力基线，生产环境采用阿里云 RDS MySQL 8.4。本规范统一实例、库表和应用连接的基础行为，作为新建环境、数据库迁移、开发联调和上线验收的依据。

更新日期：2026-11-02。

## 适用范围与执行要求

- 新建项目和新建数据库环境必须遵守本规范。
- 标为“默认”的配置是常规业务的统一选择；偏离时应记录业务原因、影响范围、验证结果和复审时间。
- 除本规范明确的时间类型要求外，其余字段类型、主键生成策略、索引设计、实例规格、备份保留周期和主备复制策略按各自专题规定。

## 版本与环境

1. 数据库版本统一为 MySQL 8.4 LTS，生产使用阿里云 RDS MySQL 8.4。
2. 内核小版本采用阿里云实际提供且经过兼容性验证的版本，记录实例内核版本和参数模板版本，定期评估维护更新。
3. 开发、测试和持续集成环境统一使用 8.4 系列，并对齐本规范中的参数。自建环境的表名大小写参数必须在初始化数据目录前确定。
4. RDS 使用 AliSQL 内核。使用阿里云专属扩展时，必须说明适用范围，并在实际 RDS 环境验证；上线前应覆盖真实连接地址、驱动、迁移工具和业务查询。

MySQL 8.4 属于长期支持系列，适合需要稳定功能和较长维护周期的环境；RDS 的 AliSQL 内核另有厂商扩展。参见 [MySQL 版本策略](https://dev.mysql.com/doc/refman/8.4/en/mysql-releases.html)和 [AliSQL 功能说明](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/overview-of-alisql-features)。

## 基线配置

下表是公司目标配置。创建实例时应核对参数模板，不能仅依赖产品默认值。

| 项目 | 规范值 | 设置或检查位置 | 选择理由 |
| --- | --- | --- | --- |
| 默认存储引擎 | `default_storage_engine=InnoDB` | 实例参数；建表显式声明 `ENGINE=InnoDB` | 统一事务、行锁和崩溃恢复能力 |
| 字符集 | `utf8mb4` | 实例 `character_set_server`、库、表、字符列和连接 | 支持完整 Unicode，包括 Emoji 和补充字符 |
| 默认比较规则 | `utf8mb4_0900_bin` | 实例 `collation_server`、库表默认值及连接 | 保留字符差异，减少身份标识和业务编码被隐式视为相等的情况 |
| 实例时区 | UTC，即 `+00:00` | RDS 参数 `default_time_zone` | 使用统一时间基准，避免依赖宿主机时区 |
| 会话时区 | `time_zone='+00:00'` | 所有业务访问入口的每个物理连接初始化 | 对齐应用、SQL 时间函数和时间字段的解释方式 |
| 事件时间类型 | `DATETIME`，禁止 `TIMESTAMP` 列类型 | 业务表、建表与变更脚本 | 统一时间处理方式，避免隐式时区转换及 TIMESTAMP 的 2038 年范围限制 |
| 时间存储语义 | 所有业务 `DATETIME` 值均表示 UTC 时刻 | 字段注释、写入逻辑和验收 | 避免同一列混入不同本地时区的数据 |
| 库表名大小写 | `lower_case_table_names=1` | 创建实例时设置；运行态检查 | 与 RDS 默认选择一致，降低跨环境名称差异 |
| 库表命名 | 小写 `snake_case` | 建库、建表及应用 SQL | 保持名称一致，便于阅读和迁移 |
| 默认事务隔离级别 | `transaction_isolation=READ-COMMITTED` | 实例参数和应用连接 | 适合常规短事务，减少部分锁争用 |
| 自动提交 | `autocommit=ON` | 应用连接的常态 | 避免普通操作意外长期占用事务；多语句事务显式管理 |
| SQL 模式 | 本规范列出的完整六项 | 实例 `sql_mode` 和应用会话 | 约束不确定查询和常规非法写入 |
| 事务日志刷盘 | `innodb_flush_log_at_trx_commit=1` | RDS 参数模板和运行态 | 提高已提交事务的持久性 |
| Binlog 刷盘 | `sync_binlog=1` | RDS 参数模板和运行态 | 提高二进制日志持久性 |
| Binlog 格式 | `ROW` | RDS 环境验收；自建环境配置 | 对齐 RDS 行格式日志和复制行为 |

RDS 参数的开放范围参见 [MySQL 8.4 开放参数一览表](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/parameters-for-apsaradb-rds-instances-that-run-mysql-8-4)。InnoDB 的能力参见 [MySQL InnoDB 说明](https://dev.mysql.com/doc/refman/8.4/en/innodb-introduction.html)。

## 字符集与比较规则

实例参数必须同时明确配置：

```text
character_set_server = utf8mb4
collation_server     = utf8mb4_0900_bin
```

### 配置层级与继承关系

字符集与比较规则可以在实例、数据库、表和字符字段四个层级配置。创建对象时，如果当前层级的字符集和比较规则两项均未显式指定，就采用上一层的默认值：

```text
实例默认 → 数据库默认 → 表默认 → 字符字段的实际配置
```

| 层级 | 配置位置 | 作用 |
| --- | --- | --- |
| 实例 | `character_set_server`、`collation_server` | 新建数据库两项均未指定时，提供默认值 |
| 数据库 | `CREATE DATABASE ... CHARACTER SET ... COLLATE ...` | 新建表两项均未指定时，提供默认值 |
| 表 | `CREATE TABLE ... DEFAULT CHARACTER SET ... COLLATE ...` | 新建字符字段两项均未指定时，提供默认值 |
| 字符字段 | 列定义中的 `CHARACTER SET`、`COLLATE` | 确定该字段实际采用的字符集和比较规则 |

继承发生在对象创建时，各对象随后保存自己的定义。修改实例、数据库或表的默认值，不会自动修改已有下级对象；已有字段需要通过明确的结构变更处理。`collation_server` 不能作为覆盖所有库表字段的全局开关。参见 [实例级默认值](https://dev.mysql.com/doc/refman/8.4/en/charset-server.html)、[表级默认值](https://dev.mysql.com/doc/refman/8.4/en/charset-table.html)和 [字段级配置](https://dev.mysql.com/doc/refman/8.4/en/charset-column.html)。

只声明 `CHARACTER SET utf8mb4`、省略 `COLLATE` 时，MySQL 会选择该字符集的默认比较规则，不能保证继承上级的 `utf8mb4_0900_bin`；列级声明也遵循此规则。因此，建库和建表必须同时声明字符集与比较规则。

连接字符集和 `collation_connection` 属于独立的会话配置，不是上述存储层级的下一层。普通字符列与字符串常量比较时，通常使用列的比较规则；SQL 显式指定与字符集兼容的 `COLLATE` 可以改变相应表达式的规则，但不会修改字段定义。连接配置仍按本规范的 Go 应用连接要求统一。参见 [连接字符集与比较规则](https://dev.mysql.com/doc/refman/8.4/en/charset-connection.html)。

### 字段比较语义

`utf8mb4_0900_bin` 按字符码值比较，区分大小写、重音及不同的字符序列，适合作为后端身份标识和业务编码的默认规则。它采用 `NO PAD`，尾部空格参与比较。它不提供各语言习惯的排序，也不自动合并 Unicode 的不同表示形式。

首尾空格处理、Unicode 规范化和账号大小写政策必须由业务明确，并在写入与查询时保持一致。`CHAR` 有自身的补空格和读取处理行为，不能只根据比较规则推断其存储语义。参见 [MySQL 二进制比较规则说明](https://dev.mysql.com/doc/refman/8.4/en/charset-binary-collations.html)。

| 字段语义 | 处理要求 |
| --- | --- |
| 数值用户 ID | 使用数值比较，字符比较规则不参与 |
| 字符串用户 ID、外部 ID、业务编码 | 默认使用 `utf8mb4_0900_bin` |
| 昵称 | 保留原始文本，默认精确匹配；是否允许重名由业务规定 |
| 昵称搜索 | 单独规定大小写、重音、规范化、分词和排序要求 |
| 登录账号 | 明确账号等价规则；需要规范化时，生成独立账号键并以唯一索引约束 |
| 原始字节数据 | 使用 `VARBINARY` 等二进制类型 |
| 需要语言排序的文本 | 按业务显式选择语言比较规则，记录列级例外 |

需要忽略大小写或重音的匹配必须显式设计；这些规则也影响等值查询和唯一索引。不得将 `utf8mb4_0900_ai_ci` 作为未说明原因的列级覆盖。

### 建库建表与字段声明

- 建库必须同时显式声明 `CHARACTER SET utf8mb4` 和 `COLLATE utf8mb4_0900_bin`。
- 建表必须同时显式声明 `DEFAULT CHARACTER SET=utf8mb4` 和 `COLLATE=utf8mb4_0900_bin`，即使所属数据库已经使用相同配置。
- 普通字符字段不单独声明字符集与比较规则，默认继承表配置。确有不同匹配或排序需求时，按列级例外同时明确两项配置，并记录业务原因及对查询、排序和唯一约束的影响。

例如：

```sql
CREATE DATABASE service_db
    CHARACTER SET utf8mb4
    COLLATE utf8mb4_0900_bin;

CREATE TABLE service_db.baseline_example (
    id BIGINT NOT NULL COMMENT '示例主键',
    business_code VARCHAR(64) NOT NULL COMMENT '示例业务编码',
    PRIMARY KEY (id)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET=utf8mb4
  COLLATE=utf8mb4_0900_bin
  COMMENT='基线属性示例表';
```

示例中的 `business_code` 继承表配置，实际使用 `utf8mb4` 和 `utf8mb4_0900_bin`；数值字段 `id` 不适用字符比较规则。该示例只展示基础属性，主键生成方式和字段长度按业务设计。阿里云支持上述字符集与比较规则组合，参见 [RDS 字符集与比较规则配置](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/modify-the-character-set-server-and-collation-server-parameters)。

## 时区与时间语义

- 公司管理的业务表禁止使用 `TIMESTAMP` 列类型，创建时间、修改时间、支付时间和到期时间等事件时间统一使用 `DATETIME`，精度按字段规范明确。RDS 内部系统表不纳入此限制。
- 所有业务 `DATETIME` 值必须表示 UTC 时刻，字段注释应注明 UTC。禁止在同一列混存北京时间等本地时间。
- 实例时区及所有业务访问连接的会话时区必须为 UTC，使用明确的 `+00:00`，不使用 `SYSTEM`。此要求覆盖应用、脚本、迁移工具、管理客户端和数据导入任务的每个物理连接。
- 所有写入方必须在写入前将事件时间转换为 UTC，读取后按 UTC 解释，再按展示需要转换。外部输入必须明确其偏移量或业务约定的来源时区，不得依赖操作系统本地时区猜测。
- 业务日历必须明确时区，例如按 `Asia/Shanghai` 计算北京时间每天零点的结算边界。生日、账期等纯日期使用 `DATE`；“某地区每天上午九点”等周期规则保存当地时间与时区规则，计算出具体事件时刻后再转换为 UTC，不能将当地时间直接写入 `DATETIME`。
- `DATETIME` 不保存时区，也不会因会话时区改变而自动转换已有值。数据库无法仅根据一个 `DATETIME` 数值判断其是否为 UTC，必须通过统一写入逻辑和业务验证保证。历史数据若保存北京时间，必须先确认含义，再制定转换方案。
- RDS 配置项名是 `default_time_zone`；运行态通过 `@@GLOBAL.time_zone` 和 `@@SESSION.time_zone` 验证。宿主机的 `system_time_zone` 无须与 UTC 相同。

例如，同一时刻的输入 `2026-11-02T10:00:00+08:00` 与 `2026-11-02T02:00:00Z`，写入 `DATETIME` 后都必须保存为 `2026-11-02 02:00:00`。仅设置会话 UTC，不会自动把普通字符串中的北京时间转换为 UTC。

禁止范围针对列类型。`DATETIME` 仍可使用 `DEFAULT CURRENT_TIMESTAMP(p)` 和 `ON UPDATE CURRENT_TIMESTAMP(p)`，其中 `p` 与列精度一致；生成值时会话必须为 UTC。参见 [MySQL 时间类型范围](https://dev.mysql.com/doc/refman/8.4/en/datetime.html)和 [自动初始化与更新](https://dev.mysql.com/doc/refman/8.4/en/timestamp-initialization.html)。

修改 RDS 时区需要重启实例，应安排维护时间并验证新连接。参见 [RDS 时区设置](https://help.aliyun.com/zh/rds/support/how-do-i-view-and-modify-the-time-zone-of-an-apsaradb-rds-for-mysql-instance)和 [MySQL 时区与时间字段说明](https://dev.mysql.com/doc/refman/8.4/en/time-zone-support.html)。

## SQL 模式

实例与应用会话的 `sql_mode` 统一为以下六项，顺序不影响含义：

```text
ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION
```

| 模式 | 选择理由 |
| --- | --- |
| `ONLY_FULL_GROUP_BY` | 约束结果不确定的分组查询 |
| `STRICT_TRANS_TABLES` | 让 InnoDB 的常规超长、越界等非法写入及时报错 |
| `NO_ZERO_IN_DATE`、`NO_ZERO_DATE` | 配合严格模式约束零日期和月日为零的日期 |
| `ERROR_FOR_DIVISION_BY_ZERO` | 配合严格模式检查写入中的除零行为 |
| `NO_ENGINE_SUBSTITUTION` | 指定引擎不可用时报错，避免静默替换 |

此组合适用于 8.4 基线；后续升级数据库系列时重新复核。严格模式不能代替业务校验，`IGNORE` 等语句行为应在 SQL 编码规范中另行约束。参见 [MySQL SQL 模式说明](https://dev.mysql.com/doc/refman/8.4/en/sql-mode.html)。

## 事务默认行为

默认采用 READ COMMITTED。该选择面向常规短事务，减少通常的间隙锁和部分锁持有带来的争用；同一事务中的两次普通查询可能读到不同提交时刻的数据。

需要多次普通查询共享同一快照时，应按事务显式选择 REPEATABLE READ，并说明一致性需求。库存、余额等并发更新仍需条件更新、锁定读取或版本检查，且应处理死锁；隔离级别不能代替业务并发控制。

应用连接常态保持 `autocommit=ON`，需要原子性的多语句操作通过显式事务完成，并确保提交或回滚。不得将一次业务的特殊会话设置泄漏给连接池中的后续请求。

RDS 文档默认隔离级别为 READ COMMITTED，社区 MySQL 默认则为 REPEATABLE READ，自建环境必须主动对齐。参见 [RDS 8.4 参数表](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/parameters-for-apsaradb-rds-instances-that-run-mysql-8-4)和 [MySQL 隔离级别说明](https://dev.mysql.com/doc/refman/8.4/en/innodb-transaction-isolation-levels.html)。

## Go 应用连接

连接初始化由统一数据库组件负责。使用 `go-sql-driver/mysql` 时，遵守以下约定；使用 ORM 时仍应检查底层连接的实际值。

| 连接项 | 规范值或要求 | 选择理由 |
| --- | --- | --- |
| `charset` | `utf8mb4` | 统一连接编码 |
| `collation` | `utf8mb4_0900_bin` | 对齐默认比较规则；与 `charset` 一起指定 |
| `parseTime` | `true` | 将 `DATE`、`DATETIME` 结果解析为 Go `time.Time`；`TIME` 列另行处理 |
| `loc` | `UTC` | 统一驱动处理时间的位置 |
| `time_zone` | `'+00:00'` | 显式设置 MySQL 会话时区 |
| `autocommit` | `1` | 对齐连接常态 |
| `multiStatements` | `false` | 保持普通业务连接的单条语句执行边界 |
| TLS | 验证 RDS CA 和连接端点主机名，最低 TLS 1.2 | 同时保证传输加密和服务端身份验证 |
| 超时与连接池 | 显式配置；数值按服务超时预算、实例连接额度和压测结果确定 | 避免所有服务盲目使用同一容量与超时 |

`loc=UTC` 不会设置 MySQL 会话时区，必须同时配置 `time_zone`。DSN 中该参数的编码写法为 `time_zone=%27%2B00%3A00%27`。每个新建的物理连接都必须完成初始化，不能仅在服务启动时随机获取一个连接执行一次设置。参见 [Go MySQL 驱动连接参数](https://github.com/go-sql-driver/mysql#dsn-data-source-name)。

### 连接加密与服务器身份验证

业务连接必须同时具备传输加密和服务器身份验证：加密传输中的 SQL、参数及查询结果，并确认连接的是目标数据库。RDS 控制台沿用 SSL 名称，MySQL 8.4 支持的协议为 TLS 1.2 和 TLS 1.3。

| 配置 | 作用 | 规范要求 |
| --- | --- | --- |
| RDS 开启 SSL | 支持加密连接，但仍可能接受非加密连接 | 必须开启 |
| RDS 强制 SSL | 拒绝非加密连接 | 必须开启强制加密 |
| Go 加载 RDS CA | 验证数据库证书是否具有可信的签发链 | 从 RDS 控制台获取 CA，加载到可信证书池 |
| Go 设置 `ServerName` | 核对证书是否属于准备连接的数据库域名 | 与实际连接端点及其证书匹配 |
| `tls=skip-verify` | 跳过服务器身份验证，连接即使加密也无法可靠确认对方身份 | 禁止使用 |
| `tls=preferred` 或其他明文回退配置 | 不能保证服务器身份验证和始终加密，可能退回非加密连接 | 禁止使用 |

RDS 的“开启 SSL”和“强制 SSL”是不同设置，必须分别确认。参见 [RDS SSL 配置说明](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/configure-a-cloud-certificate-to-enable-ssl-encryption)。驱动配置行为参见 [Go MySQL 驱动 TLS 说明](https://github.com/go-sql-driver/mysql#tls)。

例如，实际连接地址为 `rm-example.mysql.rds.aliyuncs.com:3306`，且证书覆盖该域名时，Go TLS 配置中的 `ServerName` 应为：

```text
rm-example.mysql.rds.aliyuncs.com
```

`ServerName` 使用证书覆盖的目标域名，不包含端口，也不是实例显示名称、数据库名或账号名。Go 侧加载 RDS CA 后，必须保持证书链、有效期和域名校验；证书不可信、已过期、域名不匹配或无法加密时，连接必须失败。不得通过跳过校验或退回明文绕过错误。参见 [Go TLS 配置说明](https://pkg.go.dev/crypto/tls#Config)。

不同访问入口必须分别核对：

- 主实例与只读实例分别检查 SSL 状态、受保护地址和证书。只读实例不会自动继承主实例的 SSL 配置，需要单独开启。
- 使用数据库代理时，应用连接的是代理域名，需要核对代理入口自己的 SSL 配置、CA 和证书覆盖域名。应用到代理、代理到 RDS 是两段连接，必须分别确认加密要求是否落实。

只读实例配置参见 [RDS SSL 配置说明](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/configure-a-cloud-certificate-to-enable-ssl-encryption)，代理入口配置参见 [RDS 代理 SSL 配置](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/configure-ssl-encryption-for-a-proxy-endpoint-on-an-apsaradb-rds-for-mysql-instance)。

## 日志与持久性

生产默认保留 `innodb_flush_log_at_trx_commit=1` 和 `sync_binlog=1`，通过 RDS 默认参数模板及运行态核对。此选择优先保障日志持久性；偏离时必须明确可接受的数据丢失范围并完成验证。主备切换的数据保护目标还需结合复制策略确定。参见 [RDS 参数模板说明](https://help.aliyun.com/zh/rds/apsaradb-rds-for-mysql/use-a-parameter-template-to-configure-the-parameters-of-apsaradb-rds-for-mysql-instances)。

RDS 的 Binlog 开启且格式固定为 `ROW`，作为托管环境验收项，不通过自定义参数模板修改。开发和自建测试环境保持行格式日志。参见 [RDS 使用限制](https://help.aliyun.com/en/rds/support/limits-of-apsaradb-rds-for-mysql)。

## 基线验收

以下 SQL 应逐条执行；应用连接保持 `multiStatements=false`，不得将包含多条语句的代码块作为一次查询发送。

### 实例运行参数

以下查询用于读取实际配置，与基线表逐项对照。实例内核完整版本还应与 RDS 控制台记录核对。

```sql
SELECT
    VERSION() AS mysql_version,
    @@version_comment AS distribution,
    @@GLOBAL.default_storage_engine AS default_storage_engine,
    @@GLOBAL.character_set_server AS character_set_server,
    @@GLOBAL.collation_server AS collation_server,
    @@GLOBAL.lower_case_table_names AS lower_case_table_names,
    @@GLOBAL.time_zone AS time_zone,
    @@GLOBAL.transaction_isolation AS transaction_isolation,
    @@GLOBAL.autocommit AS autocommit,
    @@GLOBAL.sql_mode AS sql_mode,
    @@GLOBAL.innodb_flush_log_at_trx_commit AS innodb_flush_log_at_trx_commit,
    @@GLOBAL.sync_binlog AS sync_binlog,
    @@GLOBAL.log_bin AS log_bin,
    @@GLOBAL.binlog_format AS binlog_format;
```

### 应用会话参数

从应用连接池取得一个实际连接，在同一个物理连接上执行以下检查，且应处于普通请求开始前的连接常态。普通客户端的查询只能验证该客户端自身的会话。

```sql
SELECT
    @@SESSION.character_set_client AS character_set_client,
    @@SESSION.character_set_connection AS character_set_connection,
    @@SESSION.character_set_results AS character_set_results,
    @@SESSION.collation_connection AS collation_connection,
    @@SESSION.time_zone AS time_zone,
    @@SESSION.transaction_isolation AS transaction_isolation,
    @@SESSION.autocommit AS autocommit,
    @@SESSION.sql_mode AS sql_mode;

SHOW SESSION STATUS WHERE Variable_name IN ('Ssl_cipher', 'Ssl_version');
```

三个连接字符集均应为 `utf8mb4`，连接比较规则应为 `utf8mb4_0900_bin`，时区为 UTC，常态隔离级别为 `READ-COMMITTED`，自动提交为 `1`。SQL 模式按六项集合校验，忽略顺序。`Ssl_cipher` 应非空，`Ssl_version` 应为 TLS 1.2 或 1.3；证书链和主机名校验还需检查应用 TLS 配置。

### 库表与字符列

将 `service_db` 替换为实际业务库名，并在同一个连接中执行。先确认库存在、账号具备相应可见权限且结果覆盖预期对象；空结果不能视为验收通过。

```sql
SET @baseline_schema = 'service_db';

SELECT SCHEMA_NAME, DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME
FROM information_schema.SCHEMATA
WHERE SCHEMA_NAME = @baseline_schema;

SELECT TABLE_NAME, ENGINE, TABLE_COLLATION
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = @baseline_schema
  AND TABLE_TYPE = 'BASE TABLE'
ORDER BY TABLE_NAME;

SELECT TABLE_NAME, COLUMN_NAME, CHARACTER_SET_NAME, COLLATION_NAME
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = @baseline_schema
  AND CHARACTER_SET_NAME IS NOT NULL
ORDER BY TABLE_NAME, ORDINAL_POSITION;
```

库、表默认字符集和比较规则必须对齐基线；字符列按默认值或已记录的业务例外核对。表引擎必须为 InnoDB，库表名称必须符合小写命名要求。必要时通过 `SHOW CREATE DATABASE` 和 `SHOW CREATE TABLE` 查看完整定义。

### 时间类型与 UTC 存储

本节用于上线或数据库变更时验收，依次检查字段类型、连接时区和实际保存的时间。三项检查需要分别通过。

#### 检查一 业务表是否使用了禁止的时间类型

**检查方法**：以业务库 `service_db` 为例，将其替换为实际库名。`@baseline_schema` 是临时保存目标库名的变量，必须在同一个连接中设置变量并执行后续查询。以下 SQL 只读取表和字段的定义，不修改表结构或业务数据。

```sql
SET @baseline_schema = 'service_db';

SELECT c.TABLE_NAME, c.COLUMN_NAME, c.COLUMN_TYPE
FROM information_schema.COLUMNS AS c
JOIN information_schema.TABLES AS t
  ON t.TABLE_SCHEMA = c.TABLE_SCHEMA
 AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = @baseline_schema
  AND t.TABLE_TYPE = 'BASE TABLE'
  AND c.DATA_TYPE = 'timestamp'
ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION;
```

**预期结果**：确认目标库存在、账号可查看全部目标业务表后，新建和整改完成的业务库应返回空结果，表示没有发现 `TIMESTAMP` 字段。历史字段若仍有返回，必须对应待整改清单，不能视为类型检查通过。空结果只说明字段类型符合要求，不能证明数据已经按 UTC 保存。

**具体例子**：如果查询返回以下内容：

| TABLE_NAME | COLUMN_NAME | COLUMN_TYPE |
| --- | --- | --- |
| orders | created_at | timestamp(3) |

说明 `orders.created_at` 使用了禁止的 `TIMESTAMP(3)` 类型，需要按迁移方案整改。

DDL 自动检查应识别字段类型，不能只搜索 `TIMESTAMP` 这几个字母。以下是两个字段定义片段：

```sql
created_at TIMESTAMP(3)                                -- 禁止的字段类型
created_at DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)    -- 允许的类型与默认时间函数
```

第二行中的 `CURRENT_TIMESTAMP(3)` 是生成当前时间的函数，不是字段类型。列元数据参见 [MySQL COLUMNS 表说明](https://dev.mysql.com/doc/refman/8.4/en/information-schema-columns-table.html)。

#### 检查二 实际连接是否使用 UTC

**检查方法**：分别通过 Go 应用、脚本、迁移工具、管理客户端和导入任务实际使用的连接执行：

```sql
SELECT @@SESSION.time_zone AS session_time_zone;
```

`@@SESSION.time_zone` 是 MySQL 自带的“当前连接时区”设置。Go 应用需要从连接池取得实际连接执行检查，同时核对驱动配置中的 `loc=UTC`；SQL 无法检查 Go 驱动的 `loc`。

**预期结果**：每个业务访问入口的实际连接均返回 `+00:00`，Go 驱动的 `loc` 为 `UTC`。连接组件还必须保证每个新建的物理连接都完成初始化，包括断线重连后的新连接。

**具体例子**：假设检查到以下结果：

| 检查入口 | 当前连接时区 | 判断 |
| --- | --- | --- |
| 管理客户端 | `+00:00` | 这个客户端连接通过 |
| Go 应用连接 | `+08:00` | 应用连接不通过，需要修正连接配置 |

管理客户端查到 UTC，只能证明该客户端的连接正确，不能代表应用连接也正确。每个连接可以单独覆盖实例默认时区，参见 [MySQL 会话时区说明](https://dev.mysql.com/doc/refman/8.4/en/time-zone-support.html)。

#### 检查三 程序是否正确转换并保存 UTC 时间

**检查方法**：在测试环境选择一个实际允许业务输入的事件时间字段，例如 `occurred_at`。通过项目真实写入流程，分别提交下表中的两个时间输入，创建两条可独立查询的测试记录。它们表示同一时刻，应用应先完成解析和 UTC 转换，再写入 `DATETIME`。测试使用项目已有的业务入口或写入组件，无须新增专用接口。

| 测试记录 | 业务输入 | 输入含义 | 数据库预期保存值 |
| --- | --- | --- | --- |
| 记录 A | `2026-11-02T10:00:00+08:00` | 北京时间 10 点 | `2026-11-02 02:00:00` |
| 记录 B | `2026-11-02T02:00:00Z` | UTC 2 点 | `2026-11-02 02:00:00` |

通过 SQL 查询这两条记录的原始字段值，并通过应用按 UTC 读取，核对其表示的实际时刻。表格省略小数秒，实际检查按字段精度核对。数据库自动生成或更新的时间字段，应另外验证生成值对应执行时的 UTC 时刻。

**预期结果**：两条记录都保存为 UTC 的 `02:00:00`，应用读取后表示同一时刻，可格式化为 `2026-11-02T02:00:00Z`。`DATETIME` 的 SQL 查询结果本身不带 `Z`，UTC 含义由应用按规范解释。页面按北京时间显示为 10 点是正常的展示转换。

**错误例子**：程序收到北京时间 10 点，错误地原样保存为 `2026-11-02 10:00:00`，再读回来仍是 10 点。虽然写入与读回数值相同，但按本规范应保存为 UTC 2 点，因此检查不通过。

`DATETIME` 不记录时区，不同会话时区直接查询同一个字段也会得到相同数值。因此，只检查“写入后读回一样”或“换连接时区后查询结果一样”，不能证明 UTC 转换正确；必须同时核对输入代表的时刻、实际保存值及应用读取后的时刻。参见 [MySQL 时间字段与时区说明](https://dev.mysql.com/doc/refman/8.4/en/time-zone-support.html)。

第三项检查应覆盖应用及项目实际使用的脚本、迁移和导入写入流程。管理客户端手工修改时间数据时，也必须先明确来源时区并转换为 UTC。
