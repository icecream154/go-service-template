//go:build integration

package mysqltest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestEnvironmentIsolationResetAndClose 在临时目录中增加业务域和 SQL 后再次创建环境，
// 验证新增库表会被自动初始化，且重置、关闭一套环境不影响另一套。
// Docker 不可用时创建失败即测试失败，不以 Skip 隐藏环境问题。
func TestEnvironmentIsolationResetAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	repositoryRoot := t.TempDir()
	writeAcceptanceSQL(t, repositoryRoot, map[string]string{
		"database/catalog/mysql/create_database.sql": "CREATE DATABASE inventory_store CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin",
		"database/catalog/mysql/010_item.sql":        acceptanceTableDDL("item"),
	})
	options := Options{RepositoryRoot: repositoryRoot, StartupTimeout: 3 * time.Minute}
	first, err := Create(ctx, options)
	if err != nil {
		t.Fatalf("create first MySQL environment: %v", err)
	}
	if first == nil {
		t.Fatal("declared MySQL schema must create the first environment")
	}
	cleanupEnvironment(t, first)
	// 预期来自本用例的明确约定，不复用被测扫描结果来构造预期。
	firstSchema := []acceptanceDatabase{
		{connection: "catalog", database: "inventory_store", tables: []string{"item"}},
	}
	assertEnvironmentSchemaAndTLS(t, ctx, first, firstSchema)
	seedAcceptanceTables(t, ctx, first, firstSchema, "first environment")

	// 新业务域的实际库名由 SQL 指定。已有业务域增加表、子目录 SQL 和视图。
	writeAcceptanceSQL(t, repositoryRoot, map[string]string{
		"database/audit/mysql/create_database.sql":              "CREATE DATABASE event_archive CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin",
		"database/audit/mysql/010_entry.sql":                    acceptanceTableDDL("entry"),
		"database/catalog/mysql/020_tag.sql":                    acceptanceTableDDL("tag"),
		"database/catalog/mysql/extensions/030_item_detail.sql": acceptanceTableDDL("item_detail"),
		"database/catalog/mysql/extensions/040_item_view.sql":   "CREATE VIEW item_view AS SELECT id, label FROM item",
	})
	second, err := Create(ctx, options)
	if err != nil {
		t.Fatalf("create extended MySQL environment: %v", err)
	}
	if second == nil {
		t.Fatal("declared MySQL schema must create the extended environment")
	}
	cleanupEnvironment(t, second)
	if first.RunID() == "" || second.RunID() == "" || first.RunID() == second.RunID() {
		t.Fatal("environments must have distinct nonempty run IDs")
	}
	secondSchema := []acceptanceDatabase{
		{connection: "audit", database: "event_archive", tables: []string{"entry"}},
		{connection: "catalog", database: "inventory_store", tables: []string{"item", "item_detail", "tag"}},
	}
	assertEnvironmentSchemaAndTLS(t, ctx, second, secondSchema)
	assertEnvironmentSchemaAndTLS(t, ctx, first, firstSchema)
	if _, err := first.DB("audit"); err == nil {
		t.Error("adding SQL on disk must not add a connection to an existing environment")
	}
	if _, err := second.DB("user"); err == nil {
		t.Error("environment unexpectedly includes an undeclared connection")
	}
	seedAcceptanceTables(t, ctx, second, secondSchema, "second environment")
	assertAcceptanceRowCounts(t, ctx, first, firstSchema, 1)
	assertAcceptanceRowCounts(t, ctx, second, secondSchema, 1)
	assertAcceptanceLabel(t, ctx, first, "first environment")
	assertAcceptanceLabel(t, ctx, second, "second environment")

	// 重置须发现新增库和子目录中的表，跳过视图；第一套的数据和连接保持可用。
	if err := second.Reset(ctx); err != nil {
		t.Fatalf("reset extended environment: %v", err)
	}
	assertAcceptanceRowCounts(t, ctx, second, secondSchema, 0)
	assertEnvironmentSchemaAndTLS(t, ctx, second, secondSchema)
	assertAcceptanceRowCounts(t, ctx, first, firstSchema, 1)
	assertAcceptanceLabel(t, ctx, first, "first environment")
	var viewRows int
	if err := environmentDB(t, second, "catalog").QueryRowContext(ctx, "SELECT COUNT(*) FROM item_view").Scan(&viewRows); err != nil {
		t.Fatalf("view must remain queryable after reset: %v", err)
	}
	if viewRows != 0 {
		t.Errorf("view row count after reset = %d, want 0", viewRows)
	}
	seedAcceptanceTables(t, ctx, second, secondSchema, "second after reset")
	assertAcceptanceRowCounts(t, ctx, second, secondSchema, 1)
	assertAcceptanceLabel(t, ctx, second, "second after reset")
	secondDB := environmentDB(t, second, "catalog")
	secondConfigDir := second.ConfigDir()
	if _, err := os.Stat(secondConfigDir); err != nil {
		t.Fatalf("configuration must exist before close: %v", err)
	}

	// 关闭扩展后的环境，验证连接与配置文件已清理，另一套仍可读写。
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 90*time.Second)
	err = second.Close(closeCtx)
	closeCancel()
	if err != nil {
		t.Fatalf("close extended environment: %v", err)
	}
	if err := secondDB.PingContext(ctx); err == nil {
		t.Error("closed environment still accepts queries through its original pool")
	}
	if _, err := os.Stat(secondConfigDir); !os.IsNotExist(err) {
		t.Errorf("configuration directory should be removed after close; stat error: %v", err)
	}
	assertAcceptanceLabel(t, ctx, first, "first environment")
	if _, err := environmentDB(t, first, "catalog").ExecContext(ctx, "UPDATE item SET label = ? WHERE id = 1", "first remains writable"); err != nil {
		t.Fatalf("write first environment after closing second: %v", err)
	}
	assertAcceptanceLabel(t, ctx, first, "first remains writable")

	// 主动关闭后，已登记的 Cleanup 仍可重复调用 Close。
	closeCtx, closeCancel = context.WithTimeout(context.Background(), 90*time.Second)
	err = second.Close(closeCtx)
	closeCancel()
	if err != nil {
		t.Errorf("close extended environment again: %v", err)
	}
}

type acceptanceDatabase struct {
	connection string
	database   string
	tables     []string
}

func writeAcceptanceSQL(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatalf("create SQL fixture directory: %v", err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatalf("write SQL fixture %s: %v", name, err)
		}
	}
}

func acceptanceTableDDL(name string) string {
	// 仅构造本文件中指定名称的测试表，不依赖项目任何业务表。
	return "CREATE TABLE `" + name + "` (id BIGINT NOT NULL PRIMARY KEY, label VARCHAR(64) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin"
}

func cleanupEnvironment(t *testing.T, environment *Environment) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := environment.Close(ctx); err != nil {
			t.Errorf("clean up MySQL environment: %v", err)
		}
	})
}

func environmentDB(t *testing.T, environment *Environment, name string) *sql.DB {
	t.Helper()
	db, err := environment.DB(name)
	if err != nil {
		t.Fatalf("get %s database: %v", name, err)
	}
	return db
}

func assertEnvironmentSchemaAndTLS(t *testing.T, ctx context.Context, environment *Environment, expected []acceptanceDatabase) {
	t.Helper()
	for _, item := range expected {
		db := environmentDB(t, environment, item.connection)
		var actualDatabase string
		if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&actualDatabase); err != nil {
			t.Fatalf("read %s default database: %v", item.connection, err)
		}
		if actualDatabase != item.database {
			t.Errorf("%s default database = %q, want %q", item.connection, actualDatabase, item.database)
		}
		var variable, cipher string
		if err := db.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&variable, &cipher); err != nil {
			t.Fatalf("inspect %s TLS session: %v", item.connection, err)
		}
		if variable != "Ssl_cipher" || cipher == "" {
			t.Errorf("%s database has no active TLS cipher", item.connection)
		}
		rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME`, item.database)
		if err != nil {
			t.Fatalf("list %s tables: %v", item.database, err)
		}
		var actual []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				rows.Close()
				t.Fatalf("scan %s table: %v", item.database, err)
			}
			actual = append(actual, table)
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil || closeErr != nil {
			t.Fatalf("read %s tables: rows error %v; close error %v", item.database, rowsErr, closeErr)
		}
		if !slices.Equal(actual, item.tables) {
			t.Errorf("%s tables = %v, want %v", item.database, actual, item.tables)
		}
	}
}

func seedAcceptanceTables(t *testing.T, ctx context.Context, environment *Environment, schema []acceptanceDatabase, label string) {
	t.Helper()
	for _, item := range schema {
		for _, table := range item.tables {
			if _, err := environmentDB(t, environment, item.connection).ExecContext(ctx,
				"INSERT INTO `"+table+"` (id, label) VALUES (?, ?)", 1, label); err != nil {
				t.Fatalf("insert %s.%s fixture: %v", item.database, table, err)
			}
		}
	}
}

func assertAcceptanceLabel(t *testing.T, ctx context.Context, environment *Environment, want string) {
	t.Helper()
	var actual string
	if err := environmentDB(t, environment, "catalog").QueryRowContext(ctx, "SELECT label FROM item WHERE id = 1").Scan(&actual); err != nil {
		t.Fatalf("query item label: %v", err)
	}
	if actual != want {
		t.Errorf("item label = %q, want %q", actual, want)
	}
}

func assertAcceptanceRowCounts(t *testing.T, ctx context.Context, environment *Environment, schema []acceptanceDatabase, want int) {
	t.Helper()
	for _, item := range schema {
		for _, table := range item.tables {
			var actual int
			if err := environmentDB(t, environment, item.connection).QueryRowContext(ctx,
				"SELECT COUNT(*) FROM `"+table+"`").Scan(&actual); err != nil {
				t.Fatalf("count %s.%s rows: %v", item.database, table, err)
			}
			if actual != want {
				t.Errorf("%s.%s row count = %d, want %d", item.database, table, actual, want)
			}
		}
	}
}
