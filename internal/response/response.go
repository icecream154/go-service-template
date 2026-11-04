// Package response 统一输出 HTTP JSON 响应，不依赖具体业务领域。
package response

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Response 的两个顶层字段始终存在；T 是业务用例专属的 Response DTO。
// 成功传已构造的 DTO 值，无业务字段时使用该用例的空结构体。
type Response[T any] struct {
	Data  *T         `json:"data"`
	Error *ErrorInfo `json:"error"`
}

// Success 输出当前接口约定的 HTTP 200 成功响应，不更改 DTO 内部的空值语义。
func Success[T any](ctx *gin.Context, data T) {
	ctx.JSON(http.StatusOK, Response[T]{Data: &data, Error: nil})
}

// Fail 输出结构化错误并终止后续 Gin 处理链；调用方仍须 return 结束当前函数。
// 无效错误定义属于服务端错误，不将其原文案或详情暴露给调用方。
func Fail(ctx *gin.Context, failure ErrorInfo) {
	if failure.HTTPStatus < http.StatusBadRequest || failure.HTTPStatus > 599 ||
		strings.TrimSpace(string(failure.Code)) == "" || strings.TrimSpace(failure.Message) == "" {
		failure = InternalError()
	}
	ctx.AbortWithStatusJSON(failure.HTTPStatus, Response[any]{Data: nil, Error: &failure})
}
