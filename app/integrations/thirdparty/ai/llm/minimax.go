package llm

import (
	"bytes"
	"context"
	"encoding/json"
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
// SDK、配置及 Transport 均为私有字段，不保存单次请求状态。
type MiniMaxClient struct {
	sdk            openai.ChatCompletionService
	requestTimeout time.Duration
	transport      *http.Transport
}

// NewMiniMaxClient 按注入配置创建 SDK Client，不发起远端请求或接入启动单例。
// Client 自有 Transport 由调用方在内部工作结束后调用 CloseIdleConnections 清理。
func NewMiniMaxClient(config MiniMaxConfig) (*MiniMaxClient, error) {
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
	return &MiniMaxClient{
		sdk:            sdk,
		requestTimeout: config.RequestTimeout,
		transport:      transport,
	}, nil
}

// CloseIdleConnections 释放自有连接池的空闲连接，不取消正在执行的请求。
func (c *MiniMaxClient) CloseIdleConnections() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// MiniMaxClientError 表达 SDK 或供应商调用故障，公开文案不包含远端正文、请求内容或密钥。
// StatusCode 保存 SDK HTTP 错误状态；ProviderCode 保存 base_resp 的非零状态码。
// 这些状态仅供上层分类，不直接决定对外状态。
type MiniMaxClientError struct {
	StatusCode   int
	ProviderCode int
	cause        error
}

func (err *MiniMaxClientError) Error() string {
	return "MiniMax text generation request failed"
}

// Unwrap 保留原始原因，供 errors.Is 识别执行取消、超时等情况。
func (err *MiniMaxClientError) Unwrap() error {
	return err.cause
}

// ErrMiniMaxInvalidResponse 表示远端未提供合法的供应商状态或候选文本。
var ErrMiniMaxInvalidResponse = errors.New("MiniMax text generation response must contain a valid status and text choice")

// 当前支持的 MiniMax 文本生成模型，由每次调用的 Model 参数指定。
const (
	MiniMaxModelM31Flash = "MiniMax-M3.1-Flash"
	MiniMaxModelM3       = "MiniMax-M3"
)

// MiniMaxGenerateTextParams 表示单轮、非流式文本生成的输入，Prompt 原样交给模型。
type MiniMaxGenerateTextParams struct {
	// Model 每次调用必填，仅允许上述模型常量；Client 不补充默认值。
	Model  string
	Prompt string
}

// MiniMaxGenerateTextResult 只返回生成文本，不向业务层暴露 SDK 响应类型。
type MiniMaxGenerateTextResult struct {
	Content string
}

// validate 校验本次调用的模型，不改写参数或补充默认值。
func (params MiniMaxGenerateTextParams) validate() error {
	if strings.TrimSpace(params.Model) == "" {
		return errors.New("MiniMax model is required")
	}
	switch params.Model {
	case MiniMaxModelM31Flash, MiniMaxModelM3:
		return nil
	default:
		return errors.New("MiniMax text generation model is not supported")
	}
}

// GenerateText 使用本次入参指定的模型调用 Chat Completions，不额外重试。
// 传入 Context 的值和更早的取消期限继续生效，request_timeout 限制本次完整操作。
// 不向 MiniMax 发送自定义 TraceId 请求头，不裁剪输入或生成文本。
func (c *MiniMaxClient) GenerateText(ctx context.Context, params MiniMaxGenerateTextParams) (*MiniMaxGenerateTextResult, error) {
	if c == nil || c.transport == nil {
		return nil, errors.New("MiniMax client is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("MiniMax request context is required")
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
		failure := &MiniMaxClientError{cause: err}
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			failure.StatusCode = apiError.StatusCode
		}
		return nil, failure
	}
	if completion == nil {
		return nil, ErrMiniMaxInvalidResponse
	}
	if err := validateMiniMaxBaseResponse(completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 {
		return nil, ErrMiniMaxInvalidResponse
	}
	message := completion.Choices[0].Message
	if !message.JSON.Content.Valid() {
		return nil, ErrMiniMaxInvalidResponse
	}
	return &MiniMaxGenerateTextResult{Content: message.Content}, nil
}

// validateMiniMaxBaseResponse 仅解析 SDK 已接收的 MiniMax 扩展字段。
// 标准 OpenAI 响应可以省略 base_resp；存在时必须提供整数状态码。
func validateMiniMaxBaseResponse(completion *openai.ChatCompletion) error {
	field, exists := completion.JSON.ExtraFields["base_resp"]
	if !exists {
		return nil
	}
	var baseResponse struct {
		StatusCode *int `json:"status_code"`
	}
	if err := json.Unmarshal([]byte(field.Raw()), &baseResponse); err != nil || baseResponse.StatusCode == nil {
		return ErrMiniMaxInvalidResponse
	}
	if *baseResponse.StatusCode != 0 {
		return &MiniMaxClientError{ProviderCode: *baseResponse.StatusCode}
	}
	return nil
}
