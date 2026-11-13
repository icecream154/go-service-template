package dbassert

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"sync"
	"testing"
)

var registerSnapDriverOnce sync.Once

type memState struct {
	mu    sync.Mutex
	cols  []string
	rows  [][]driver.Value
	count int64
}

var snapState = &memState{}

func ensureSnapDriver() {
	registerSnapDriverOnce.Do(func() {
		sql.Register("testkit_dbassert_stub", &snapDriver{state: snapState})
	})
}

type snapDriver struct{ state *memState }

func (d *snapDriver) Open(_ string) (driver.Conn, error) { return &snapConn{state: d.state}, nil }

type snapConn struct{ state *memState }

func (c *snapConn) Prepare(_ string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *snapConn) Close() error                          { return nil }
func (c *snapConn) Begin() (driver.Tx, error)             { return nil, driver.ErrSkip }
func (c *snapConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if query == "SELECT COUNT(*) FROM `demo_items`" {
		return &sliceRows{
			cols: []string{"cnt"},
			rows: [][]driver.Value{{c.state.count}},
		}, nil
	}
	copiedRows := make([][]driver.Value, len(c.state.rows))
	for i := range c.state.rows {
		copiedRows[i] = append([]driver.Value(nil), c.state.rows[i]...)
	}
	return &sliceRows{
		cols: append([]string(nil), c.state.cols...),
		rows: copiedRows,
	}, nil
}

type sliceRows struct {
	cols []string
	rows [][]driver.Value
	idx  int
}

func (r *sliceRows) Columns() []string { return r.cols }
func (r *sliceRows) Close() error      { return nil }
func (r *sliceRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

func TestQuerySnapshotAndCountBaseline(t *testing.T) {
	ensureSnapDriver()
	db, err := sql.Open("testkit_dbassert_stub", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	snapState.mu.Lock()
	snapState.cols = []string{"name", "avatar", "version"}
	snapState.rows = [][]driver.Value{{"Alice", nil, int64(0)}}
	snapState.count = 3
	snapState.mu.Unlock()

	// 1. 单行快照未发生变化时 RequireUnchanged 正常通过，且严格区分 SQL NULL 与字符串 "<NULL>"。
	snap := CaptureSingleRow(t, db, "SELECT name, avatar, version FROM users WHERE id = ?", 1)
	snap.RequireUnchanged(t)

	if (cellValue{isNull: true}) == (cellValue{isNull: false, text: "<NULL>"}) {
		t.Fatal("SQL NULL cellValue must never equal literal string '<NULL>'")
	}

	// 2. 表行数基线增量核对。
	provider := func(_ testing.TB, _ string) *sql.DB { return db }
	counts := CaptureCounts(t, provider, Table("main", "demo_items"))
	counts.RequireUnchanged(t)
	counts.RequireDelta(t, "main", "demo_items", 0)

	snapState.mu.Lock()
	snapState.count = 5
	snapState.mu.Unlock()
	counts.RequireDelta(t, "main", "demo_items", 2)
}
