// Package authz 提供 Controller 复用的 HTTP 身份要求与项目身份转换方法。
// 本包只返回身份或错误；接口访问规则、响应类别和业务错误码仍由 Controller 决定。
package authz

import (
	"errors"

	httpmiddleware "go-service-template/app/http/middleware"
	"go-service-template/internal/authn"

	"github.com/gin-gonic/gin"
)

var (
	// ErrAuthenticationRequired 表示本次请求没有已认证身份，但调用方要求必须有身份。
	// Controller 将其映射为 401，并设置认证挑战；本包不直接写出响应。
	ErrAuthenticationRequired = errors.New("authentication required")
	// ErrPrincipalNotAllowed 表示已有可信身份，但类别或命名空间不符合调用方的要求。
	// Controller 将其映射为 403，并自行选择所属接口的业务错误码。
	ErrPrincipalNotAllowed = errors.New("authenticated principal is not allowed")
)

// RequirePrincipal 读取当前请求的可信身份，并检查调用方指定的类别和命名空间。
// ctx 属于已执行可选认证中间件的请求；本方法不读取 Header，也不重新验证凭证。
// 没有身份返回 ErrAuthenticationRequired；类别或命名空间不符返回 ErrPrincipalNotAllowed。
// 失败时返回零值身份，成功时返回身份副本，不修改 Context 中保存的身份。
// 本方法不检查目标对象或业务权限，不查询数据库，不写响应，也不调用 Abort。
// Controller 必须处理返回的错误，并自行决定是否继续调用 Service。
func RequirePrincipal(ctx *gin.Context, kind authn.PrincipalKind, namespace string) (authn.Principal, error) {
	principal, exists := httpmiddleware.PrincipalFromContext(ctx)
	if !exists {
		return authn.Principal{}, ErrAuthenticationRequired
	}
	if principal.Kind != kind || principal.Namespace != namespace {
		return authn.Principal{}, ErrPrincipalNotAllowed
	}
	return principal, nil
}
