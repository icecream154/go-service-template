package v1

import (
	"go-service-template/app/domains/health/services"
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
)

// CheckResponse 是健康接口的对外数据，不直接序列化服务内部类型。
type CheckResponse struct {
	Status string `json:"status"`
}

func Data(value any) any {
	switch result := value.(type) {
	case services.CheckResult:
		return CheckResponse{Status: result.Status}
	case nil:
		return nil
	default:
		panic("unmapped health result type")
	}
}

func Write(c *gin.Context, result services.Result) {
	response.Success(c, result.Code, result.Message, Data(result.Data))
}
