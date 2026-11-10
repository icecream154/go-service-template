package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"go-service-template/internal/logger"

	"github.com/openai/openai-go/v3"
	"gopkg.in/yaml.v3"
)

// MiniMaxConfig 对应单个环境的 ai/llm/minimax.yml，API Key 按明文原值读取。
// 配置仅通过构造参数注入，不从环境变量补充或覆盖。
type MiniMaxConfig struct {
	BaseURL        string        `yaml:"base_url"`
	APIKey         string        `yaml:"api_key"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// LoadMiniMaxConfig 读取指定 YAML 文件，不选择环境或回退到其他配置。
// 仅接受一个文档及已声明字段，错误不包含配置正文或密钥。
func LoadMiniMaxConfig(path string) (MiniMaxConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MiniMaxConfig{}, fmt.Errorf("read MiniMax configuration: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config MiniMaxConfig
	if err := decoder.Decode(&config); err != nil {
		return MiniMaxConfig{}, errors.New("invalid MiniMax configuration YAML: check field names and scalar types")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return MiniMaxConfig{}, errors.New("MiniMax configuration must contain exactly one YAML document")
	}
	if err := config.validate(); err != nil {
		return MiniMaxConfig{}, err
	}
	return config, nil
}

// validate 检查构造 Client 所需配置，不改写字段或回显敏感值。
func (config MiniMaxConfig) validate() error {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.Opaque != "" {
		return errors.New("MiniMax base_url must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(config.BaseURL, "#") {
		return errors.New("MiniMax base_url must not contain credentials, a query or a fragment")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return errors.New("MiniMax api_key is required")
	}
	if config.ConnectTimeout <= 0 {
		return errors.New("MiniMax connect_timeout must be positive")
	}
	if config.RequestTimeout <= 0 {
		return errors.New("MiniMax request_timeout must be positive")
	}
	return nil
}

// MiniMaxClient 封装 MiniMax OpenAI 兼容接口，构造完成后允许并发复用。
// 私有公共实现持有独立的 SDK 和 Transport，不保存单次请求状态。
type MiniMaxClient struct {
	client *openAICompatibleClient
}

// NewMiniMaxClient 按注入配置创建 SDK Client，不发起远端请求或接入启动单例。
// Client 自有 Transport 由调用方在内部工作结束后调用 CloseIdleConnections 清理。
func NewMiniMaxClient(config MiniMaxConfig) (*MiniMaxClient, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	client, err := newOpenAICompatibleClient(miniMaxProvider, config.BaseURL, config.APIKey, config.ConnectTimeout, config.RequestTimeout)
	if err != nil {
		return nil, err
	}
	return &MiniMaxClient{client: client}, nil
}

// CloseIdleConnections 释放自有连接池的空闲连接，不取消正在执行的请求。
func (c *MiniMaxClient) CloseIdleConnections() {
	if c != nil {
		c.client.closeIdleConnections()
	}
}

const miniMaxProvider = "MiniMax"

// 当前支持的 MiniMax 文本生成模型，由每次调用的 Model 参数指定。
const (
	MiniMaxModelM31Flash = "MiniMax-M3.1-Flash"
	MiniMaxModelM3       = "MiniMax-M3"
)

// GenerateText 使用本次入参指定的模型调用 Chat Completions，不额外重试。
// 传入 Context 的值和更早的取消期限继续生效，request_timeout 限制本次完整操作。
// 不向 MiniMax 发送自定义 TraceId 请求头，不裁剪输入或生成文本。
func (c *MiniMaxClient) GenerateText(ctx context.Context, params GenerateTextParams) (*GenerateTextResult, error) {
	// 1. 从具体 Client 方法识别 operation，派生 Context 保留调用方的 TraceId 和执行控制。
	// nil 不派生为 Background，继续保留原有公共层拒绝无 Context 请求的契约。
	if ctx != nil {
		// 1.1. 日志由公共层输出，但操作身份始终属于当前 MiniMaxClient.GenerateText。
		ctx = logger.WithCurrentOperation(ctx)
	}
	// 2. 安全取得当前实例的底座，未初始化时也交给公共层统一记录失败并返回原有文案。
	var client *openAICompatibleClient
	if c != nil {
		// 2.1. 底座在初始化后只读复用，不在 Client 字段保存请求或日志状态。
		client = c.client
	}
	// 3. 注入已有模型范围和 MiniMax 响应检查，后者在文本提取前执行，结果参与同一次调用日志。
	return client.generateText(ctx, miniMaxProvider, params, validateMiniMaxBaseResponse, MiniMaxModelM31Flash, MiniMaxModelM3)
}

// validateMiniMaxBaseResponse 仅解析 SDK 已接收的 MiniMax 扩展字段。
// 标准 OpenAI 响应可以省略 base_resp；存在时必须提供整数状态码。
func validateMiniMaxBaseResponse(completion *openai.ChatCompletion) error {
	if completion == nil {
		return newInvalidResponseError(miniMaxProvider)
	}
	field, exists := completion.JSON.ExtraFields["base_resp"]
	if !exists {
		return nil
	}
	var baseResponse struct {
		StatusCode *int `json:"status_code"`
	}
	if err := json.Unmarshal([]byte(field.Raw()), &baseResponse); err != nil || baseResponse.StatusCode == nil {
		return newInvalidResponseError(miniMaxProvider)
	}
	if *baseResponse.StatusCode != 0 {
		return &ClientError{Provider: miniMaxProvider, ProviderCode: *baseResponse.StatusCode}
	}
	return nil
}
