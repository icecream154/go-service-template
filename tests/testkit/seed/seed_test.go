package seed

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var registerStubDriverOnce sync.Once

type recordedExecDriver struct {
	mu      sync.Mutex
	queries []string
}

var stubDrv = &recordedExecDriver{}

func ensureStubSQLDriver() {
	registerStubDriverOnce.Do(func() {
		sql.Register("testkit_seed_stub", stubDrv)
	})
}

func (d *recordedExecDriver) Open(_ string) (driver.Conn, error) {
	return &stubConn{drv: d}, nil
}

type stubConn struct{ drv *recordedExecDriver }

func (c *stubConn) Prepare(_ string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *stubConn) Close() error                          { return nil }
func (c *stubConn) Begin() (driver.Tx, error)             { return nil, driver.ErrSkip }
func (c *stubConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.drv.mu.Lock()
	c.drv.queries = append(c.drv.queries, query)
	c.drv.mu.Unlock()
	return driver.RowsAffected(1), nil
}

func TestSessionIDClockExecFreezeAndLoadJSON(t *testing.T) {
	ensureStubSQLDriver()
	db, err := sql.Open("testkit_seed_stub", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	base := time.Date(2026, 11, 13, 8, 30, 15, 987654321, time.FixedZone("CST", 8*3600))
	sd := New(t, func(_ testing.TB, _ string) *sql.DB { return db }, WithBaseTime(base), WithStartID(500))

	// 1. 验证命名别名幂等分配与单调递增。
	idAlice1 := sd.ID("user:alice")
	idOrder := sd.ID("order:01")
	idAlice2 := sd.ID(" user:alice ")
	if idAlice1 != 500 || idOrder != 501 || idAlice2 != 500 {
		t.Fatalf("unexpected allocated IDs: alice1=%d order=%d alice2=%d", idAlice1, idOrder, idAlice2)
	}
	if next := sd.NextID(); next != 502 {
		t.Fatalf("NextID = %d, want 502", next)
	}

	// 2. 验证时间统一转为 UTC 且截断至毫秒精度（去除亚毫秒纳秒位）。
	gotTime := sd.Time(2 * time.Hour)
	if gotTime.Location() != time.UTC || gotTime.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatalf("expected UTC millisecond timestamp, got %v (loc=%v)", gotTime, gotTime.Location())
	}

	// 3. 验证封存前正常写库，Freeze 后状态标记为已封存。
	sd.Exec("user", "INSERT INTO demo VALUES (1)")
	if sd.IsFrozen() {
		t.Fatal("session should not be frozen before Freeze()")
	}
	sd.Freeze()
	if !sd.IsFrozen() {
		t.Fatal("session must report IsFrozen() == true after Freeze()")
	}

	// 4. 验证静态 JSON 模板严格加载。
	fixturePath := filepath.Join(t.TempDir(), "sample.json")
	if err := os.WriteFile(fixturePath, []byte(`{"title":"hello","count":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	type sampleFixture struct {
		Title string `json:"title"`
		Count int    `json:"count"`
	}
	loaded := LoadJSON[sampleFixture](t, fixturePath)
	if loaded.Title != "hello" || loaded.Count != 3 {
		t.Fatalf("unexpected loaded JSON: %+v", loaded)
	}
}
