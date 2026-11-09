package company

import (
	"errors"
	"fmt"
	"net/http"

	"go-service-template/app/integrations/company/base"
	"go-service-template/app/integrations/company/python-service-template/image"
)

const pythonServiceTemplateName = "python-service-template"

// PythonServiceTemplateClients 按目标服务分组，不引入业务 Service 层。
type PythonServiceTemplateClients struct {
	Image ImageClients
}

type ImageClients struct {
	ImageEdit *image.ImageEditClient
	ImageGen  *image.ImageGenClient
}

func newPythonServiceTemplateClients(config serviceConfig) (PythonServiceTemplateClients, *http.Transport, error) {
	controllers, exists := config.Domains["image"]
	if !exists || len(config.Domains) != 1 {
		return PythonServiceTemplateClients{}, nil, errors.New("python-service-template must configure only the registered image domain")
	}
	editConfig, hasEdit := controllers["image_edit"]
	genConfig, hasGen := controllers["image_gen"]
	if !hasEdit || !hasGen || len(controllers) != 2 {
		return PythonServiceTemplateClients{}, nil, errors.New("python-service-template.image must configure the registered image_edit and image_gen clients")
	}
	transport, err := base.NewTransport(config.ConnectTimeout)
	if err != nil {
		return PythonServiceTemplateClients{}, nil, fmt.Errorf("create python-service-template transport: %w", err)
	}
	edit, err := image.NewImageEditClient(base.Config{BaseURL: config.BaseURL, RequestTimeout: editConfig.RequestTimeout}, transport)
	if err != nil {
		transport.CloseIdleConnections()
		return PythonServiceTemplateClients{}, nil, fmt.Errorf("create image edit client: %w", err)
	}
	gen, err := image.NewImageGenClient(base.Config{BaseURL: config.BaseURL, RequestTimeout: genConfig.RequestTimeout}, transport)
	if err != nil {
		transport.CloseIdleConnections()
		return PythonServiceTemplateClients{}, nil, fmt.Errorf("create image generation client: %w", err)
	}
	return PythonServiceTemplateClients{Image: ImageClients{ImageEdit: edit, ImageGen: gen}}, transport, nil
}
