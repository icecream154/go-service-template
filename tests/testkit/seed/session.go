// Package seed 提供与具体业务模型解耦的前置测试数据会话管理，包含确定性命名 ID 分配、UTC 毫秒测试时钟、跨库写入路由、前置封存栅栏与静态 JSON 加载。
package seed

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const defaultStartID int64 = 900_000_001

// DBProvider 按逻辑连接名（如 "user"、"order"）返回对应的 *sql.DB 连接池。
type DBProvider func(t testing.TB, connection string) *sql.DB

// Option 自定义前置数据会话的起始时钟或起始发号序列。
type Option func(*Session)

// WithBaseTime 指定会话的基准时间（内部自动转为 UTC 并截断到毫秒精度，对齐 MySQL DATETIME(3)）。
func WithBaseTime(base time.Time) Option {
	return func(s *Session) {
		s.baseTime = base.UTC().Truncate(time.Millisecond)
	}
}

// WithStartID 指定正整数 ID 分配器的起始值（必须 > 0）。
func WithStartID(startID int64) Option {
	return func(s *Session) {
		if startID > 0 {
			s.nextID = startID
		}
	}
}

// Session 管理单条测试流程开始前的存量数据准备状态，并在进入业务步骤后通过 Freeze 阻止违规中途改库。
type Session struct {
	t          testing.TB
	dbProvider DBProvider
	baseTime   time.Time

	mu     sync.Mutex
	nextID int64
	ids    map[string]int64
	frozen bool
}

// New 创建绑定到当前测试与数据库连接提供者的前置数据会话。
// 默认以当前挂钟时间的 UTC 毫秒截断值作为基准时钟，保证相对偏移时间与被测子进程内的实时挂钟保持一致。
func New(t testing.TB, dbProvider DBProvider, opts ...Option) *Session {
	t.Helper()
	if dbProvider == nil {
		t.Fatal("seed.New requires a non-nil DBProvider")
	}
	s := &Session{
		t:          t,
		dbProvider: dbProvider,
		baseTime:   time.Now().UTC().Truncate(time.Millisecond),
		nextID:     defaultStartID,
		ids:        make(map[string]int64),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// ID 按非空逻辑别名返回唯一正整数 ID：首次访问时按序分配并记录，后续访问同一别名始终返回相同 ID。
func (s *Session) ID(alias string) int64 {
	s.t.Helper()
	key := strings.TrimSpace(alias)
	if key == "" {
		s.t.Fatal("seed.Session.ID requires a non-blank alias")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.ids[key]; ok {
		return existing
	}
	allocated := s.nextID
	s.nextID++
	s.ids[key] = allocated
	return allocated
}

// NextID 分配一个无需复用的全新正整数 ID。
func (s *Session) NextID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	allocated := s.nextID
	s.nextID++
	return allocated
}

// BaseTime 返回会话的基准 UTC 毫秒时间。
func (s *Session) BaseTime() time.Time {
	return s.baseTime
}

// Time 返回相对基准时间偏移 offset 后的 UTC 毫秒时间（严格截断至 time.Millisecond）。
func (s *Session) Time(offset time.Duration) time.Time {
	return s.baseTime.Add(offset).UTC().Truncate(time.Millisecond)
}

// Freeze 封存前置数据会话；调用后任何通过本会话向数据库写入数据的尝试都会立即使测试失败，防止伪造业务流程中间状态。
func (s *Session) Freeze() {
	s.mu.Lock()
	s.frozen = true
	s.mu.Unlock()
}

// IsFrozen 返回当前前置会话是否已封存。
func (s *Session) IsFrozen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frozen
}

// Exec 在尚未 Freeze 的前提下向指定逻辑数据库连接执行前置写库 SQL。
func (s *Session) Exec(connection, query string, args ...any) sql.Result {
	s.t.Helper()
	s.mu.Lock()
	frozen := s.frozen
	s.mu.Unlock()
	if frozen {
		s.t.Fatalf("seed.Session is frozen: cannot execute direct SQL write on connection %q after business steps begin (intermediate state must be produced via real HTTP endpoints)", connection)
	}

	db := s.dbProvider(s.t, connection)
	if db == nil {
		s.t.Fatalf("seed.Session received nil *sql.DB for connection %q", connection)
	}
	res, err := db.ExecContext(s.t.Context(), query, args...)
	if err != nil {
		s.t.Fatalf("seed.Session SQL exec on %q failed: %v\nSQL: %s", connection, err, query)
	}
	return res
}

// LoadJSON 从指定文件（如 testdata/*.json）严格读取并解码静态前置数据模板（拒绝未知字段）。
func LoadJSON[T any](t testing.TB, filePath string) T {
	t.Helper()
	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("seed.LoadJSON read %s: %v", filePath, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var target T
	if err := dec.Decode(&target); err != nil {
		t.Fatalf("seed.LoadJSON decode %s: %v", filePath, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		t.Fatalf("seed.LoadJSON found trailing content in %s", filePath)
	}
	return target
}
