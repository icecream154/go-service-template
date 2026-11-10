package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"go-service-template/internal/logger"

	"gopkg.in/yaml.v3"
)

// QwenConfig 对应单个环境的 ai/llm/qwen.yml，API Key 按明文原值读取。
// 配置仅通过构造参数注入，不从环境变量补充或覆盖。
type QwenConfig struct {
	BaseURL        string        `yaml:"base_url"`
	APIKey         string        `yaml:"api_key"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// LoadQwenConfig 读取指定 YAML 文件，不选择环境或回退到其他配置。
// 仅接受一个文档及已声明字段，错误不包含配置正文或密钥。
func LoadQwenConfig(path string) (QwenConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return QwenConfig{}, fmt.Errorf("read Qwen configuration: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config QwenConfig
	if err := decoder.Decode(&config); err != nil {
		return QwenConfig{}, errors.New("invalid Qwen configuration YAML: check field names and scalar types")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return QwenConfig{}, errors.New("Qwen configuration must contain exactly one YAML document")
	}
	if err := config.validate(); err != nil {
		return QwenConfig{}, err
	}
	return config, nil
}

// validate 检查构造 Client 所需配置，不改写字段或回显敏感值。
func (config QwenConfig) validate() error {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.Opaque != "" {
		return errors.New("Qwen base_url must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(config.BaseURL, "#") {
		return errors.New("Qwen base_url must not contain credentials, a query or a fragment")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return errors.New("Qwen api_key is required")
	}
	if config.ConnectTimeout <= 0 {
		return errors.New("Qwen connect_timeout must be positive")
	}
	if config.RequestTimeout <= 0 {
		return errors.New("Qwen request_timeout must be positive")
	}
	return nil
}

// QwenClient 封装阿里云 OpenAI 兼容接口，构造完成后允许并发复用。
// 私有公共实现持有独立的 SDK 和 Transport，不保存单次请求状态。
type QwenClient struct {
	client *openAICompatibleClient
}

// NewQwenClient 按注入配置创建 SDK Client，不发起远端请求或接入启动单例。
// Client 自有 Transport 由调用方在内部工作结束后调用 CloseIdleConnections 清理。
func NewQwenClient(config QwenConfig) (*QwenClient, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	client, err := newOpenAICompatibleClient(qwenProvider, config.BaseURL, config.APIKey, config.ConnectTimeout, config.RequestTimeout)
	if err != nil {
		return nil, err
	}
	return &QwenClient{client: client}, nil
}

// CloseIdleConnections 释放自有连接池的空闲连接，不取消正在执行的请求。
func (c *QwenClient) CloseIdleConnections() {
	if c != nil {
		c.client.closeIdleConnections()
	}
}

const qwenProvider = "Qwen"

// 当前支持的 Qwen 文本生成模型，由每次调用的 Model 参数指定。
const (
	QwenModel38Max   = "qwen3.8-max"
	QwenModel37Plus  = "qwen3.7-plus"
	QwenModel38Flash = "qwen3.8-flash"
)

// GenerateText 使用本次入参指定的模型调用 Chat Completions，不额外重试。
// 传入 Context 的值和更早的取消期限继续生效，request_timeout 限制本次完整操作。
// 不向阿里云发送自定义 TraceId 请求头，不裁剪输入或生成文本。
func (c *QwenClient) GenerateText(ctx context.Context, params GenerateTextParams) (*GenerateTextResult, error) {
	// 1. 在具体 Client 方法入口自动建立操作作用域，保留调用方的 TraceId、取消和截止时间。
	// nil 仍交给公共调用流程报错，不通过入口封装将其改为 Background。
	if ctx != nil {
		// 1.1. 公共层直接沿用该 Context，不把底座方法名或供应商 SDK 方法名当成 operation。
		ctx = logger.WithCurrentOperation(ctx)
	}
	// 2. 安全取得底座，零值或 nil Client 同样进入公共流程，以便记录调用开始及初始化失败。
	var client *openAICompatibleClient
	if c != nil {
		// 2.1. 只读取构造时保存的底座，不将单次 Context、结果或日志状态写回共享实例。
		client = c.client
	}
	// 3. 由公共层统一校验、调用 SDK、提取文本及记录日志；Qwen 使用已有模型范围，无额外响应钩子。
	return client.generateText(ctx, qwenProvider, params, nil, QwenModel38Max, QwenModel37Plus, QwenModel38Flash)
}
