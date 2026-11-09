package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
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
// SDK、配置及 Transport 均为私有字段，不保存单次请求状态。
type QwenClient struct {
	sdk            openai.ChatCompletionService
	requestTimeout time.Duration
	transport      *http.Transport
}

// NewQwenClient 按注入配置创建 SDK Client，不发起远端请求或接入启动单例。
// Client 自有 Transport 由调用方在内部工作结束后调用 CloseIdleConnections 清理。
func NewQwenClient(config QwenConfig) (*QwenClient, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || defaultTransport == nil {
		return nil, errors.New("default HTTP transport must be an *http.Transport")
	}
	transport := defaultTransport.Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   config.ConnectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   config.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// 仅构造所需的 SDK 接口，避免继承进程中的 OPENAI_* 环境配置及自定义头。
	sdk := openai.NewChatCompletionService(
		option.WithAPIKey(config.APIKey),
		option.WithBaseURL(config.BaseURL),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	return &QwenClient{
		sdk:            sdk,
		requestTimeout: config.RequestTimeout, transport: transport,
	}, nil
}

// CloseIdleConnections 释放自有连接池的空闲连接，不取消正在执行的请求。
func (c *QwenClient) CloseIdleConnections() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// QwenClientError 表达 SDK 调用故障，公开文案不包含供应商正文、请求内容或密钥。
// StatusCode 在收到 SDK HTTP 错误时保存远端状态，仅供上层分类，不直接决定对外状态。
type QwenClientError struct {
	StatusCode int
	cause      error
}

func (err *QwenClientError) Error() string {
	return "Qwen text generation request failed"
}

// Unwrap 保留原始原因，供 errors.Is 识别执行取消、超时等情况。
func (err *QwenClientError) Unwrap() error {
	return err.cause
}

// ErrQwenInvalidResponse 表示远端未提供当前单轮文本接口所需的候选文本。
var ErrQwenInvalidResponse = errors.New("Qwen text generation response must contain a text choice")

// 当前支持的 Qwen 文本生成模型，由每次调用的 Model 参数指定。
const (
	QwenModel38Max   = "qwen3.8-max"
	QwenModel37Plus  = "qwen3.7-plus"
	QwenModel38Flash = "qwen3.8-flash"
)

// QwenGenerateTextParams 表示单轮、非流式文本生成的输入，Prompt 原样交给模型。
type QwenGenerateTextParams struct {
	// Model 每次调用必填，仅允许上述模型常量；Client 不补充默认值。
	Model  string
	Prompt string
}

// QwenGenerateTextResult 只返回生成文本，不向业务层暴露 SDK 响应类型。
type QwenGenerateTextResult struct {
	Content string
}

// validate 校验本次调用的模型，不改写参数或补充默认值。
func (params QwenGenerateTextParams) validate() error {
	if strings.TrimSpace(params.Model) == "" {
		return errors.New("Qwen model is required")
	}
	switch params.Model {
	case QwenModel38Max, QwenModel37Plus, QwenModel38Flash:
		return nil
	default:
		return errors.New("Qwen text generation model is not supported")
	}
}

// GenerateText 使用本次入参指定的模型调用 Chat Completions，不额外重试。
// 传入 Context 的值和更早的取消期限继续生效，request_timeout 限制本次完整操作。
// 不向阿里云发送自定义 TraceId 请求头，不裁剪输入或生成文本。
func (c *QwenClient) GenerateText(ctx context.Context, params QwenGenerateTextParams) (*QwenGenerateTextResult, error) {
	if c == nil || c.transport == nil {
		return nil, errors.New("Qwen client is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("Qwen request context is required")
	}
	if err := params.validate(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	completion, err := c.sdk.New(callCtx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(params.Prompt)},
		Model:    openai.ChatModel(params.Model),
	})
	if err != nil {
		failure := &QwenClientError{cause: err}
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			failure.StatusCode = apiError.StatusCode
		}
		return nil, failure
	}
	if completion == nil || len(completion.Choices) == 0 {
		return nil, ErrQwenInvalidResponse
	}
	message := completion.Choices[0].Message
	if !message.JSON.Content.Valid() {
		return nil, ErrQwenInvalidResponse
	}
	return &QwenGenerateTextResult{Content: message.Content}, nil
}
