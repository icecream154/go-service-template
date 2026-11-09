package image

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

	"go-service-template/app/integrations/company/base"
)

// ImageGenClient 组合公共 Client，对应目标服务的图片生成 controller。
type ImageGenClient struct {
	client *base.Client
	// base.Client 尚无请求执行入口，本文件的调用示例暂存这两个字段，继续复用注入的 Transport。
	baseURL    string
	httpClient *http.Client
}

// GenerateImageParams 是图片生成接口的模拟入参，后续按真实远端契约调整。
// Width、Height 为 0 时，示例接口分别使用 1024。
type GenerateImageParams struct {
	Prompt string `json:"prompt"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// GenerateImageResult 是示例接口的出参，字段以后续真实远端契约为准。
type GenerateImageResult struct {
	ImageURL string `json:"image_url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// NewImageGenClient 注入配置并复用目标服务的 Transport，不调用远端接口。
func NewImageGenClient(config base.Config, transport *http.Transport) (*ImageGenClient, error) {
	client, err := base.New(config, transport)
	if err != nil {
		return nil, err
	}
	return &ImageGenClient{
		client:  client,
		baseURL: config.BaseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   config.RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// GenerateImage 按暂定协议调用 POST /api/v1/image/imageGen/generateImage。
// 请求体为 GenerateImageParams，HTTP 200 的响应正文直接为 GenerateImageResult JSON；
// 路径和字段用于演示远端调用，尚未与真实图片服务确认。调用使用传入的 Context，不额外重试。
func (c *ImageGenClient) GenerateImage(ctx context.Context, params GenerateImageParams) (*GenerateImageResult, error) {
	if c == nil || c.client == nil || c.httpClient == nil {
		return nil, errors.New("image generation client is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("image generation context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return nil, errors.New("image generation prompt is required")
	}
	if params.Width < 0 || params.Height < 0 {
		return nil, errors.New("image dimensions must not be negative")
	}
	if params.Width == 0 {
		params.Width = 1024
	}
	if params.Height == 0 {
		params.Height = 1024
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode image generation request: %w", err)
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/api/v1/image/imageGen/generateImage"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create image generation request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send image generation request: %w", err)
	}
	defer response.Body.Close()

	// 示例响应只有图片地址和尺寸，限制读取大小，避免将任意响应体完整载入内存。
	const maxResponseBytes = 1 << 20
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image generation response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, errors.New("image generation response exceeds 1 MiB")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image generation returned HTTP status %d", response.StatusCode)
	}
	var result GenerateImageResult
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("decode image generation response: %w", err)
	}
	imageURL, err := url.Parse(result.ImageURL)
	if err != nil || (imageURL.Scheme != "http" && imageURL.Scheme != "https") || imageURL.Hostname() == "" || result.Width <= 0 || result.Height <= 0 {
		return nil, errors.New("image generation response must contain an HTTP image URL and positive dimensions")
	}
	return &result, nil
}
