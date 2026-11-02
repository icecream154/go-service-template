package contracts

import "github.com/gin-gonic/gin"

// VersionedRouter 是领域 V1 路由的统一挂载契约。
type VersionedRouter interface {
	RegisterV1Routes(group *gin.RouterGroup)
}
