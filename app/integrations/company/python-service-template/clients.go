// Package pythonservicetemplate 装配 python-service-template 的共享 Transport 和具体 Client。
package pythonservicetemplate

import (
	"errors"
	"fmt"
	"net/http"

	"go-service-template/app/integrations/company/base"
	"go-service-template/app/integrations/company/python-service-template/image"
)

// Clients 保存目标服务的 Client 分组，不引入业务 Service 层。
type Clients struct {
	Image ImageClients
}

// ImageClients 保存 image domain 下的 controller Client。
type ImageClients struct {
	ImageEdit *image.ImageEditClient
	ImageGen  *image.ImageGenClient
}

// New 按注入配置校验已注册的 Client，并为它们构造同一个目标服务 Transport。
// 构造过程不发起 HTTP 请求；成功后由调用方管理 Transport 的最终清理。
func New(config base.ServiceConfig) (Clients, *http.Transport, error) {
	controllers, exists := config.Domains["image"]
	if !exists || len(config.Domains) != 1 {
		return Clients{}, nil, errors.New("python-service-template must configure only the registered image domain")
	}
	editConfig, hasEdit := controllers["image_edit"]
	genConfig, hasGen := controllers["image_gen"]
	if !hasEdit || !hasGen || len(controllers) != 2 {
		return Clients{}, nil, errors.New("python-service-template.image must configure the registered image_edit and image_gen clients")
	}
	transport, err := base.NewTransport(config.ConnectTimeout)
	if err != nil {
		return Clients{}, nil, fmt.Errorf("create python-service-template transport: %w", err)
	}
	edit, err := image.NewImageEditClient(base.Config{BaseURL: config.BaseURL, RequestTimeout: editConfig.RequestTimeout}, transport)
	if err != nil {
		transport.CloseIdleConnections()
		return Clients{}, nil, fmt.Errorf("create image edit client: %w", err)
	}
	gen, err := image.NewImageGenClient(base.Config{BaseURL: config.BaseURL, RequestTimeout: genConfig.RequestTimeout}, transport)
	if err != nil {
		transport.CloseIdleConnections()
		return Clients{}, nil, fmt.Errorf("create image generation client: %w", err)
	}
	return Clients{Image: ImageClients{ImageEdit: edit, ImageGen: gen}}, transport, nil
}
