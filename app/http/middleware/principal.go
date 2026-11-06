package middleware

import (
	"go-service-template/internal/authn"

	"github.com/gin-gonic/gin"
)

// principalContextKey 只供本包操作，业务代码通过 PrincipalFromContext 读取身份。
// 使用包路径前缀避免与其他 Gin Context 数据重名；不使用请求参数作为存储键。
const principalContextKey = "go-service-template/app/http/middleware/principal"

// setPrincipal 仅由认证中间件在验证成功后调用，按值保存本次可信身份。
// 不保存 Token、用户模型或权限结果，也不向 Request DTO 或共享实例写入身份。
func setPrincipal(ctx *gin.Context, principal authn.Principal) {
	ctx.Set(principalContextKey, principal)
}

// PrincipalFromContext 返回本次请求已通过认证的身份副本，不执行认证或写出响应。
// 身份未保存或存储类型不正确时返回零值和 false，不能把该结果当作匿名认证成功。
// 调用方必须先检查 bool；修改返回值不会改变 Context 中的身份。
// 身份只在当前请求中使用，不据此修改共享对象，也不自动传入 Service。
func PrincipalFromContext(ctx *gin.Context) (authn.Principal, bool) {
	value, exists := ctx.Get(principalContextKey)
	if !exists {
		return authn.Principal{}, false
	}
	principal, ok := value.(authn.Principal)
	if !ok {
		return authn.Principal{}, false
	}
	return principal, true
}
