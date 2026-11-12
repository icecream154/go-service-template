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

func repositoryRoot(root string) (string, error) {
	if root != "" {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve repository root: %w", err)
		}
		return absolute, nil
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		module, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil && strings.HasPrefix(string(module), "module go-service-template\n") {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("cannot locate repository; set Options.RepositoryRoot")
		}
		current = parent
	}
}

// readSchema 递归读取已提供建库脚本的业务域 SQL，在启动 Docker 前报告扫描错误。
func readSchema(root string) (schemaPlan, error) {
	var plan schemaPlan
	directory := filepath.Join(root, "database")
	domains, err := os.ReadDir(directory) // ReadDir 按名称排序，执行次序不依赖文件系统遍历顺序。
	if err != nil {
		return plan, fmt.Errorf("scan database directory: %w", err)
	}
	for _, domain := range domains {
		if !domain.IsDir() {
			continue
		}
		mysqlDir := filepath.Join(directory, domain.Name(), "mysql")
		entries, err := os.ReadDir(mysqlDir)
		if os.IsNotExist(err) {
			continue // 同一业务域也可以只有 Redis、MongoDB 等其他存储。
		}
		if err != nil {
			return plan, fmt.Errorf("read MySQL directory %s: %w", mysqlDir, err)
		}
		if !slices.ContainsFunc(entries, func(entry fs.DirEntry) bool {
			return !entry.IsDir() && entry.Name() == databaseCreationFilename
		}) {
			continue
		}
		if !schemaIdentifier.MatchString(domain.Name()) {
			return plan, fmt.Errorf("invalid MySQL domain directory %s; expected a directory with a lowercase snake_case domain name", mysqlDir)
		}
		database, err := readDatabaseSchema(root, mysqlDir, domain.Name())
		if err != nil {
			return plan, err
		}
		plan.databases = append(plan.databases, database)
	}
	if len(plan.databases) == 0 {
		return plan, fmt.Errorf("no MySQL schema directories with %s found under database/<domain>/mysql", databaseCreationFilename)
	}
	return plan, nil
}

func readDatabaseSchema(root, directory, connection string) (databaseSchema, error) {
	database := databaseSchema{connection: connection}
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read schema %s: %w", relative, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return fmt.Errorf("schema %s is empty", relative)
		}
		script := sqlScript{path: filepath.ToSlash(relative), contents: string(contents)}
		if entry.Name() == databaseCreationFilename {
			if filepath.Dir(path) != directory {
				return fmt.Errorf("database creation script %s must be directly inside the domain mysql directory", relative)
			}
			database.create = script
		} else {
			database.scripts = append(database.scripts, script)
		}
		return nil
	})
	if err != nil {
		return database, fmt.Errorf("scan schema directory %s: %w", directory, err)
	}
	if database.create.path == "" {
		return database, fmt.Errorf("missing %s in %s", databaseCreationFilename, directory)
	}
	sort.Slice(database.scripts, func(i, j int) bool { return database.scripts[i].path < database.scripts[j].path })
	return database, nil
}

// initializeSchema 先建所有库，再执行各域的其他 SQL。实际库名由 MySQL 返回，不从域名拼接。
func initializeSchema(ctx context.Context, db *sql.DB, plan schemaPlan, password string) (map[string]string, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open schema initialization session: %w", err)
	}
	defer conn.Close()
	known, err := databaseNames(ctx, conn)
	if err != nil {
		return nil, err
	}
	databases := make(map[string]string, len(plan.databases))
	for _, domain := range plan.databases {
		if _, err := conn.ExecContext(ctx, domain.create.contents); err != nil {
			return nil, fmt.Errorf("apply schema %s: %w", domain.create.path, err)
		}
		after, err := databaseNames(ctx, conn)
		if err != nil {
			return nil, err
		}
		var created []string
		for name := range after {
			if !known[name] {
				created = append(created, name)
			}
		}
		if len(created) != 1 || len(after) != len(known)+1 {
			return nil, fmt.Errorf("schema %s must create exactly one new database; database names must not be shared between domains", domain.create.path)
		}
		if !schemaIdentifier.MatchString(created[0]) {
			return nil, fmt.Errorf("schema %s must create a database with a lowercase snake_case name", domain.create.path)
		}
		databases[domain.connection] = created[0]
		known = after
	}
	for _, domain := range plan.databases {
		// USE 和该域的所有脚本在同一物理连接执行，支持不带库名前缀的建表 SQL。
		if _, err := conn.ExecContext(ctx, "USE "+quoteIdentifier(databases[domain.connection])); err != nil {
			return nil, fmt.Errorf("select database for domain %s: %w", domain.connection, err)
		}
		for _, script := range domain.scripts {
			if _, err := conn.ExecContext(ctx, script.contents); err != nil {
				return nil, fmt.Errorf("apply schema %s: %w", script.path, err)
			}
		}
	}
	// 建库只允许发生在建库阶段，避免其他脚本创建的库漏出配置、授权和重置范围。
	after, err := databaseNames(ctx, conn)
	if err != nil {
		return nil, err
	}
	if !maps.Equal(after, known) {
		return nil, errors.New("non-creation schema scripts changed the database set; place CREATE DATABASE only in the domain creation file")
	}
	// 账号和密码只来自固定名称和 crypto/rand 十六进制值，不接受外部 SQL 片段。
	// 创建账号不使用参数占位符，因为 MySQL 账号管理语句的预处理支持有独立限制。
	if _, err := conn.ExecContext(ctx, "CREATE USER '"+appUser+"'@'%' IDENTIFIED BY '"+password+"' REQUIRE SSL"); err != nil {
		return nil, errors.New("create MySQL test application account failed")
	}
	for _, domain := range plan.databases {
		database := databases[domain.connection]
		// GRANT 的库级目标仍将下划线视为通配符，反引号引用之外还需转义。
		// 库名已按 snake_case 校验，因此无需处理百分号等其他通配符。
		grantDatabase := strings.ReplaceAll(database, "_", `\_`)
		if _, err := conn.ExecContext(ctx, "GRANT SELECT, INSERT, UPDATE, DELETE ON "+quoteIdentifier(grantDatabase)+".* TO '"+appUser+"'@'%'"); err != nil {
			return nil, fmt.Errorf("grant MySQL test application access to %s: %w", database, err)
		}
	}
	return databases, nil
}

func databaseNames(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
	if err != nil {
		return nil, fmt.Errorf("list MySQL databases: %w", err)
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}

// resetSchema 每次从运行中的业务库查询实际表名，新建的表不需要登记到 Go 清单中。
func resetSchema(ctx context.Context, db *sql.DB, databases map[string]string) error {
	names := make([]string, 0, len(databases))
	for _, name := range databases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, database := range names {
		rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME`, database)
		if err != nil {
			return fmt.Errorf("list tables in %s for reset: %w", database, err)
		}
		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, table)
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return fmt.Errorf("read tables in %s for reset: %w", database, err)
		}
		for _, table := range tables {
			if _, err := db.ExecContext(ctx, "TRUNCATE TABLE "+quoteIdentifier(database)+"."+quoteIdentifier(table)); err != nil {
				return fmt.Errorf("reset table %s.%s: %w", database, table, err)
			}
		}
	}
	return nil
}

func quoteIdentifier(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

func verifyInstance(ctx context.Context, db *sql.DB) error {
	var version, charset, collation, zone, isolation, mode, engine, binlogFormat string
	var names, secure, flush, syncBinlog, logBin, autocommit int
	if err := db.QueryRowContext(ctx, `SELECT VERSION(), @@GLOBAL.character_set_server,
		@@GLOBAL.collation_server, @@GLOBAL.time_zone, @@GLOBAL.transaction_isolation,
		@@GLOBAL.sql_mode, @@GLOBAL.lower_case_table_names, @@GLOBAL.require_secure_transport,
		@@GLOBAL.default_storage_engine, @@GLOBAL.innodb_flush_log_at_trx_commit,
		@@GLOBAL.sync_binlog, @@GLOBAL.log_bin, @@GLOBAL.binlog_format, @@GLOBAL.autocommit`).Scan(
		&version, &charset, &collation, &zone, &isolation, &mode, &names, &secure,
		&engine, &flush, &syncBinlog, &logBin, &binlogFormat, &autocommit); err != nil {
		return fmt.Errorf("verify MySQL instance baseline: %w", err)
	}
	modes := strings.Split(mode, ",")
	expectedModes := strings.Split(baselineSQLMode, ",")
	sort.Strings(modes)
	sort.Strings(expectedModes)
	if !strings.HasPrefix(version, "8.4.") || charset != "utf8mb4" || collation != "utf8mb4_0900_bin" ||
		zone != "+00:00" || isolation != "READ-COMMITTED" || names != 1 || secure != 1 ||
		engine != "InnoDB" || flush != 1 || syncBinlog != 1 || logBin != 1 || binlogFormat != "ROW" || autocommit != 1 ||
		strings.Join(modes, ",") != strings.Join(expectedModes, ",") {
		return errors.New("MySQL test instance does not match the project 8.4 baseline")
	}
	return nil
}
