package sdkmock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是运行在被测子进程内的轻量协议客户端，将第三方 SDK 调用委托给父进程的 Controller。
type Client struct {
	invokeURL  string
	httpClient *http.Client
}

// NewClient 校验控制面地址并创建无状态跨进程 Mock 客户端。
func NewClient(controllerURL string) (*Client, error) {
	trimmed := strings.TrimSpace(controllerURL)
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("sdkmock controller URL must be a valid HTTP or HTTPS URL")
	}
	transport := &http.Transport{
		DisableKeepAlives: false,
		MaxIdleConns:      16,
		IdleConnTimeout:   30 * time.Second,
	}
	return &Client{
		invokeURL: strings.TrimRight(trimmed, "/") + "/invoke",
		httpClient: &http.Client{
			Transport: transport,
		},
	}, nil
}

// CloseIdleConnections 释放底层 HTTP 连接池中的空闲连接。
func (c *Client) CloseIdleConnections() {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// Invoke 向父进程 Controller 发送一次 (provider, operation) 调用。
// 若 Controller 配置了成功结果则解码到 resultOut；若配置了故障或未预期调用则返回 *CallError。
func (c *Client) Invoke(ctx context.Context, provider, operation string, params any, resultOut any) error {
	if c == nil {
		return errors.New("sdkmock client is not initialized")
	}
	if ctx == nil {
		return errors.New("sdkmock invocation requires a context")
	}

	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal sdkmock params: %w", err)
	}
	reqBytes, err := json.Marshal(InvokeRequest{
		Provider:  provider,
		Operation: operation,
		Params:    paramsBytes,
	})
	if err != nil {
		return fmt.Errorf("marshal sdkmock request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.invokeURL, bytes.NewReader(reqBytes))
	if err != nil {
		return fmt.Errorf("build sdkmock HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send sdkmock HTTP request: %w", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, maxInvokeBodyBytes))
	if err != nil {
		return fmt.Errorf("read sdkmock HTTP response: %w", err)
	}

	var invokeResp InvokeResponse
	if err := json.Unmarshal(respBytes, &invokeResp); err != nil {
		return fmt.Errorf("decode sdkmock response (status %d): %w", httpResp.StatusCode, err)
	}
	if invokeResp.Error != nil {
		return &CallError{Fault: *invokeResp.Error}
	}
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected sdkmock status %d", httpResp.StatusCode)
	}
	if resultOut != nil && len(invokeResp.Result) > 0 {
		if err := json.Unmarshal(invokeResp.Result, resultOut); err != nil {
			return fmt.Errorf("decode sdkmock result payload: %w", err)
		}
	}
	return nil
}
