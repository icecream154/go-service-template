package logger

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go-service-template/env"
)

// shared 只保存完整构造成功的进程共享 Logger；配置加载失败时保持未初始化状态。
// 读写锁保护初始化发布和读取，不保存任何请求 Context，也不支持运行中替换实例。
var shared struct {
	sync.RWMutex
	logger *Logger
	file   *os.File
}

// Init 读取当前部署环境的日志配置，生成进程实例标识，并发布共享 Logger。
// configDir 是 logger 配置叶子目录。输出使用 stdout，配置 file_path 时也追加到日志文件。
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

	// 2. 读取唯一的环境配置文件，取得服务名称、最低日志级别和可选文件路径。
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

	// 4. stdout 只暴露 Write，以便退出时只同步 Logger 自行打开的日志文件。
	var output io.Writer = struct{ io.Writer }{os.Stdout}
	var file *os.File
	if config.FilePath != "" {
		if strings.TrimSpace(config.FilePath) == "" {
			return errors.New("logger file_path must not be whitespace only")
		}
		if err := os.MkdirAll(filepath.Dir(config.FilePath), 0755); err != nil {
			return fmt.Errorf("create logger file directory: %w", err)
		}
		file, err = os.OpenFile(config.FilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open logger file: %w", err)
		}
		output = fileOutput{Writer: io.MultiWriter(os.Stdout, file), file: file}
	}

	// 5. 校验日志配置并构造编码器。失败时关闭已经打开的文件。
	instance, err := New(Config{
		ServiceName: config.ServiceName,
		InstanceID:  hex.EncodeToString(instanceBytes[:]),
		MinLevel:    config.MinLevel,
	}, deployment, output)
	if err != nil {
		if file != nil {
			return errors.Join(err, file.Close())
		}
		return err
	}

	// 6. 全部步骤成功后发布共享实例和它拥有的文件，后续 Current 取得同一 Logger。
	// 本方法不记录业务日志、不注册全局标准库 Logger，也不在后台启动协程。
	shared.logger = instance
	shared.file = file
	return nil
}

// fileOutput 将同一条 JSON 写到 stdout 和文件。Sync 只同步文件。
type fileOutput struct {
	io.Writer
	file *os.File
}

func (w fileOutput) Sync() error {
	return w.file.Sync()
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

// Sync 同步共享 Logger 拥有的日志文件。没有打开的文件时返回 nil。
// Sync 只同步文件，文件由 Shutdown 关闭。
func Sync() error {
	// 持有读锁直到同步结束，使 Shutdown 在同步后关闭文件。
	shared.RLock()
	defer shared.RUnlock()
	if shared.file == nil {
		return nil
	}
	return shared.logger.Sync()
}

// Shutdown 在业务工作结束后同步并关闭共享 Logger 拥有的文件，可以重复调用。
// stdout 的生命周期仍由进程管理。
func Shutdown() error {
	shared.Lock()
	defer shared.Unlock()
	if shared.file == nil {
		return nil
	}
	file := shared.file
	shared.file = nil
	syncErr := shared.logger.Sync()
	if err := file.Close(); err != nil {
		return errors.Join(syncErr, fmt.Errorf("close JSON log file: %w", err))
	}
	return syncErr
}
