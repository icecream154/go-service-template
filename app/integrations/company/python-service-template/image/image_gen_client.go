package image

import "go-service-template/app/integrations/company/base"

// ImageGenClient 组合公共 Client，对应目标服务的图片生成 controller。
type ImageGenClient struct {
	client *base.Client
}
