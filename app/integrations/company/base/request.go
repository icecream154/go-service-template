package base

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

	"go-service-template/internal/traceid"
)

// GetJSON 向目标服务发送 GET 请求，将 HTTP 200 的 JSON 正文解码到 result。
// query 是可选的查询参数，result 必须是非 nil 指针；本方法不校验业务字段或额外重试。
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, result any) error {
	return c.requestJSON(ctx, http.MethodGet, path, query, nil, result)
}

// PostJSON 向目标服务发送 JSON POST 请求，将 HTTP 200 的 JSON 正文解码到 result。
// path 是服务内的接口路径，result 必须是非 nil 指针；本方法不校验业务字段或额外重试。
func (c *Client) PostJSON(ctx context.Context, path string, params, result any) error {
	return c.requestJSON(ctx, http.MethodPost, path, nil, params, result)
}

func (c *Client) requestJSON(ctx context.Context, method, path string, query url.Values, params, result any) error {
	if c == nil || c.httpClient == nil {
		return errors.New("company HTTP client is not initialized")
	}
	if ctx == nil {
		return errors.New("HTTP request context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var body io.Reader
	if method == http.MethodPost {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode JSON request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/" + strings.TrimLeft(path, "/")
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create HTTP request: %w", err)
	}
	if len(query) > 0 {
		values := request.URL.Query()
		for key, entries := range query {
			for _, value := range entries {
				values.Add(key, value)
			}
		}
		request.URL.RawQuery = values.Encode()
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	// 统一透传调用方的链路标识；没有请求上下文的调用不在这里另行生成。
	if id := traceid.FromContext(ctx); id != "" {
		request.Header.Set(traceid.HeaderName, id)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send HTTP request: %w", err)
	}
	defer response.Body.Close()

	// JSON 响应读取上限为 16 MiB（16,777,216 字节），可根据业务实际情况调整。
	const maxResponseBytes = 16 << 20
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read HTTP response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return errors.New("JSON response exceeds 16 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request returned status %d", response.StatusCode)
	}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return fmt.Errorf("decode JSON response: %w", err)
	}
	return nil
}
