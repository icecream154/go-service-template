package image

import (
	"net/http"

	"go-service-template/app/integrations/company/base"
)

// ImageGenClient 组合公共 Client，对应目标服务的图片生成 controller。
type ImageGenClient struct {
	client *base.Client
}

// NewImageGenClient 注入配置并复用目标服务的 Transport，不调用远端接口。
func NewImageGenClient(config base.Config, transport *http.Transport) (*ImageGenClient, error) {
	client, err := base.New(config, transport)
	if err != nil {
		return nil, err
	}
	return &ImageGenClient{client: client}, nil
}
