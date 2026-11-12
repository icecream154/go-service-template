package mysqltest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadSchemaDiscoversExtensions(t *testing.T) {
	// 先扫描一个业务域，再只增加目录和 SQL；新域与嵌套文件必须自动加入，顺序不依赖创建顺序。
	root := t.TempDir()
	writeScannerFile(t, root, "database/ledger/mysql/20_entry.sql", "CREATE TABLE journal.entry (id BIGINT PRIMARY KEY);\n")
	writeScannerFile(t, root, "database/ledger/mysql/create_database.sql", "CREATE DATABASE journal;\n")
	writeScannerFile(t, root, "database/ledger/mysql/10_account.sql", "CREATE TABLE journal.account (id BIGINT PRIMARY KEY);\n")
	writeScannerFile(t, root, "database/ledger/mysql/README.md", "not SQL")
	writeScannerFile(t, root, "database/cache/redis/seed.sql", "not MySQL")

	initial, err := readSchema(root)
	if err != nil {
		t.Fatalf("read initial schema: %v", err)
	}
	wantInitial := schemaPlan{databases: []databaseSchema{{
		connection: "ledger",
		create: sqlScript{
			path:     "database/ledger/mysql/create_database.sql",
			contents: "CREATE DATABASE journal;\n",
		},
		scripts: []sqlScript{
			{path: "database/ledger/mysql/10_account.sql", contents: "CREATE TABLE journal.account (id BIGINT PRIMARY KEY);\n"},
			{path: "database/ledger/mysql/20_entry.sql", contents: "CREATE TABLE journal.entry (id BIGINT PRIMARY KEY);\n"},
		},
	}}}
	if !reflect.DeepEqual(initial, wantInitial) {
		t.Fatalf("initial schema = %#v; want %#v", initial, wantInitial)
	}

	// 库名由 SQL 决定；目录名只确定连接名，不要求使用 <domain>_service 后缀。
	writeScannerFile(t, root, "database/inventory/mysql/tables/20_item.sql", "CREATE TABLE warehouse.item (id BIGINT PRIMARY KEY);\n")
	writeScannerFile(t, root, "database/inventory/mysql/create_database.sql", "CREATE DATABASE warehouse;\n")
	writeScannerFile(t, root, "database/inventory/mysql/tables/10_location.sql", "CREATE TABLE warehouse.location (id BIGINT PRIMARY KEY);\n")
	writeScannerFile(t, root, "database/ledger/mysql/history/30_event.sql", "CREATE TABLE journal.event (id BIGINT PRIMARY KEY);\n")

	extended, err := readSchema(root)
	if err != nil {
		t.Fatalf("read extended schema: %v", err)
	}
	wantExtended := schemaPlan{databases: []databaseSchema{
		{
			connection: "inventory",
			create: sqlScript{
				path:     "database/inventory/mysql/create_database.sql",
				contents: "CREATE DATABASE warehouse;\n",
			},
			scripts: []sqlScript{
				{path: "database/inventory/mysql/tables/10_location.sql", contents: "CREATE TABLE warehouse.location (id BIGINT PRIMARY KEY);\n"},
				{path: "database/inventory/mysql/tables/20_item.sql", contents: "CREATE TABLE warehouse.item (id BIGINT PRIMARY KEY);\n"},
			},
		},
		{
			connection: "ledger",
			create: sqlScript{
				path:     "database/ledger/mysql/create_database.sql",
				contents: "CREATE DATABASE journal;\n",
			},
			scripts: []sqlScript{
				{path: "database/ledger/mysql/10_account.sql", contents: "CREATE TABLE journal.account (id BIGINT PRIMARY KEY);\n"},
				{path: "database/ledger/mysql/20_entry.sql", contents: "CREATE TABLE journal.entry (id BIGINT PRIMARY KEY);\n"},
				{path: "database/ledger/mysql/history/30_event.sql", contents: "CREATE TABLE journal.event (id BIGINT PRIMARY KEY);\n"},
			},
		},
	}}
	if !reflect.DeepEqual(extended, wantExtended) {
		t.Errorf("extended schema = %#v; want %#v", extended, wantExtended)
	}
}

func TestReadSchemaAllowsDatabaseWithoutTables(t *testing.T) {
	// 新业务域可以先提供建库 SQL，之后再添加表；扫描器不要求预设的表数量。
	root := t.TempDir()
	writeScannerFile(t, root, "database/new_domain/mysql/create_database.sql", "CREATE DATABASE future_data;")
	plan, err := readSchema(root)
	if err != nil {
		t.Fatalf("read empty database schema: %v", err)
	}
	if len(plan.databases) != 1 || plan.databases[0].connection != "new_domain" || len(plan.databases[0].scripts) != 0 {
		t.Fatalf("expected one database without table scripts, got %#v", plan)
	}
}

func TestReadSchemaAllowsProjectWithoutMySQL(t *testing.T) {
	// 未声明任何 MySQL 业务域表示项目不需要 MySQL，扫描应成功返回空计划。
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{name: "missing database directory"},
		{name: "empty database directory", directories: []string{"database"}},
		{name: "only other storage", files: map[string]string{
			"database/cache/redis/seed.sql":        "not MySQL",
			"database/documents/mongo/schema.json": "{}",
		}},
		{name: "no domain with a root creation script", directories: []string{"database/empty/mysql"}, files: map[string]string{
			"database/catalog/mysql/item.sql":                   "CREATE TABLE item (id BIGINT);",
			"database/nested/mysql/scripts/create_database.sql": "CREATE DATABASE nested_data;",
			"database/uppercase/mysql/CREATE_DATABASE.sql":      "CREATE DATABASE uppercase_data;",
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range test.directories {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for path, contents := range test.files {
				writeScannerFile(t, root, path, contents)
			}
			plan, err := readSchema(root)
			if err != nil {
				t.Fatalf("project without MySQL should not fail discovery: %v", err)
			}
			if len(plan.databases) != 0 {
				t.Fatalf("project without MySQL should have an empty plan, got %#v", plan)
			}
		})
	}
}

func TestReadSchemaSkipsDomainsWithoutRootCreationScript(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "database", "transactions", "mysql"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeScannerFile(t, root, "database/draft/mysql/item.sql", "CREATE TABLE item (id BIGINT);")
	writeScannerFile(t, root, "database/nested/mysql/scripts/create_database.sql", "CREATE DATABASE nested_data;")
	writeScannerFile(t, root, "database/uppercase/mysql/CREATE_DATABASE.sql", "CREATE DATABASE uppercase_data;")
	writeScannerFile(t, root, "database/catalog/mysql/create_database.sql", "CREATE DATABASE catalog_data;")

	plan, err := readSchema(root)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if len(plan.databases) != 1 || plan.databases[0].connection != "catalog" {
		t.Fatalf("expected only the catalog schema, got %#v", plan)
	}
}

func TestReadSchemaRejectsIncompleteOrAmbiguousLayout(t *testing.T) {
	cases := []struct {
		name        string
		files       map[string]string
		errContains string
	}{
		// 已声明 MySQL 的业务域必须通过文件组织和 SQL 文件检查，不能当作没有依赖跳过。
		{name: "duplicate creation script in nested directory", files: map[string]string{
			"database/catalog/mysql/create_database.sql":        "CREATE DATABASE catalog_data;",
			"database/catalog/mysql/nested/create_database.sql": "CREATE DATABASE hidden_data;",
		}, errContains: "database/catalog/mysql/nested/create_database.sql"},
		// 空脚本必须指明所在文件，不能悄悄遗漏初始化步骤。
		{name: "empty creation script", files: map[string]string{
			"database/catalog/mysql/create_database.sql": " \n\t",
		}, errContains: "database/catalog/mysql/create_database.sql"},
		{name: "empty nested script", files: map[string]string{
			"database/catalog/mysql/create_database.sql": "CREATE DATABASE catalog_data;",
			"database/catalog/mysql/tables/item.sql":     " \n\t",
		}, errContains: "database/catalog/mysql/tables/item.sql"},
		// 目录名将成为连接名，必须符合项目数据库组件接受的命名规则。
		{name: "hyphenated domain", files: map[string]string{"database/sales-data/mysql/create_database.sql": "CREATE DATABASE sales_data;"}},
		{name: "uppercase domain", files: map[string]string{"database/Sales/mysql/create_database.sql": "CREATE DATABASE sales_data;"}},
		{name: "numeric domain prefix", files: map[string]string{"database/1sales/mysql/create_database.sql": "CREATE DATABASE sales_data;"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for path, contents := range test.files {
				writeScannerFile(t, root, path, contents)
			}
			_, err := readSchema(root)
			if err == nil {
				t.Fatal("invalid layout must return an error")
			}
			if test.errContains != "" && !strings.Contains(filepath.ToSlash(err.Error()), test.errContains) {
				t.Errorf("error %q does not contain %q", err, test.errContains)
			}
		})
	}
}

func TestReadSchemaRejectsInvalidDirectories(t *testing.T) {
	// 不需要 MySQL 与目录本身损坏不同；路径错误不能被吞掉并返回空计划。
	for _, scenario := range []string{"missing repository", "repository is a file", "database is a file", "mysql is a file"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			switch scenario {
			case "missing repository":
				root = filepath.Join(root, "missing")
			case "repository is a file":
				writeScannerFile(t, root, "repository", "not a directory")
				root = filepath.Join(root, "repository")
			case "database is a file":
				writeScannerFile(t, root, "database", "not a directory")
			case "mysql is a file":
				writeScannerFile(t, root, "database/catalog/mysql", "not a directory")
			}
			if _, err := readSchema(root); err == nil {
				t.Fatal("invalid directory must return an error, not an empty successful plan")
			}
		})
	}
}

func TestReadSchemaDoesNotIgnoreUnreadableSQL(t *testing.T) {
	// 无法读取的 SQL 不能被当作不存在；失效链接在不同权限运行环境下都能稳定触发此条件。
	root := t.TempDir()
	writeScannerFile(t, root, "database/catalog/mysql/create_database.sql", "CREATE DATABASE catalog_data;")
	brokenPath := filepath.Join(root, "database", "catalog", "mysql", "missing.sql")
	if err := os.Symlink(filepath.Join(root, "nonexistent.sql"), brokenPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readSchema(root); err == nil {
		t.Fatal("unreadable SQL must fail discovery instead of being omitted")
	}
}

func writeScannerFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
