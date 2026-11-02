package requests

import (
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
)

const RequestKey = "Request"

// Bind 在路由入口完成绑定，将独立请求 DTO 交给控制器。
func Bind[T any]() gin.HandlerFunc {
	return func(c *gin.Context) {
		var request T
		if err := c.ShouldBind(&request); err != nil {
			response.ErrorParam(c, err)
			return
		}
		c.Set(RequestKey, &request)
		c.Next()
	}
}

func Get[T any](c *gin.Context) (*T, bool) {
	value, exists := c.Get(RequestKey)
	if !exists {
		return nil, false
	}
	request, ok := value.(*T)
	return request, ok && request != nil
}
