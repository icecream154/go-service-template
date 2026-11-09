package image

import (
	"context"
	"net/http"
	"net/url"

	"go-service-template/app/integrations/company/base"
	"go-service-template/internal/logger"
)

// ImageEditClient 组合公共 Client，对应目标服务的图片编辑 controller。
type ImageEditClient struct {
	client *base.Client
}

// QueryImageEditTaskByIdParams 是查询图片编辑任务的示例入参，后续按真实远端契约调整。
type QueryImageEditTaskByIdParams struct {
	TaskID string `json:"task_id"`
}

// QueryImageEditTaskByIdResult 是示例接口的出参，字段以后续真实远端契约为准。
type QueryImageEditTaskByIdResult struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	ImageURL string `json:"image_url"`
}

// NewImageEditClient 注入配置并复用目标服务的 Transport，不调用远端接口。
func NewImageEditClient(config base.Config, transport *http.Transport) (*ImageEditClient, error) {
	client, err := base.New(config, transport)
	if err != nil {
		return nil, err
	}
	return &ImageEditClient{client: client}, nil
}

// QueryImageEditTaskById 按暂定协议调用 GET /api/v1/image/imageEdit/queryImageEditTaskById。
// task_id 通过查询参数传入，HTTP 200 的响应正文直接为 QueryImageEditTaskByIdResult JSON；
// 路径和字段用于演示远端调用，尚未与真实图片服务确认。调用使用传入的 Context，不额外重试。
func (c *ImageEditClient) QueryImageEditTaskById(ctx context.Context, params QueryImageEditTaskByIdParams) (*QueryImageEditTaskByIdResult, error) {
	// 1. 在具体 Client 的业务入口建立操作作用域，自动沿用当前请求的 TraceId 和执行控制。
	// nil Context 不派生 Background，继续保留公共层拒绝无上下文请求的原有契约。
	if ctx != nil {
		// 1.1. 公共封装从当前方法符号识别操作，不把请求实参或底座 GetJSON 写入操作名称。
		ctx = logger.WithCurrentOperation(ctx)
	}
	// 2. 按现有协议构造任务查询参数；实际任务 ID 仅传给远端，不参与日志字段。
	query := url.Values{"task_id": {params.TaskID}}
	// 3. 公共底座统一发送请求并记录开始、结束及错误，本方法只负责业务入参与出参。
	var result QueryImageEditTaskByIdResult
	if err := c.client.GetJSON(ctx, "/api/v1/image/imageEdit/queryImageEditTaskById", query, &result); err != nil {
		// 3.1. 原样交回底座错误，不重复记录日志或改变调用方可检查的错误链。
		return nil, err
	}
	// 4. 返回已解码的业务结果，不额外记录响应正文或图片地址。
	return &result, nil
}
