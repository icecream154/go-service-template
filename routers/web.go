// Package routers 创建 HTTP 路由并绑定请求处理方法。
package routers

import (
	userrouter "go-service-template/app/domains/user/routers"
	httpmiddleware "go-service-template/app/http/middleware"
	"go-service-template/internal/authn"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

// New 创建带路由模板绑定、异常恢复及业务 API 可选认证的 Gin 路由。
// 用户领域的控制器在注册前创建，后续请求复用已绑定的处理方法。
// authenticator 必须已完成装配且可并发使用；构造本身不查询数据库或签发凭证。
// HTTP 生命周期日志由 NewHTTPHandler 的外层 HTTPLogging 负责，独立使用本路由时需显式装配。
func New(authenticator authn.Authenticator) *gin.Engine {
	// 1. 创建没有默认中间件的 Gin Engine，明确管理模板绑定、异常恢复与认证的先后顺序。
	router := gin.New()
	// 2. 全局中间件在 Gin 完成路由匹配后绑定模板，使认证失败和 NoRoute 也能使用 HTTP 操作作用域。
	// 外层 HTTPLogging 负责唯一的请求与响应日志，避免 Gin 以内层结束时间再输出一份访问摘要。
	// CustomRecovery 捕获当前请求处理链中的 panic，记录异常后执行恢复回调。
	// 普通业务方法返回的 error 仍由 Controller 判断并输出，不会自动进入该回调。
	// 回调的第二个参数是 panic 的内容；这里用 _ 忽略，不将内部异常直接返回给客户端。
	router.Use(httpmiddleware.HTTPRouteLogging(), gin.CustomRecovery(func(ctx *gin.Context, _ any) {
		// 2.1. Written 表示响应头已经发出，正文也可能已经写入。
		// 此时无法再修改 HTTP 状态码，也不能追加错误 JSON，以免破坏原响应。
		if ctx.Writer.Written() {
			// 2.1.1. Abort 阻止后续 Gin 处理器执行，return 结束当前恢复回调。
			ctx.Abort()
			return
		}
		// 2.2. 尚未发送响应时，统一返回 HTTP 500 和 data/error 结构。
		// data 为 null，错误码为 INTERNAL_ERROR，文案固定为 internal server error；
		// 原始 panic 详情保留在服务端日志中。
		response.Fail(ctx, response.InternalError())
	}))
	// 3. NoRoute 处理未匹配已注册路由的请求，模板绑定中间件会将它记录为固定 unmatched 标识。
	// 统一返回 HTTP 404 和 data/error 结构：data 为 null，错误码为 NOT_FOUND，
	// 文案为 route not found，使框架层错误与业务接口使用相同的响应格式。
	router.NoRoute(func(ctx *gin.Context) {
		// 3.1. 保持现有 404 业务响应，HTTP 日志只观察结果，不改变返回结构或错误文案。
		response.Fail(ctx, response.NotFound("route not found"))
	})

	// 4. 顶层统一管理 API 前缀和版本，各领域按 Controller 分组注册业务用例。
	api := router.Group("/api")
	// 4.1. 所有版本和领域继承这一次认证：无头按匿名继续，有凭证必须验证通过。
	// 这里不要求必须登录；Validator 先检查参数，Controller 再判断身份和业务权限。
	api.Use(httpmiddleware.AuthenticateIfPresent(authenticator))
	// 4.2. 注册 V1 领域路由，HTTP operation 使用 Gin 注册模板，不从具体 Controller 方法名猜路径。
	v1 := api.Group("/v1")
	userrouter.NewRouter().RegisterV1Routes(v1)

	// 5. 返回可复用的 Gin Engine；监听、TraceId、响应等待及外层日志由生产 HTTP 装配层负责。
	return router
}
