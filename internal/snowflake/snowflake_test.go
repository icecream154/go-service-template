package snowflake

import (
	"errors"
	"math"
	"testing"
	"testing/synctest"
	"time"
)

const (
	testEpochMillis   int64 = 1483228800000
	testLastTimestamp int64 = testEpochMillis + (1 << 41) - 1
)

type initCase struct {
	name         string
	dataCenterID int64
	machineID    int64
	initial      *generator
	wantNodeID   int64
	wantError    error
}

func TestInit(t *testing.T) {
	existing := &generator{
		nodeID: 128, lastTimestamp: testEpochMillis + 9, sequence: 17,
		nowMillis: func() int64 { return testEpochMillis + 9 },
		wait:      func(time.Duration) {},
	}
	tests := []initCase{
		// 使用两个非零编号，验证首次初始化按位组合节点，并发布未发号的实例。
		{name: "publishes configured generator", dataCenterID: 3, machineID: 29, wantNodeID: 413},
		// 数据中心号刚超过上界，验证失败时共享实例仍为空。
		{name: "invalid node leaves generator unpublished", dataCenterID: 8, machineID: 29, wantError: ErrInvalidNode},
		// 已有发过号的实例，再传入合法但不同的节点号，验证拒绝替换和重置。
		{name: "repeated init preserves generator", dataCenterID: 5, machineID: 88, initial: existing, wantError: ErrAlreadyInitialized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setSharedSnowflakeGenerator(t, test.initial)

			if err := Init(test.dataCenterID, test.machineID); !errors.Is(err, test.wantError) {
				t.Fatalf("Init(%d, %d) error = %v, want %v", test.dataCenterID, test.machineID, err, test.wantError)
			}
			shared.RLock()
			got := shared.generator
			shared.RUnlock()
			if test.wantError != nil {
				if got != test.initial {
					t.Fatalf("Init() changed the shared generator on failure, got %p, want %p", got, test.initial)
				}
				if test.initial != nil {
					assertSnowflakeState(t, got, testEpochMillis+9, 17)
				}
				return
			}
			if got == nil {
				t.Fatal("Init() did not publish a generator")
			}
			if got.nodeID != test.wantNodeID {
				t.Errorf("Init() node ID = %d, want %d", got.nodeID, test.wantNodeID)
			}
			assertSnowflakeState(t, got, -1, 0)
		})
	}
}

func TestInitUsesCurrentTimeForNextID(t *testing.T) {
	setSharedSnowflakeGenerator(t, nil)
	synctest.Test(t, func(t *testing.T) {
		// 将虚拟时钟推进到纪元后第 17 毫秒，验证公开入口用当前时间和初始化的节点号发号。
		time.Sleep(time.Until(time.UnixMilli(testEpochMillis + 17)))
		if err := Init(2, 11); err != nil {
			t.Fatalf("Init(2, 11) error = %v", err)
		}
		got, err := NextID()
		assertSnowflakeID(t, got, err, 17<<22|2<<19|11<<12, nil)
	})
}

type nextIDCase struct {
	name      string
	initial   *generator
	wantID    int64
	wantError error
}

func TestNextID(t *testing.T) {
	tests := []nextIDCase{
		// 共享实例为空时调用公开入口，应返回 0 和未初始化错误。
		{name: "not initialized", wantError: ErrNotInitialized},
		// 安装固定时钟的实例，验证公开入口返回共享实例的实际发号结果。
		{
			name:    "uses shared generator",
			initial: newSnowflakeTestGenerator(t, 3, 17, func() int64 { return testEpochMillis + 19 }, func(time.Duration) {}),
			wantID:  19<<22 | 3<<19 | 17<<12,
		},
		// 实例已发布但时钟早于纪元，验证公开入口原样传递发号失败。
		{
			name:      "preserves generator error",
			initial:   newSnowflakeTestGenerator(t, 3, 17, func() int64 { return testEpochMillis - 1 }, func(time.Duration) {}),
			wantError: ErrTimestampOutOfRange,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setSharedSnowflakeGenerator(t, test.initial)

			got, err := NextID()
			assertSnowflakeID(t, got, err, test.wantID, test.wantError)
		})
	}
}

type generatorNodeCase struct {
	name         string
	dataCenterID int64
	machineID    int64
	wantNodeID   int64
	wantError    error
}

func TestNewGeneratorNodeBounds(t *testing.T) {
	tests := []generatorNodeCase{
		// 两个编号都取 0，验证允许使用节点空间的下界。
		{name: "minimum node", dataCenterID: 0, machineID: 0, wantNodeID: 0},
		// 3 位和 7 位均全为 1，验证两项上界可组成最大的 10 位节点号。
		{name: "maximum node", dataCenterID: 7, machineID: 127, wantNodeID: 1023},
		// 数据中心号比下界小 1，应返回可识别的无效节点错误。
		{name: "data center below minimum", dataCenterID: -1, machineID: 5, wantError: ErrInvalidNode},
		// 数据中心号刚超过 3 位上界，应拒绝而不截断到有效编号。
		{name: "data center above maximum", dataCenterID: 8, machineID: 5, wantError: ErrInvalidNode},
		// 其余参数有效时传入负机器号，验证机器号单独检查。
		{name: "machine below minimum", dataCenterID: 5, machineID: -1, wantError: ErrInvalidNode},
		// 机器号超过 7 位上界时应拒绝，避免溢出到数据中心位。
		{name: "machine above maximum", dataCenterID: 5, machineID: 128, wantError: ErrInvalidNode},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newGenerator(test.dataCenterID, test.machineID, func() int64 { return testEpochMillis }, func(time.Duration) {})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("newGenerator(%d, %d) error = %v, want %v", test.dataCenterID, test.machineID, err, test.wantError)
			}
			if test.wantError != nil {
				if got != nil {
					t.Error("newGenerator() returned a generator with an invalid node")
				}
				return
			}
			if got == nil {
				t.Fatal("newGenerator() returned nil for a valid node")
			}
			if got.nodeID != test.wantNodeID {
				t.Errorf("newGenerator() node ID = %d, want %d", got.nodeID, test.wantNodeID)
			}
		})
	}
}

type generatorDependencyCase struct {
	name      string
	nowMillis func() int64
	wait      func(time.Duration)
}

func TestNewGeneratorRequiresClockAndWait(t *testing.T) {
	tests := []generatorDependencyCase{
		// 提供等待函数但省略时钟，验证构造时就拒绝缺失的取时函数。
		{name: "missing clock", wait: func(time.Duration) {}},
		// 提供时钟但省略等待函数，验证构造时就拒绝缺失的等待函数。
		{name: "missing wait", nowMillis: func() int64 { return testEpochMillis }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newGenerator(0, 0, test.nowMillis, test.wait)
			if got != nil || err == nil {
				t.Errorf("newGenerator() = (%p, %v), want nil generator and an error", got, err)
			}
		})
	}
}

type generatorStep struct {
	timestamp int64
	wantID    int64
	wantError error
}

type generatorNextIDCase struct {
	name         string
	dataCenterID int64
	machineID    int64
	steps        []generatorStep
}

func TestGeneratorNextID(t *testing.T) {
	tests := []generatorNextIDCase{
		// 用不同的时间和节点位值验证编码，同时覆盖同毫秒递增和跨毫秒重置。
		{
			name: "bit layout and per-millisecond sequence", dataCenterID: 3, machineID: 42,
			steps: []generatorStep{
				// 首次发号验证 41 位时间、3 位数据中心、7 位机器号和 12 位序列的位置。
				{timestamp: testEpochMillis + 5, wantID: 5<<22 | 3<<19 | 42<<12},
				// 保持时钟不动，验证只递增最低 12 位的序列。
				{timestamp: testEpochMillis + 5, wantID: 5<<22 | 3<<19 | 42<<12 | 1},
				// 前进一毫秒，验证时间更新且新一毫秒从序列 0 开始。
				{timestamp: testEpochMillis + 6, wantID: 6<<22 | 3<<19 | 42<<12},
			},
		},
		// 纪元首毫秒和节点 0 会产生全零编码，验证保留 0 并跳到序列 1。
		{
			name: "reserves zero ID",
			steps: []generatorStep{
				// 时间和节点都为 0，验证第一个有效 ID 为 1。
				{timestamp: testEpochMillis, wantID: 1},
				// 仍在纪元首毫秒时，验证第二次调用在 1 的基础上递增。
				{timestamp: testEpochMillis, wantID: 2},
				// 离开纪元首毫秒后，验证可以正常使用序列 0。
				{timestamp: testEpochMillis + 1, wantID: 1 << 22},
			},
		},
		// 同样在纪元首毫秒，选择非零节点验证只有全零编码才需要跳过。
		{
			name: "nonzero node starts at sequence zero", machineID: 1,
			steps: []generatorStep{
				// 机器号已经使编码为正，因此序列应从 0 开始。
				{timestamp: testEpochMillis, wantID: 1 << 12},
			},
		},
		// 时间差和两个节点编号取上界，验证最高位不会溢出到符号位。
		{
			name: "maximum timestamp and node", dataCenterID: 7, machineID: 127,
			steps: []generatorStep{
				// 除最低 12 位外都为 1，得到最大的整毫秒起始 ID。
				{timestamp: testLastTimestamp, wantID: math.MaxInt64 - 4095},
			},
		},
		// 使用两个相邻越界值和有效值交替调用，验证失败不会推进时间或序列。
		{
			name: "out-of-range clock preserves state", machineID: 1,
			steps: []generatorStep{
				// 初次取时刚好早于纪元，验证下界错误。
				{timestamp: testEpochMillis - 1, wantError: ErrTimestampOutOfRange},
				// 回到纪元后，验证可以正常生成该节点的首个 ID。
				{timestamp: testEpochMillis, wantID: 1 << 12},
				// 时钟刚好超过 41 位上界，验证不会生成溢出的 ID。
				{timestamp: testLastTimestamp + 1, wantError: ErrTimestampOutOfRange},
				// 再回到上次成功时间，验证越界调用没有改变序列。
				{timestamp: testEpochMillis, wantID: 1<<12 | 1},
			},
		},
		// 在有效范围内回拨 1 毫秒，验证可以与越界区分并在时钟恢复时继续发号。
		{
			name: "clock rollback preserves state",
			steps: []generatorStep{
				// 先在纪元后 4 毫秒成功发号，建立上一次的时间。
				{timestamp: testEpochMillis + 4, wantID: 4 << 22},
				// 上一次发号时间减 1 毫秒，验证返回明确的回拨错误。
				{timestamp: testEpochMillis + 3, wantError: ErrClockMovedBackwards},
				// 恢复到上一次成功时间，验证继续递增该毫秒的序列。
				{timestamp: testEpochMillis + 4, wantID: 4<<22 | 1},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var timestamp int64
			g := newSnowflakeTestGenerator(t, test.dataCenterID, test.machineID, func() int64 { return timestamp }, func(time.Duration) {
				t.Fatal("nextID() waited before the sequence was exhausted")
			})

			for _, step := range test.steps {
				timestamp = step.timestamp
				previousTimestamp, previousSequence := g.lastTimestamp, g.sequence
				got, err := g.nextID()
				assertSnowflakeID(t, got, err, step.wantID, step.wantError)
				if step.wantError != nil {
					assertSnowflakeState(t, g, previousTimestamp, previousSequence)
				}
			}
		})
	}
}

type sequenceExhaustionCase struct {
	name         string
	changeAtWait int
	changedTime  int64
	wantWaits    int
	wantID       int64
	wantError    error
}

func TestGeneratorNextIDWhenSequenceIsExhausted(t *testing.T) {
	const startingTime = testEpochMillis + 6
	const firstID = 6<<22 | 2<<19 | 13<<12
	tests := []sequenceExhaustionCase{
		// 在最后一次允许的等待推进时钟，验证上限内仍会尝试取时并重置序列。
		{name: "clock advances on final wait", changeAtWait: 10, changedTime: startingTime + 1, wantWaits: 10, wantID: 7<<22 | 2<<19 | 13<<12},
		// 时钟始终停滞，验证等待达到上限后返回明确错误。
		{name: "clock remains stopped", wantWaits: 10, wantError: ErrSequenceExhausted},
		// 在首次等待时回拨时钟，验证立即返回回拨错误。
		{name: "clock moves backwards while waiting", changeAtWait: 1, changedTime: startingTime - 1, wantWaits: 1, wantError: ErrClockMovedBackwards},
		// 在首次等待时跳到时间差上界之外，验证等待后仍检查时间范围。
		{name: "clock exceeds range while waiting", changeAtWait: 1, changedTime: testLastTimestamp + 1, wantWaits: 1, wantError: ErrTimestampOutOfRange},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			timestamp, waits := startingTime, 0
			g := newSnowflakeTestGenerator(t, 2, 13, func() int64 { return timestamp }, func(delay time.Duration) {
				waits++
				if delay != time.Millisecond {
					t.Errorf("wait %d duration = %s, want 1ms", waits, delay)
				}
				if waits > test.wantWaits {
					t.Fatalf("nextID() waited %d times, want at most %d", waits, test.wantWaits)
				}
				if waits == test.changeAtWait {
					timestamp = test.changedTime
				}
			})

			// 在同一毫秒发满 4096 个 ID，验证耗尽前的每个序列都可使用。
			for sequence := range int64(4096) {
				got, err := g.nextID()
				assertSnowflakeID(t, got, err, firstID|sequence, nil)
			}
			if waits != 0 {
				t.Fatalf("waits before capacity exhausted = %d, want 0", waits)
			}

			got, err := g.nextID()
			assertSnowflakeID(t, got, err, test.wantID, test.wantError)
			if waits != test.wantWaits {
				t.Errorf("waits = %d, want %d", waits, test.wantWaits)
			}
			if test.wantError != nil {
				assertSnowflakeState(t, g, startingTime, 4095)

				// 时钟恢复后应立即生成新毫秒的序列 0。
				timestamp = startingTime + 2
				got, err = g.nextID()
				assertSnowflakeID(t, got, err, 8<<22|2<<19|13<<12, nil)
			}
		})
	}
}

func TestGeneratorNextIDConcurrent(t *testing.T) {
	const workers = 64
	const firstID = 2<<22 | 1<<19 | 9<<12

	// 在固定毫秒同时发起 64 次请求，验证共享发号器只分配一次每个序列。
	g := newSnowflakeTestGenerator(t, 1, 9, func() int64 { return testEpochMillis + 2 }, func(time.Duration) {})
	type result struct {
		id  int64
		err error
	}
	start := make(chan struct{})
	results := make(chan result, workers)
	for range workers {
		go func() {
			<-start
			id, err := g.nextID()
			results <- result{id: id, err: err}
		}()
	}
	close(start)

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	seen := make(map[int64]bool, workers)
	for range workers {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatalf("nextID() error = %v, want nil", got.err)
			}
			if got.id < firstID || got.id >= firstID+workers {
				t.Fatalf("nextID() = %d, want ID in [%d, %d]", got.id, firstID, firstID+workers-1)
			}
			if seen[got.id] {
				t.Fatalf("nextID() returned duplicate ID %d", got.id)
			}
			seen[got.id] = true
		case <-deadline.C:
			t.Fatal("concurrent calls to nextID() did not finish")
		}
	}
}

func newSnowflakeTestGenerator(t *testing.T, dataCenterID, machineID int64, nowMillis func() int64, wait func(time.Duration)) *generator {
	t.Helper()
	g, err := newGenerator(dataCenterID, machineID, nowMillis, wait)
	if err != nil {
		t.Fatalf("newGenerator() error = %v, want nil", err)
	}
	return g
}

func setSharedSnowflakeGenerator(t *testing.T, g *generator) {
	t.Helper()
	shared.Lock()
	original := shared.generator
	shared.generator = g
	shared.Unlock()
	t.Cleanup(func() {
		shared.Lock()
		shared.generator = original
		shared.Unlock()
	})
}

func assertSnowflakeID(t *testing.T, got int64, err error, wantID int64, wantError error) {
	t.Helper()
	if got != wantID || !errors.Is(err, wantError) {
		t.Fatalf("generated (%d, %v), want (%d, %v)", got, err, wantID, wantError)
	}
}

func assertSnowflakeState(t *testing.T, g *generator, wantTimestamp, wantSequence int64) {
	t.Helper()
	if g.lastTimestamp != wantTimestamp || g.sequence != wantSequence {
		t.Errorf("generator state = (timestamp %d, sequence %d), want (%d, %d)", g.lastTimestamp, g.sequence, wantTimestamp, wantSequence)
	}
}
