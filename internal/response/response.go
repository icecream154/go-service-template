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
// 通用类别必须已登记，HTTP 状态码由类别的固定映射决定。
// 合法错误对象的消息、业务码和字段明细原样输出，包括 INTERNAL_ERROR。
// 未登记的类别或空白必填内容属于无效错误定义，使用框架默认内部错误兜底。
func Fail(ctx *gin.Context, failure ErrorInfo) {
	definition, valid := failure.Code.definition()
	if !valid || strings.TrimSpace(failure.Message) == "" ||
		(failure.BusinessCode != "" && strings.TrimSpace(string(failure.BusinessCode)) == "") {
		failure = InternalError()
		definition, _ = failure.Code.definition()
	}
	ctx.AbortWithStatusJSON(definition.httpStatus, Response[any]{Data: nil, Error: &failure})
}
