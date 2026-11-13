// Package dbassert 提供不依赖任何具体业务表结构或 Model 的通用 SQL 查询结果快照对比与多库表行数增量断言。
package dbassert

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var safeSQLIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// DBProvider 按逻辑连接名获取对应的 *sql.DB。
type DBProvider func(t testing.TB, connection string) *sql.DB

type cellValue struct {
	isNull bool
	text   string
}

func (c cellValue) String() string {
	if c.isNull {
		return "<NULL>"
	}
	return fmt.Sprintf("%q", c.text)
}

// QuerySnapshot 保存某条 SQL 查询在特定时刻返回的列名与全部行值快照，用于验证后续操作未产生非预期数据库修改。
type QuerySnapshot struct {
	db      *sql.DB
	query   string
	args    []any
	columns []string
	rows    [][]cellValue
}

// CaptureQuery 执行给定 SQL 查询并保存完整结果集快照（允许 0 行或多行）。
func CaptureQuery(t testing.TB, db *sql.DB, query string, args ...any) *QuerySnapshot {
	t.Helper()
	if db == nil {
		t.Fatal("dbassert.CaptureQuery requires a non-nil *sql.DB")
	}
	copiedArgs := slices.Clone(args)
	cols, rows := executeSnapshotQuery(t, db, query, copiedArgs)
	return &QuerySnapshot{
		db:      db,
		query:   query,
		args:    copiedArgs,
		columns: cols,
		rows:    rows,
	}
}

// CaptureSingleRow 执行给定 SQL 查询并断言当前恰好命中 1 行记录，防止因过滤条件拼错导致后续“0 行 == 0 行”假性通过。
func CaptureSingleRow(t testing.TB, db *sql.DB, query string, args ...any) *QuerySnapshot {
	t.Helper()
	snap := CaptureQuery(t, db, query, args...)
	if len(snap.rows) != 1 {
		t.Fatalf("dbassert.CaptureSingleRow expected exactly 1 row at capture time, got %d (SQL: %s)", len(snap.rows), query)
	}
	return snap
}

// RequireUnchanged 重新执行同一条查询，断言当前结果集与拍照时刻在行数、列顺序、NULL 状态及每一列值上完全一致。
func (s *QuerySnapshot) RequireUnchanged(t testing.TB) {
	t.Helper()
	if s == nil {
		t.Fatal("nil QuerySnapshot")
	}
	gotCols, gotRows := executeSnapshotQuery(t, s.db, s.query, s.args)
	if !slices.Equal(gotCols, s.columns) {
		t.Fatalf("SQL snapshot columns changed: got %v, want %v (SQL: %s)", gotCols, s.columns, s.query)
	}
	if len(gotRows) != len(s.rows) {
		t.Fatalf("SQL snapshot row count changed: got %d, want %d (SQL: %s)", len(gotRows), len(s.rows), s.query)
	}
	for rowIdx := range s.rows {
		for colIdx := range s.columns {
			gotCell := gotRows[rowIdx][colIdx]
			wantCell := s.rows[rowIdx][colIdx]
			if gotCell != wantCell {
				t.Fatalf("SQL snapshot row[%d].%s mutated: got %s, want %s (SQL: %s)",
					rowIdx, s.columns[colIdx], gotCell, wantCell, s.query)
			}
		}
	}
}

func executeSnapshotQuery(t testing.TB, db *sql.DB, query string, args []any) ([]string, [][]cellValue) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("dbassert query failed: %v (SQL: %s)", err, query)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("dbassert read columns failed: %v", err)
	}

	var captured [][]cellValue
	rawValues := make([]any, len(cols))
	scanTargets := make([]any, len(cols))
	for i := range rawValues {
		scanTargets[i] = &rawValues[i]
	}

	for rows.Next() {
		clear(rawValues)
		if err := rows.Scan(scanTargets...); err != nil {
			t.Fatalf("dbassert scan row failed: %v", err)
		}
		rowCells := make([]cellValue, len(cols))
		for i, v := range rawValues {
			rowCells[i] = canonicalizeCell(v)
		}
		captured = append(captured, rowCells)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("dbassert iterate rows failed: %v", err)
	}
	return cols, captured
}

func canonicalizeCell(v any) cellValue {
	if v == nil {
		return cellValue{isNull: true}
	}
	switch typed := v.(type) {
	case []byte:
		return cellValue{text: string(typed)}
	case string:
		return cellValue{text: typed}
	case time.Time:
		return cellValue{text: typed.UTC().Format(time.RFC3339Nano)}
	default:
		return cellValue{text: fmt.Sprint(typed)}
	}
}

// TableRef 标识某一逻辑数据库连接下的一张具体数据表。
type TableRef struct {
	Connection string
	TableName  string
}

// Table 构造并校验合法的 (连接名, 表名) 引用。
func Table(connection, tableName string) TableRef {
	conn := strings.TrimSpace(connection)
	tbl := strings.TrimSpace(tableName)
	if conn == "" || !safeSQLIdentifier.MatchString(tbl) {
		panic(fmt.Sprintf("dbassert.Table invalid identifier (%q, %q)", connection, tableName))
	}
	return TableRef{Connection: conn, TableName: tbl}
}

// CountBaseline 保存一组跨库数据表在基准时刻的行数，用于验证流程结束后的表级行数变化（如全链路只读零写入或精确新增 N 行）。
type CountBaseline struct {
	dbProvider DBProvider
	tables     []TableRef
	counts     map[TableRef]int64
}

// CaptureCounts 对列出的所有逻辑库表执行 COUNT(*) 并记录行数基线。
func CaptureCounts(t testing.TB, dbProvider DBProvider, tables ...TableRef) *CountBaseline {
	t.Helper()
	if dbProvider == nil {
		t.Fatal("dbassert.CaptureCounts requires a non-nil DBProvider")
	}
	if len(tables) == 0 {
		t.Fatal("dbassert.CaptureCounts requires at least one TableRef")
	}
	counts := make(map[TableRef]int64, len(tables))
	for _, ref := range tables {
		counts[ref] = queryTableCount(t, dbProvider, ref)
	}
	return &CountBaseline{
		dbProvider: dbProvider,
		tables:     slices.Clone(tables),
		counts:     counts,
	}
}

// RequireUnchanged 断言基线登记的所有库表当前行数均与拍照时刻完全相同（增量全部为 0）。
func (b *CountBaseline) RequireUnchanged(t testing.TB) {
	t.Helper()
	for _, ref := range b.tables {
		got := queryTableCount(t, b.dbProvider, ref)
		want := b.counts[ref]
		if got != want {
			t.Fatalf("table %s.%s row count changed: got %d, want unchanged %d (delta=%+d)",
				ref.Connection, ref.TableName, got, want, got-want)
		}
	}
}

// RequireDelta 断言指定逻辑连接与数据表相较基准时刻的行数增量恰好等于 wantDelta。
func (b *CountBaseline) RequireDelta(t testing.TB, connection, tableName string, wantDelta int64) {
	t.Helper()
	ref := Table(connection, tableName)
	before, ok := b.counts[ref]
	if !ok {
		t.Fatalf("table %s.%s was not included in CountBaseline", ref.Connection, ref.TableName)
	}
	after := queryTableCount(t, b.dbProvider, ref)
	if gotDelta := after - before; gotDelta != wantDelta {
		t.Fatalf("table %s.%s row count delta = %+d (before=%d, after=%d), want %+d",
			ref.Connection, ref.TableName, gotDelta, before, after, wantDelta)
	}
}

func queryTableCount(t testing.TB, dbProvider DBProvider, ref TableRef) int64 {
	t.Helper()
	db := dbProvider(t, ref.Connection)
	if db == nil {
		t.Fatalf("dbassert received nil *sql.DB for connection %q", ref.Connection)
	}
	var count int64
	query := "SELECT COUNT(*) FROM `" + ref.TableName + "`"
	if err := db.QueryRowContext(t.Context(), query).Scan(&count); err != nil {
		t.Fatalf("dbassert count %s.%s failed: %v", ref.Connection, ref.TableName, err)
	}
	return count
}
