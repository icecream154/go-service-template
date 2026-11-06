// Package validator 提供 HTTP 请求参数到 Request DTO 的通用绑定入口。
package validator

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// Bind 根据请求方法将参数映射到 target 指向的 Request DTO，不执行自动校验。
// GET 只读取 Query，按 form 标签映射；POST 只读取 JSON，按 json 标签映射。
// 调用方须为每次请求创建独立 DTO 并传入指针，绑定失败时由调用方输出错误响应。
// JSON 语法或字段类型转换错误属于绑定失败；必填、长度、范围等规则由 Validator 显式检查。
// Request DTO 禁止使用 binding 等自动校验标签，本入口不调用 Gin 的自动校验器。
func Bind(ctx *gin.Context, target any) error {
	switch ctx.Request.Method {
	case http.MethodGet:
		// 复用 Gin 的 Query 映射能力，跳过 ShouldBindQuery 附带的标签校验。
		return binding.MapFormWithTag(target, ctx.Request.URL.Query(), "form")
	case http.MethodPost:
		if ctx.ContentType() != "application/json" {
			return errors.New("Content-Type must be application/json")
		}
		if ctx.Request.Body == nil {
			return errors.New("invalid request")
		}
		// 标准解码保留 json 标签、指针及预填默认值语义，不触发 Gin 的标签校验。
		return json.NewDecoder(ctx.Request.Body).Decode(target)
	default:
		return errors.New("unsupported request method")
	}
}
