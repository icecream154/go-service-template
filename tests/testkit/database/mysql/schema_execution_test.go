package mysqltest

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	schemaNamesQuery = "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME"
	resetTablesQuery = "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME"
)

func TestInitializeSchemaCreatesAllDatabasesBeforeTables(t *testing.T) {
	// 两个域采用与目录名不同的库名；表脚本可引用其他域，因此必须先完成所有建库。
	db, mock := newSchemaExecutionDB(t)
	plan := schemaPlan{databases: []databaseSchema{
		{
			connection: "audit",
			create:     sqlScript{path: "database/audit/mysql/create_database.sql", contents: "CREATE DATABASE event_store"},
			scripts: []sqlScript{{
				path:     "database/audit/mysql/events.sql",
				contents: "CREATE TABLE warehouse.event_link (id BIGINT PRIMARY KEY)",
			}},
		},
		{
			connection: "inventory",
			create:     sqlScript{path: "database/inventory/mysql/create_database.sql", contents: "CREATE DATABASE warehouse"},
			scripts: []sqlScript{{
				path:     "database/inventory/mysql/items.sql",
				contents: "CREATE TABLE items (id BIGINT PRIMARY KEY)",
			}},
		},
	}}
	// 严格顺序保证两个 CREATE DATABASE 都早于第一个域的 USE 和表脚本。
	expectSchemaNames(mock)
	expectSchemaStatement(mock, "CREATE DATABASE event_store")
	expectSchemaNames(mock, "event_store")
	expectSchemaStatement(mock, "CREATE DATABASE warehouse")
	expectSchemaNames(mock, "event_store", "warehouse")
	expectSchemaStatement(mock, "USE `event_store`")
	expectSchemaStatement(mock, "CREATE TABLE warehouse.event_link (id BIGINT PRIMARY KEY)")
	expectSchemaStatement(mock, "USE `warehouse`")
	expectSchemaStatement(mock, "CREATE TABLE items (id BIGINT PRIMARY KEY)")
	expectSchemaNames(mock, "event_store", "warehouse")
	expectSchemaStatement(mock, "CREATE USER 'test_app'@'%' IDENTIFIED BY '0123abcd' REQUIRE SSL")
	// 库级授权中的下划线必须转义，不能把 event_store 当作匹配其他库名的模式。
	expectSchemaStatement(mock, "GRANT SELECT, INSERT, UPDATE, DELETE ON `event\\_store`.* TO 'test_app'@'%'")
	expectSchemaStatement(mock, "GRANT SELECT, INSERT, UPDATE, DELETE ON `warehouse`.* TO 'test_app'@'%'")

	actual, err := initializeSchema(t.Context(), db, plan, "0123abcd")
	if err != nil {
		t.Fatalf("initialize schema: %v", err)
	}
	want := map[string]string{"audit": "event_store", "inventory": "warehouse"}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("connections = %v; want %v", actual, want)
	}
}

func TestInitializeSchemaRejectsScriptsWithoutNewDatabase(t *testing.T) {
	for _, test := range []struct {
		name      string
		duplicate bool
	}{
		// 第二个业务域复用已有库时必须报错，不能把两个域默默指向同一个库。
		{name: "duplicate database", duplicate: true},
		// 标为建库脚本但没有创建任何库时，不能继续执行表脚本或创建账号。
		{name: "no database created"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock := newSchemaExecutionDB(t)
			badPath := "database/catalog/mysql/create_database.sql"
			plan := schemaPlan{databases: []databaseSchema{{
				connection: "catalog",
				create:     sqlScript{path: badPath, contents: "CREATE DATABASE IF NOT EXISTS shared_data"},
			}}}
			expectSchemaNames(mock)
			if test.duplicate {
				plan.databases = append([]databaseSchema{{
					connection: "audit",
					create:     sqlScript{path: "database/audit/mysql/create_database.sql", contents: "CREATE DATABASE shared_data"},
				}}, plan.databases...)
				expectSchemaStatement(mock, "CREATE DATABASE shared_data")
				expectSchemaNames(mock, "shared_data")
			}
			expectSchemaStatement(mock, "CREATE DATABASE IF NOT EXISTS shared_data")
			if test.duplicate {
				expectSchemaNames(mock, "shared_data")
			} else {
				expectSchemaNames(mock)
			}

			actual, err := initializeSchema(t.Context(), db, plan, "0123abcd")
			if err == nil || actual != nil {
				t.Fatalf("expected failed initialization without connections, got %v, %v", actual, err)
			}
			if !strings.Contains(err.Error(), badPath) {
				t.Errorf("error %q does not identify script %q", err, badPath)
			}
		})
	}
}

func TestInitializeSchemaReportsFailedTableScript(t *testing.T) {
	// SQL 执行错误必须保留原始原因及相对文件路径，不能返回部分可用的连接配置。
	db, mock := newSchemaExecutionDB(t)
	wantErr := errors.New("invalid column declaration")
	path := "database/catalog/mysql/tables/item.sql"
	plan := schemaPlan{databases: []databaseSchema{{
		connection: "catalog",
		create:     sqlScript{path: "database/catalog/mysql/create_database.sql", contents: "CREATE DATABASE inventory_data"},
		scripts:    []sqlScript{{path: path, contents: "CREATE TABLE item (invalid declaration)"}},
	}}}
	expectSchemaNames(mock)
	expectSchemaStatement(mock, "CREATE DATABASE inventory_data")
	expectSchemaNames(mock, "inventory_data")
	expectSchemaStatement(mock, "USE `inventory_data`")
	mock.ExpectExec("CREATE TABLE item (invalid declaration)").WillReturnError(wantErr)

	actual, err := initializeSchema(t.Context(), db, plan, "0123abcd")
	if actual != nil || !errors.Is(err, wantErr) {
		t.Fatalf("initialize schema = %v, %v; want nil and original SQL error", actual, err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not identify script %q", err, path)
	}
}

func TestInitializeSchemaRejectsDatabaseCreatedByOrdinaryScript(t *testing.T) {
	// 普通脚本中新建的库没有所属连接；初始化必须报错，不能遗漏授权和重置范围后宣称成功。
	db, mock := newSchemaExecutionDB(t)
	plan := schemaPlan{databases: []databaseSchema{{
		connection: "catalog",
		create:     sqlScript{path: "database/catalog/mysql/create_database.sql", contents: "CREATE DATABASE warehouse"},
		scripts: []sqlScript{{
			path:     "database/catalog/mysql/tables/misplaced.sql",
			contents: "CREATE DATABASE shadow_data",
		}},
	}}}
	expectSchemaNames(mock)
	expectSchemaStatement(mock, "CREATE DATABASE warehouse")
	expectSchemaNames(mock, "warehouse")
	expectSchemaStatement(mock, "USE `warehouse`")
	expectSchemaStatement(mock, "CREATE DATABASE shadow_data")
	expectSchemaNames(mock, "warehouse", "shadow_data")

	actual, err := initializeSchema(t.Context(), db, plan, "0123abcd")
	if actual != nil || err == nil {
		t.Fatalf("database outside creation script must fail initialization, got %v, %v", actual, err)
	}
}

func TestResetSchemaDiscoversNewTablesOnEveryCall(t *testing.T) {
	// 仅查询指定业务库中的 BASE TABLE；第二次重置发现新表，不依赖启动时或 Go 代码中的清单。
	db, mock := newSchemaExecutionDB(t)
	databases := map[string]string{"catalog": "warehouse", "audit": "event_store"}
	mock.ExpectQuery(resetTablesQuery).WithArgs("event_store").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("event"))
	expectSchemaStatement(mock, "TRUNCATE TABLE `event_store`.`event`")
	mock.ExpectQuery(resetTablesQuery).WithArgs("warehouse").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("item"))
	expectSchemaStatement(mock, "TRUNCATE TABLE `warehouse`.`item`")
	if err := resetSchema(t.Context(), db, databases); err != nil {
		t.Fatalf("first reset: %v", err)
	}

	mock.ExpectQuery(resetTablesQuery).WithArgs("event_store").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("event").AddRow("late_table"))
	expectSchemaStatement(mock, "TRUNCATE TABLE `event_store`.`event`")
	expectSchemaStatement(mock, "TRUNCATE TABLE `event_store`.`late_table`")
	mock.ExpectQuery(resetTablesQuery).WithArgs("warehouse").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("item").AddRow("item`revision"))
	expectSchemaStatement(mock, "TRUNCATE TABLE `warehouse`.`item`")
	// 元数据中的反引号必须按标识符规则转义，不能直接拼进 SQL。
	expectSchemaStatement(mock, "TRUNCATE TABLE `warehouse`.`item``revision`")
	if err := resetSchema(t.Context(), db, databases); err != nil {
		t.Fatalf("reset after adding tables: %v", err)
	}
}

func TestResetSchemaReportsIncompleteCleanup(t *testing.T) {
	for _, stage := range []string{"list", "read", "truncate"} {
		t.Run(stage, func(t *testing.T) {
			// 无论列举、读取表名还是实际清表失败，都必须让调用者知道环境尚未重置成功。
			db, mock := newSchemaExecutionDB(t)
			wantErr := errors.New("database operation failed")
			query := mock.ExpectQuery(resetTablesQuery).WithArgs("warehouse")
			switch stage {
			case "list":
				query.WillReturnError(wantErr)
			case "read":
				query.WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("item").RowError(0, wantErr))
			case "truncate":
				query.WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("item"))
				mock.ExpectExec("TRUNCATE TABLE `warehouse`.`item`").WillReturnError(wantErr)
			}
			err := resetSchema(t.Context(), db, map[string]string{"catalog": "warehouse"})
			if !errors.Is(err, wantErr) {
				t.Fatalf("reset error = %v; want original failure", err)
			}
			if !strings.Contains(err.Error(), "warehouse") {
				t.Errorf("reset error does not identify database: %v", err)
			}
			if stage == "truncate" && !strings.Contains(err.Error(), "item") {
				t.Errorf("truncate error does not identify table: %v", err)
			}
		})
	}
}

func newSchemaExecutionDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("schema SQL expectations: %v", err)
		}
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Errorf("close schema mock: %v", err)
		}
	})
	return db, mock
}

func expectSchemaNames(mock sqlmock.Sqlmock, businessNames ...string) {
	rows := sqlmock.NewRows([]string{"SCHEMA_NAME"}).
		AddRow("information_schema").AddRow("mysql").AddRow("performance_schema").AddRow("sys")
	for _, name := range businessNames {
		rows.AddRow(name)
	}
	mock.ExpectQuery(schemaNamesQuery).WillReturnRows(rows)
}

func expectSchemaStatement(mock sqlmock.Sqlmock, statement string) {
	mock.ExpectExec(statement).WillReturnResult(sqlmock.NewResult(0, 0))
}
