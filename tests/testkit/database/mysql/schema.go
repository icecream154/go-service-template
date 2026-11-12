package mysqltest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// schemaPlan 来自目录扫描，只保存本次要执行的 SQL，不维护业务库名或表名清单。
type schemaPlan struct {
	databases []databaseSchema
}

type databaseSchema struct {
	connection string // database/<connection>/mysql 中的业务域目录名。
	create     sqlScript
	scripts    []sqlScript
}

type sqlScript struct {
	path     string // 相对仓库路径，用于排序和错误定位。
	contents string
}

const databaseCreationFilename = "create_database.sql"

var schemaIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// repositoryRoot 解析 Options.RepositoryRoot，返回 readSchema 用来查找 database/ 的仓库绝对路径。
// root 有值时使用调用方指定的路径。root 为空时，从测试进程的工作目录向上查找声明本项目模块的 go.mod。
// 无法解析路径、获取工作目录或找到本项目时返回错误。目录的存在性和类型由 readSchema 检查。
func repositoryRoot(root string) (string, error) {
	// 显式路径也可指向测试准备的临时仓库根目录。filepath.Abs 按当前工作目录解析相对路径。
	if root != "" {
		absolute, err := filepath.Abs(root)
		if err != nil {
			// 标记路径解析阶段并保留底层错误，供调用方定位指定目录的问题。
			return "", fmt.Errorf("resolve repository root: %w", err)
		}
		return absolute, nil
	}

	// 自动发现从测试进程的当前工作目录开始，支持在仓库的子包目录中运行测试。
	current, err := os.Getwd()
	if err != nil {
		// 获取工作目录失败时向上传递系统错误，调用方据此结束扫描。
		return "", err
	}

	// 逐级检查当前目录及其父目录，返回最近一个模块声明匹配的目录。
	for {
		// 用 go.mod 开头的模块声明识别仓库，读取成功且匹配时即找到 SQL 扫描根目录。
		module, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil && strings.HasPrefix(string(module), "module go-service-template\n") {
			return current, nil
		}

		// 移到上一级继续查找。父目录等于当前目录时，说明已经查到文件系统根目录。
		parent := filepath.Dir(current)
		if parent == current {
			// 自动查找耗尽时，错误会提示调用方显式设置 Options.RepositoryRoot。
			return "", errors.New("cannot locate repository; set Options.RepositoryRoot")
		}
		current = parent
	}
}

// readSchema 从仓库的 database/ 目录发现 MySQL 业务域，递归读取 SQL 并生成初始化计划。
// 只有 mysql/ 根目录提供 create_database.sql 的业务域才会加入计划。
// 没有 database/ 目录或符合条件的业务域时，返回空计划，表示项目未声明 MySQL 依赖。
// 路径无效、读取失败或已声明的 SQL 不符合要求时，返回错误。
func readSchema(root string) (schemaPlan, error) {
	var plan schemaPlan

	// 先确认仓库根目录有效，避免把传错路径误认为项目没有 MySQL 依赖。
	info, err := os.Stat(root)
	if err != nil {
		return plan, fmt.Errorf("inspect repository root: %w", err)
	}
	if !info.IsDir() {
		return plan, fmt.Errorf("repository root %s must be a directory", root)
	}

	// 读取业务域列表。项目可以没有 database/；其余读取错误仍须报告。
	// ReadDir 按名称排序，业务域的处理顺序由此确定。
	directory := filepath.Join(root, "database")
	domains, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return plan, nil
	}
	if err != nil {
		return plan, fmt.Errorf("scan database directory: %w", err)
	}

	for _, domain := range domains {
		// 跳过 database/ 下的非目录条目，例如说明文件。
		if !domain.IsDir() {
			continue
		}

		// 没有 mysql/ 子目录的业务域会跳过，例如只定义了 Redis 的业务域。
		mysqlDir := filepath.Join(directory, domain.Name(), "mysql")
		entries, err := os.ReadDir(mysqlDir)
		if os.IsNotExist(err) {
			continue
		}
		// 其余读取错误会中止扫描，例如权限不足或 mysql 路径指向普通文件。
		if err != nil {
			return plan, fmt.Errorf("read MySQL directory %s: %w", mysqlDir, err)
		}

		// 建库文件必须直接位于 mysql/ 根目录，且文件名须精确匹配 create_database.sql。
		// 空目录、只有建表脚本或建库脚本只在子目录时，跳过整个业务域。
		if !slices.ContainsFunc(entries, func(entry fs.DirEntry) bool {
			return !entry.IsDir() && entry.Name() == databaseCreationFilename
		}) {
			continue
		}

		// 业务域名用作连接名。参与初始化的业务域必须符合小写 snake_case 命名规则。
		if !schemaIdentifier.MatchString(domain.Name()) {
			return plan, fmt.Errorf("invalid MySQL domain directory %s; expected a directory with a lowercase snake_case domain name", mysqlDir)
		}

		// 递归读取该业务域的 SQL。遍历或读取失败、SQL 文件为空、子目录存在同名建库脚本时返回错误。
		database, err := readDatabaseSchema(root, mysqlDir, domain.Name())
		if err != nil {
			return plan, err
		}
		// 将成功读取的业务域按扫描顺序加入初始化计划。
		plan.databases = append(plan.databases, database)
	}

	// 空计划是正常结果，由创建入口据此跳过 MySQL，不要求项目必须使用该存储。
	return plan, nil
}

// readDatabaseSchema 递归读取单个业务域的 SQL，分别保存建库脚本和其他初始化脚本。
// root 是仓库根目录，用于计算脚本的相对路径；directory 是该业务域的 mysql/ 目录；
// connection 是业务域对应的连接名，直接保存到返回结果中。
// 返回结果中的 create 保存建库脚本，scripts 保存按相对路径排序的其他脚本。
// 本方法只读取文件，不执行 SQL；遍历、读取或文件组织检查失败时返回错误。
func readDatabaseSchema(root, directory, connection string) (databaseSchema, error) {
	database := databaseSchema{connection: connection}
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		// 先处理遍历错误；发生错误时 entry 可能为空，不能继续读取其属性。
		if walkErr != nil {
			return walkErr
		}

		// 只读取扩展名精确为 .sql 的文件。遇到目录返回 nil，仍会继续遍历其子目录。
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			return nil
		}

		// 使用相对仓库的路径标识脚本，便于定位文件，也作为后续排序依据。
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		// 完整读取 SQL。空文件或只有空白的文件直接报错，不把它当作有效初始化步骤。
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read schema %s: %w", relative, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return fmt.Errorf("schema %s is empty", relative)
		}

		// 路径统一使用 / 分隔；SQL 保留原始内容，在实际执行时由 MySQL 检查语法。
		script := sqlScript{path: filepath.ToSlash(relative), contents: string(contents)}
		if entry.Name() == databaseCreationFilename {
			// 建库脚本必须直接位于当前 mysql/ 目录，子目录内的同名脚本属于组织错误。
			if filepath.Dir(path) != directory {
				return fmt.Errorf("database creation script %s must be directly inside the domain mysql directory", relative)
			}
			// 单独保存建库脚本，供初始化阶段优先执行，不参与其他脚本的排序。
			database.create = script
		} else {
			database.scripts = append(database.scripts, script)
		}
		return nil
	})

	// 任一文件处理失败都会中止遍历，并补充当前业务域目录，方便定位扫描失败的位置。
	if err != nil {
		return database, fmt.Errorf("scan schema directory %s: %w", directory, err)
	}

	// 确保返回的描述包含建库脚本；即使调用方已检查过目录，这里也核对实际读取结果。
	if database.create.path == "" {
		return database, fmt.Errorf("missing %s in %s", databaseCreationFilename, directory)
	}

	// 按相对路径字典序确定其他脚本的执行顺序，不分析 SQL 之间的依赖。
	// 有先后依赖的脚本，应通过目录或文件名前缀明确顺序。
	sort.Slice(database.scripts, func(i, j int) bool { return database.scripts[i].path < database.scripts[j].path })
	return database, nil
}

// initializeSchema 在同一个管理会话中执行 plan 中的建库和其他 SQL，再创建并授权测试应用账号。
// ctx 控制借用连接及 SQL 执行的取消和超时，db 是 Create 提供的管理连接池，plan 来自 readSchema。
// password 由调用方提供，Create 使用 randomPassword 生成本轮 test_app 的十六进制密码。
// 成功时返回业务域连接名到实际库名的映射，供应用配置和数据重置共用。
// 任一步失败时返回 nil 和错误。已经执行的 SQL 可能已生效，由 Create 的失败流程尝试回收测试实例。
func initializeSchema(ctx context.Context, db *sql.DB, plan schemaPlan, password string) (map[string]string, error) {
	// 从管理池借用一个专用连接，让后续 USE 和同一业务域的脚本共用一个 MySQL 会话。
	conn, err := db.Conn(ctx)
	if err != nil {
		// 取连接失败时保留底层错误，标明失败发生在开始执行脚本之前。
		return nil, fmt.Errorf("open schema initialization session: %w", err)
	}
	// 初始化结束后将专用连接归还连接池，管理器负责整个池的生命周期。
	defer conn.Close()

	// 获取实例当前可见的库名集合，作为判断第一个建库脚本新增了哪个库的依据。
	known, err := databaseNames(ctx, conn)
	if err != nil {
		// 取得建库前的快照是识别新库的前提，失败时直接返回查询错误。
		return nil, err
	}

	// 按业务域数量预分配映射。先完成所有建库脚本，供后续脚本引用同实例中的其他业务库。
	databases := make(map[string]string, len(plan.databases))
	for _, domain := range plan.databases {
		// 执行该业务域 mysql/ 根目录下的建库脚本，失败时用相对仓库的文件路径定位错误。
		if _, err := conn.ExecContext(ctx, domain.create.contents); err != nil {
			return nil, fmt.Errorf("apply schema %s: %w", domain.create.path, err)
		}

		// 重新读取库名集合，实际创建的库由 MySQL 返回的结果确定。
		after, err := databaseNames(ctx, conn)
		if err != nil {
			// 脚本已执行，读取执行后的库名列表失败时将查询错误交给 Create 处理。
			return nil, err
		}

		// 收集这次查询中新出现的库名，用于建立业务域与实际库之间的对应关系。
		var created []string
		for name := range after {
			if !known[name] {
				created = append(created, name)
			}
		}

		// 同时检查新增项数和库总数，要求每个脚本恰好增加一个此前不存在的库。
		if len(created) != 1 || len(after) != len(known)+1 {
			return nil, fmt.Errorf("schema %s must create exactly one new database; database names must not be shared between domains", domain.create.path)
		}
		// 实际库名须符合项目的小写 snake_case 规则，后续会用于连接配置和库级授权。
		if !schemaIdentifier.MatchString(created[0]) {
			return nil, fmt.Errorf("schema %s must create a database with a lowercase snake_case name", domain.create.path)
		}

		// 登记刚建好的库。下一份脚本以这次读取的集合为起点，独立识别自己的新增库。
		databases[domain.connection] = created[0]
		known = after
	}

	// 所有业务库就绪后，依次选择每个业务域的实际库并执行该域的其余初始化脚本。
	for _, domain := range plan.databases {
		// USE 和该域的所有脚本在同一物理连接执行，支持不带库名前缀的建表 SQL。
		if _, err := conn.ExecContext(ctx, "USE "+quoteIdentifier(databases[domain.connection])); err != nil {
			return nil, fmt.Errorf("select database for domain %s: %w", domain.connection, err)
		}

		// 脚本已经按相对路径排序。任一文件失败即结束初始化，并报告出错脚本。
		for _, script := range domain.scripts {
			if _, err := conn.ExecContext(ctx, script.contents); err != nil {
				return nil, fmt.Errorf("apply schema %s: %w", script.path, err)
			}
		}
	}

	// 重新读取其余脚本执行完毕后的库名集合，供下面检查本阶段是否改动了业务库范围。
	after, err := databaseNames(ctx, conn)
	if err != nil {
		// 读取失败时结束初始化，授权基于校验通过的实际库名集合执行。
		return nil, err
	}
	// 库名集合有差异时报告脚本组织错误，确保授权、应用配置和重置都基于建库阶段的映射。
	if !maps.Equal(after, known) {
		return nil, errors.New("non-creation schema scripts changed the database set; place CREATE DATABASE only in the domain creation file")
	}

	// CREATE USER 的预处理支持有独立限制，这里使用固定的 appUser 和调用方生成的十六进制密码组装语句。
	// REQUIRE SSL 要求测试应用账号通过 TLS 连接。
	if _, err := conn.ExecContext(ctx, "CREATE USER '"+appUser+"'@'%' IDENTIFIED BY '"+password+"' REQUIRE SSL"); err != nil {
		// 返回固定错误，避免驱动错误把含有密码的 SQL 带到调用方。
		return nil, errors.New("create MySQL test application account failed")
	}

	// 逐个为实际创建的库授予 SELECT、INSERT、UPDATE 和 DELETE 权限，供测试应用账号读写业务数据。
	for _, domain := range plan.databases {
		database := databases[domain.connection]
		// GRANT 的库级目标仍将下划线视为通配符，反引号引用之外还需转义。
		// 通过 schemaIdentifier 校验的库名中，具有通配语义的字符只有下划线。
		grantDatabase := strings.ReplaceAll(database, "_", `\_`)
		if _, err := conn.ExecContext(ctx, "GRANT SELECT, INSERT, UPDATE, DELETE ON "+quoteIdentifier(grantDatabase)+".* TO '"+appUser+"'@'%'"); err != nil {
			// 报告授权失败的实际库名，保留数据库返回的原因。
			return nil, fmt.Errorf("grant MySQL test application access to %s: %w", database, err)
		}
	}

	// 等所有业务库初始化和授权成功后，将完整映射交给 Create。
	return databases, nil
}

// databaseNames 查询 information_schema.SCHEMATA，返回当前会话可见的库名集合。
// conn 是初始化时借用的管理连接，ctx 控制查询和结果读取。
// 返回的 map 以库名为键，值固定为 true。initializeSchema 在错误为 nil 时使用这份快照比较脚本执行前后的状态。
func databaseNames(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	// 使用执行初始化 SQL 的管理会话查询 SCHEMA_NAME，使查询与脚本使用同一账号和实例。
	rows, err := conn.QueryContext(ctx, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
	if err != nil {
		// 标明库名查询阶段，并保留数据库返回的错误。
		return nil, fmt.Errorf("list MySQL databases: %w", err)
	}
	// 确保读取失败或提前返回时释放结果集，供调用方在同一连接上继续执行其他 SQL。
	defer rows.Close()

	// 将每行的 SCHEMA_NAME 加入集合，调用方据此检查某个库名是否已存在。
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			// 单行解码失败时，返回扫描错误，由 defer 释放结果集。
			return nil, err
		}
		names[name] = true
	}

	// Next 结束后检查迭代错误，避免将中途读取到的部分结果当成完整库名集合。
	return names, rows.Err()
}

// resetSchema 使用 TRUNCATE 清空本轮业务库中的基础表数据，供 Environment.Reset 复用测试环境。
// ctx 控制表清单查询与清空操作，db 是管理连接池，databases 来自 initializeSchema 的连接名到实际库名映射。
// 每次调用都从 information_schema 查询现有表，因此初始化之后创建的基础表也会纳入本轮重置。
// 清空逐表生效。查询或清空失败时立即返回错误，此前成功的清空操作已经生效。
func resetSchema(ctx context.Context, db *sql.DB, databases map[string]string) error {
	// 重置按实际库名操作，因此只收集映射中的值。
	names := make([]string, 0, len(databases))
	for _, name := range databases {
		names = append(names, name)
	}
	// 按库名排序，使重置顺序在不同运行间保持一致，便于定位失败位置。
	sort.Strings(names)

	// 按库查询当前的基础表，使用参数限定 TABLE_SCHEMA，表名排序也决定后续的清空顺序。
	for _, database := range names {
		rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME`, database)
		if err != nil {
			// 表清单查询失败时返回实际库名和查询错误。
			return fmt.Errorf("list tables in %s for reset: %w", database, err)
		}

		// 先读完整个表清单，再释放结果集并执行 TRUNCATE，及时把查询占用的连接归还给池。
		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				// 单行解码失败时立即释放当前结果集，再传递原始错误。
				rows.Close()
				return err
			}
			tables = append(tables, table)
		}

		// 汇总迭代和关闭结果集时的错误，只有完整读出表清单后才开始清空当前库。
		readErr := rows.Err()
		closeErr := rows.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("read tables in %s for reset: %w", database, err)
		}

		// 分别引用库名和表名，以完整限定名指定目标，支持池中未选择默认库的会话。
		for _, table := range tables {
			if _, err := db.ExecContext(ctx, "TRUNCATE TABLE "+quoteIdentifier(database)+"."+quoteIdentifier(table)); err != nil {
				// 任一张表失败就停止本轮重置，错误会带上完整的库名和表名。
				return fmt.Errorf("reset table %s.%s: %w", database, table, err)
			}
		}
	}

	// 所有目标库中的基础表都处理完成，调用方可以继续准备下一轮测试数据。
	return nil
}

// quoteIdentifier 将单个库名或表名格式化为 MySQL 的反引号标识符。
// 调用方构造带库名的表引用时，分别引用库名和表名，再用点号连接。
func quoteIdentifier(name string) string {
	// 将名称中的反引号写成两个反引号，使它保留在标识符内部，再加上外层引号。
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// verifyInstance 检查运行中的 MySQL 版本及全局变量是否符合项目的 8.4 实例基线。
// Create 在初始化脚本执行完成后调用本方法，再根据校验结果决定是否继续生成应用配置。
// ctx 是本轮启动上下文，db 是已建立的管理连接池。查询失败或任何设置偏离基线时返回错误。
func verifyInstance(ctx context.Context, db *sql.DB) error {
	// 按返回列的类型准备扫描目标。版本和文本配置用字符串，数值参数和开关用 int。
	var version, charset, collation, zone, isolation, mode, engine, binlogFormat string
	var names, secure, flush, syncBinlog, logBin, autocommit int

	// 一次查询版本和服务端全局值，以核对初始化结束时实际生效的实例设置。
	if err := db.QueryRowContext(ctx, `SELECT VERSION(), @@GLOBAL.character_set_server,
		@@GLOBAL.collation_server, @@GLOBAL.time_zone, @@GLOBAL.transaction_isolation,
		@@GLOBAL.sql_mode, @@GLOBAL.lower_case_table_names, @@GLOBAL.require_secure_transport,
		@@GLOBAL.default_storage_engine, @@GLOBAL.innodb_flush_log_at_trx_commit,
		@@GLOBAL.sync_binlog, @@GLOBAL.log_bin, @@GLOBAL.binlog_format, @@GLOBAL.autocommit`).Scan(
		&version, &charset, &collation, &zone, &isolation, &mode, &names, &secure,
		&engine, &flush, &syncBinlog, &logBin, &binlogFormat, &autocommit); err != nil {
		// 查询与结果解码错误均由 Scan 返回，保留底层错误供启动流程定位。
		return fmt.Errorf("verify MySQL instance baseline: %w", err)
	}

	// 将逗号分隔的 SQL 模式拆分并排序，使比对以启用项为准，允许不同排列顺序。
	modes := strings.Split(mode, ",")
	expectedModes := strings.Split(baselineSQLMode, ",")
	sort.Strings(modes)
	sort.Strings(expectedModes)

	// 核对版本、字符集、排序规则、时区、事务、库表命名、安全传输和日志等基线项。
	if !strings.HasPrefix(version, "8.4.") || charset != "utf8mb4" || collation != "utf8mb4_0900_bin" ||
		zone != "+00:00" || isolation != "READ-COMMITTED" || names != 1 || secure != 1 ||
		engine != "InnoDB" || flush != 1 || syncBinlog != 1 || logBin != 1 || binlogFormat != "ROW" || autocommit != 1 ||
		strings.Join(modes, ",") != strings.Join(expectedModes, ",") {
		return errors.New("MySQL test instance does not match the project 8.4 baseline")
	}

	// 实例各项设置匹配后，Create 可以继续生成应用配置并打开业务连接。
	return nil
}
