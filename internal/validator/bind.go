// Package validator 提供 HTTP 请求参数到 Request DTO 的通用绑定入口。
package validator

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Bind 根据请求方法选择 Gin 绑定器，将参数填入 target 指向的 Request DTO。
// GET 只读取 Query，按 form 标签映射；POST 只读取 JSON，按 json 标签映射。
// 调用方须为每次请求创建独立 DTO 并传入指针，绑定失败时由调用方输出错误响应。
// 本函数不增加校验规则；DTO 若已有 binding 标签，Gin 仍会执行对应的基础校验。
func Bind(ctx *gin.Context, target any) error {
	switch ctx.Request.Method {
	case http.MethodGet:
		return ctx.ShouldBindQuery(target)
	case http.MethodPost:
		if ctx.ContentType() != "application/json" {
			return errors.New("Content-Type must be application/json")
		}
		return ctx.ShouldBindJSON(target)
	default:
		return errors.New("unsupported request method")
	}
}
