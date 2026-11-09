package image

import (
	"context"
	"net/http"

	"go-service-template/app/integrations/company/base"
	"go-service-template/internal/logger"
)

// ImageGenClient 组合公共 Client，对应目标服务的图片生成 controller。
type ImageGenClient struct {
	client *base.Client
}

// GenerateImageParams 是图片生成接口的模拟入参，后续按真实远端契约调整。
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
	return &ImageGenClient{client: client}, nil
}

// GenerateImage 按暂定协议调用 POST /api/v1/image/imageGen/generateImage。
// 请求体为 GenerateImageParams，HTTP 200 的响应正文直接为 GenerateImageResult JSON；
// 路径和字段用于演示远端调用，尚未与真实图片服务确认。调用使用传入的 Context，不额外重试。
func (c *ImageGenClient) GenerateImage(ctx context.Context, params GenerateImageParams) (*GenerateImageResult, error) {
	// 1. 在具体业务方法入口识别 Client 操作，保留父 Context 的 TraceId、取消和截止时间。
	// nil 继续交给公共请求层报错，避免 WithCurrentOperation 将其变成可发送请求的 Background。
	if ctx != nil {
		// 1.1. 方法符号由公共封装自动解析，不手写操作名称，公共 PostJSON 只沿用此作用域。
		ctx = logger.WithCurrentOperation(ctx)
	}
	// 2. 由公共底座执行一次 JSON 调用并记录完整生命周期；本方法不重复打印请求或错误日志。
	var result GenerateImageResult
	if err := c.client.PostJSON(ctx, "/api/v1/image/imageGen/generateImage", params, &result); err != nil {
		// 2.1. 原样返回底座错误，日志中的受控错误说明不替换调用方可判断的错误链。
		return nil, err
	}
	// 3. 协议处理成功后返回远端结果，不在日志中记录图片地址、提示词或其他业务字段。
	return &result, nil
}
