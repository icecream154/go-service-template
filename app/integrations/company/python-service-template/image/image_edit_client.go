package image

import (
	"context"
	"net/http"
	"net/url"

	"go-service-template/app/integrations/company/base"
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
	query := url.Values{"task_id": {params.TaskID}}
	var result QueryImageEditTaskByIdResult
	if err := c.client.GetJSON(ctx, "/api/v1/image/imageEdit/queryImageEditTaskById", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
