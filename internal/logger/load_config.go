package logger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go-service-template/env"

	"gopkg.in/yaml.v3"
)

// configuration 只描述日志 YAML 中可配置的字段，不重复声明部署环境或进程实例标识。
// 部署环境由 env.Config 提供，实例标识由 Init 为当前进程生成。
type configuration struct {
	// ServiceName 是部署配置明确指定的服务名称，原值交给 New 校验和保存。
	ServiceName string `yaml:"service_name"`
	// MinLevel 使用 Level 的固定字符串取值，语义校验统一交给 New，加载器不补默认级别。
	MinLevel Level `yaml:"min_level"`
	// FilePath 可选。配置时将 JSON 日志同时追加到该文件，相对路径以进程工作目录为基准。
	FilePath string `yaml:"file_path"`
}

// loadConfiguration 读取指定环境的日志配置文件，不改变共享实例或展开环境变量。
// configDir 必须直接包含 dev.yml、test.yml、prod.yml；只读取所选文件，不回退其他环境。
func loadConfiguration(configDir, environment string) (configuration, error) {
	// 1. 只接受 env 包已声明的部署环境，先检查再拼接文件名，防止任意输入改变读取范围。
	// 这里选择文件，不重新读取 APP_ENV，也不转换大小写或去掉环境名称两侧空白。
	switch environment {
	case env.DevEnv, env.TestEnv, env.ProdEnv:
		// 1.1. 合法环境继续使用同名 YAML 文件，新增环境时须同步明确配置文件选择规则。
	default:
		// 1.2. 未支持的环境直接报告装配错误，不按默认开发环境继续启动。
		return configuration{}, errors.New("unsupported logger deployment environment")
	}

	// 2. 确认调用方提供了日志配置叶子目录；TrimSpace 只判断空白，不修改实际目录值。
	if strings.TrimSpace(configDir) == "" {
		return configuration{}, errors.New("logger configuration directory is required")
	}
	// 2.1. 将唯一的目标文件解析为绝对路径，便于读取失败时定位部署文件。
	filePath, err := filepath.Abs(filepath.Join(configDir, environment+".yml"))
	if err != nil {
		return configuration{}, errors.New("cannot resolve logger configuration path")
	}

	// 3. 一次性读取所选文件；文件不存在或无权限时保留底层原因，不切换到其他环境文件。
	data, err := os.ReadFile(filePath)
	if err != nil {
		return configuration{}, fmt.Errorf("cannot read logger configuration file %q: %w", filePath, err)
	}

	// 4. 使用已知字段模式解析第一份 YAML 文档，拒绝拼错字段、未知字段和无效标量类型。
	// 解码错误只返回固定文案，不把配置正文或 YAML 库可能附带的原值写入错误信息。
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config configuration
	if err := decoder.Decode(&config); err != nil {
		return configuration{}, errors.New("invalid logger configuration YAML: check field names and scalar types")
	}

	// 5. 首份文档之后只允许 EOF；即使第二份文档为空，也不能形成多份配置隐式合并。
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return configuration{}, errors.New("logger configuration must contain exactly one YAML document")
	}

	// 6. 返回文件字段原值；Init 补充实例标识后调用 New，统一完成服务名和日志级别的语义校验。
	// 加载器不填充实例标识、默认级别或环境变量，失败配置也不会在这里发布为共享 Logger。
	return config, nil
}
