package image

import (
	"net/http"

	"go-service-template/app/integrations/company/base"
)

// ImageEditClient 组合公共 Client，对应目标服务的图片编辑 controller。
type ImageEditClient struct {
	client *base.Client
}

// NewImageEditClient 注入配置并复用目标服务的 Transport，不调用远端接口。
func NewImageEditClient(config base.Config, transport *http.Transport) (*ImageEditClient, error) {
	client, err := base.New(config, transport)
	if err != nil {
		return nil, err
	}
	return &ImageEditClient{client: client}, nil
}
