package image

import "go-service-template/app/integrations/company/base"

// ImageEditClient 组合公共 Client，对应目标服务的图片编辑 controller。
type ImageEditClient struct {
	client *base.Client
}
