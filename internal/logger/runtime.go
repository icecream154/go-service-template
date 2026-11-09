package logger

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"go-service-template/env"
)

// shared 只保存完整构造成功的进程共享 Logger；配置加载失败时保持未初始化状态。
// 读写锁保护初始化发布和读取，不保存任何请求 Context，也不支持运行中替换实例。
var shared struct {
	sync.RWMutex
	logger *Logger
}

// Init 读取当前部署环境的日志配置，生成进程实例标识，并发布共享 Logger。
// configDir 是 logger 配置叶子目录，deployment 复用已有环境配置；输出固定为 stdout。
// 只有成功初始化后才能调用 Current，重复成功初始化返回错误，失败不会发布部分实例。
func Init(configDir string, deployment env.Config) error {
	// 1. 串行执行构造和发布，防止并发初始化生成多个进程标识或替换已经使用的 Logger。
	// 锁只覆盖启动装配过程，日志写入仍由 Logger 自己保护，不依赖这把共享实例锁。
	shared.Lock()
	defer shared.Unlock()
	if shared.logger != nil {
		// 1.1. 已成功发布的实例保持不变，调用方应修正启动顺序，而不是重新加载配置。
		return errors.New("logger is already initialized")
	}

	// 2. 读取唯一的环境配置文件，配置内容只提供服务名称和最低日志级别。
	// 读取或 YAML 解析失败时直接返回，进程共享位置仍保持 nil。
	config, err := loadConfiguration(configDir, deployment.Environment)
	if err != nil {
		return err
	}

	// 3. 为本次初始化生成独立的 16 字节随机标识，编码为 32 位小写十六进制字符串。
	// 它表示当前进程实例，不复用请求 TraceId，也不依赖尚未初始化的雪花发号器。
	var instanceBytes [16]byte
	if _, err := rand.Read(instanceBytes[:]); err != nil {
		// 3.1. 随机源失败时保留原始原因并停止装配，不用时间或固定值伪造实例身份。
		return fmt.Errorf("generate logger instance ID: %w", err)
	}

	// 4. 通过已有构造方法完成日志配置语义校验、编码器及输出器的创建。
	// 匿名包装只暴露 io.Writer 的 Write：stdout 没有本包维护的缓冲，无须调用文件 fsync；
	// 终端或管道通常不支持 os.File.Sync，故不把它们的 EINVAL 当成正常停机失败。
	// New 的独立使用方仍可直接传入支持 Sync 的文件，本处不改变该构造接口的同步语义。
	instance, err := New(Config{
		ServiceName: config.ServiceName,
		InstanceID:  hex.EncodeToString(instanceBytes[:]),
		MinLevel:    config.MinLevel,
	}, deployment, struct{ io.Writer }{os.Stdout})
	if err != nil {
		// 4.1. 构造失败不发布实例；stdout 的所有权仍属于进程，本包没有需要关闭的输出资源。
		return err
	}

	// 5. 仅在全部步骤成功后一次性发布指针，后续 Current 取得同一个可并发复用的实例。
	// 本方法不记录业务日志、不注册全局标准库 Logger，也不在后台启动协程。
	shared.logger = instance
	return nil
}

// Current 返回启动时发布的共享 Logger；未初始化时的调用属于装配顺序错误。
// 返回值复用同一实例，其配置和输出器均为私有字段，不向调用方暴露可变请求状态。
func Current() *Logger {
	// 1. 在读锁保护下取得共享实例，避免读取到并发初始化尚未完成的发布状态。
	shared.RLock()
	defer shared.RUnlock()
	if shared.logger == nil {
		// 1.1. 调用方必须先完成 Init；不隐式选择配置、生成标识或创建默认 Logger。
		panic("logger is not initialized; call Init before Current")
	}

	// 2. 返回完整构造的进程共享实例，实际日志写入不再持有此处的共享读锁。
	return shared.logger
}

// Sync 同步已经初始化的共享 Logger，未初始化时可安全调用并返回 nil。
// 只转交同步操作，不清空共享指针、不关闭 stdout，也不表示所有业务或采集平台已完成处理。
func Sync() error {
	// 1. 在短暂读锁下复制共享指针，然后释放锁，避免输出器同步期间阻塞共享实例访问。
	shared.RLock()
	instance := shared.logger
	shared.RUnlock()

	// 2. 参数解析、环境加载或日志配置加载可能在发布实例前失败；这类退出路径无日志实例需要同步。
	if instance == nil {
		return nil
	}

	// 3. 将实例同步结果原样交给生命周期持有方，保留已有错误链，不递归使用故障 Logger 记录自身错误。
	// Init 选择的 stdout 包装没有自有缓冲，因此该路径不会对终端或管道发起文件 fsync。
	return instance.Sync()
}
