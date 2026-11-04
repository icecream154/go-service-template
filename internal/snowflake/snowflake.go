// Package snowflake 提供进程共享的雪花 ID 生成器。
// 编码沿用 tennis：2017 UTC 纪元、41 位毫秒时间差、3 位数据中心号、
// 7 位机器号和 12 位毫秒内序列。所有业务共享同一个并发安全实例。
// 节点分配以及进程重启后的节点复用、历史时钟高水位由部署方保障。
package snowflake

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	epochMillis    int64 = 1483228800000 // 2017-01-01 00:00:00 UTC。
	dataCenterBits       = 3
	machineBits          = 7
	sequenceBits         = 12
	timestampShift       = dataCenterBits + machineBits + sequenceBits
	maxTimestamp   int64 = (1 << 41) - 1
	maxSequence    int64 = (1 << sequenceBits) - 1
	// 时钟停滞时最多等待 10 次，每次 1 毫秒；不无限占用调用方。
	maxWaitAttempts = 10
)

var (
	ErrInvalidNode         = errors.New("snowflake: invalid data center or machine ID")
	ErrNotInitialized      = errors.New("snowflake: generator is not initialized")
	ErrAlreadyInitialized  = errors.New("snowflake: generator is already initialized")
	ErrClockMovedBackwards = errors.New("snowflake: clock moved backwards")
	ErrTimestampOutOfRange = errors.New("snowflake: timestamp is outside the supported range")
	ErrSequenceExhausted   = errors.New("snowflake: sequence exhausted while the clock did not advance")
)

var shared struct {
	sync.RWMutex
	generator *generator
}

// Init 在启动阶段创建并发布唯一实例。重复调用返回错误，绝不重置序列或切换节点。
// dataCenterID 范围为 0～7，machineID 范围为 0～127，不提供默认节点号。
func Init(dataCenterID, machineID int64) error {
	next, err := newGenerator(dataCenterID, machineID, func() int64 {
		return time.Now().UnixMilli()
	}, time.Sleep)
	if err != nil {
		return err
	}
	shared.Lock()
	defer shared.Unlock()
	if shared.generator != nil {
		return ErrAlreadyInitialized
	}
	shared.generator = next
	return nil
}

// NextID 返回正 int64。未初始化、时钟异常或容量耗尽时返回 0 和明确错误；
// 错误返回的 0 不是可用标识，调用方必须检查 error 并中止本次创建。
func NextID() (int64, error) {
	shared.RLock()
	current := shared.generator
	shared.RUnlock()
	if current == nil {
		return 0, ErrNotInitialized
	}
	return current.nextID()
}

// generator 的时间、序列只在成功发号时更新；取时和等待函数供确定性测试替换。
type generator struct {
	mu            sync.Mutex
	nodeID        int64
	lastTimestamp int64
	sequence      int64
	nowMillis     func() int64
	wait          func(time.Duration)
}

func newGenerator(dataCenterID, machineID int64, nowMillis func() int64, wait func(time.Duration)) (*generator, error) {
	if dataCenterID < 0 || dataCenterID >= 1<<dataCenterBits {
		return nil, fmt.Errorf("%w: data center ID must be between 0 and 7", ErrInvalidNode)
	}
	if machineID < 0 || machineID >= 1<<machineBits {
		return nil, fmt.Errorf("%w: machine ID must be between 0 and 127", ErrInvalidNode)
	}
	if nowMillis == nil || wait == nil {
		return nil, errors.New("snowflake: clock and wait functions are required")
	}
	return &generator{
		nodeID: dataCenterID<<machineBits | machineID, lastTimestamp: -1,
		nowMillis: nowMillis, wait: wait,
	}, nil
}

func (g *generator) nextID() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	timestamp, err := g.readTimestamp()
	if err != nil {
		return 0, err
	}
	var sequence int64
	if timestamp == g.lastTimestamp {
		if g.sequence < maxSequence {
			sequence = g.sequence + 1
		} else {
			for attempt := 0; timestamp == g.lastTimestamp; attempt++ {
				if attempt == maxWaitAttempts {
					return 0, ErrSequenceExhausted
				}
				g.wait(time.Millisecond)
				timestamp, err = g.readTimestamp()
				if err != nil {
					return 0, err
				}
			}
		}
	}
	id := (timestamp-epochMillis)<<timestampShift | g.nodeID<<sequenceBits | sequence
	if id == 0 {
		// 全零编码保留不用：纪元首毫秒、节点 0 的首个序列从 1 开始。
		sequence = 1
		id = 1
	}
	g.lastTimestamp, g.sequence = timestamp, sequence
	return id, nil
}

// readTimestamp 只校验，不推进发号状态；调用方持有 g.mu。
func (g *generator) readTimestamp() (int64, error) {
	timestamp := g.nowMillis()
	if timestamp < epochMillis || timestamp-epochMillis > maxTimestamp {
		return 0, ErrTimestampOutOfRange
	}
	if timestamp < g.lastTimestamp {
		return 0, ErrClockMovedBackwards
	}
	return timestamp, nil
}
