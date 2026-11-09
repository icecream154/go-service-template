// Package base 定义公司内部 HTTP Client 的公共结构。
package base

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Config 保存注入单个 Client 的配置；连接超时由目标服务共享的 Transport 配置。
type Config struct {
	BaseURL        string
	RequestTimeout time.Duration
}

// Client 保存目标服务地址及 HTTP 客户端，供具体 controller 的 Client 组合使用。
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New 校验配置并构造 Client，不发起网络请求。
// transport 由调用方创建并按目标服务共享，本函数不修改它或接管其生命周期。
func New(config Config, transport *http.Transport) (*Client, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if transport == nil {
		return nil, errors.New("shared HTTP transport is required")
	}
	return &Client{
		baseURL: config.BaseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.RequestTimeout,
		},
	}, nil
}

func (config Config) validate() error {
	if !utf8.ValidString(config.BaseURL) || strings.TrimSpace(config.BaseURL) != config.BaseURL {
		return errors.New("base_url must be valid UTF-8 without surrounding whitespace")
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.Opaque != "" {
		return errors.New("base_url must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(config.BaseURL, "#") {
		return errors.New("base_url must not contain credentials, a query or a fragment")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return errors.New("base_url port must be between 1 and 65535")
		}
	}
	if config.RequestTimeout <= 0 {
		return errors.New("request_timeout must be positive")
	}
	return nil
}
