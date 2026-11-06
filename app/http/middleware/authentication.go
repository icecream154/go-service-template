package middleware

import (
	"context"
	"errors"
	"log"
	"net/http"
	"reflect"
	"strings"

	"go-service-template/internal/authn"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

var (
	// errInvalidAuthorizationHeader 表示 HTTP 凭证结构不合法，不表示 JWT 验签失败。
	// 错误不包含收到的 Header 或 Token，重复值、缺少正文及含混格式共用此错误。
	errInvalidAuthorizationHeader = errors.New("authorization header must contain exactly one bearer credential")
	// errInvalidAuthenticatedPrincipal 表示认证器返回成功，却违反公共身份结果契约。
	// 属于认证实现问题，不能将其作为调用方凭证失败返回 401。
	errInvalidAuthenticatedPrincipal = errors.New("authenticator returned an invalid principal")
)

// RequireAuthentication 构造强制 Bearer 认证中间件；只有显式挂到路由后才会执行。
// authenticator 由装配层传入并须支持并发使用，本包不获取 JWT 单例或判断业务权限。
// nil 依赖在构造时 panic，属于装配错误；每个请求的凭证和身份均保存为局部变量。
func RequireAuthentication(authenticator authn.Authenticator) gin.HandlerFunc {
	if authenticator == nil {
		panic("authentication middleware requires an authenticator")
	}
	// 接口值可能持有带类型的 nil 指针，此时接口本身不等于 nil。
	// 只在构造时检查可为 nil 的动态类型，避免把依赖错误拖到请求处理时。
	value := reflect.ValueOf(authenticator)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			panic("authentication middleware requires an authenticator")
		}
	default:
		panic("unhandled default case")
	}

	return func(ctx *gin.Context) {
		// 本轮认证必须自行产生身份，不能沿用同一请求前序处理器留下的身份。
		// nil 不是 Principal，读取助手会返回 false；正常跨请求隔离由 Gin 完成。
		ctx.Set(principalContextKey, nil)
		credential, err := readCredential(ctx.Request.Header)
		if err != nil {
			failAuthentication(ctx, err)
			return
		}
		// JWT 的算法、签名、声明和有效期均交给认证器，本层不解码 Token。
		// 沿用 Request Context；现有外层响应等待包装器负责取消隔离，本层不另设超时。
		principal, err := authenticator.Authenticate(ctx.Request.Context(), credential)
		if err != nil {
			failAuthentication(ctx, err)
			return
		}
		// 防止自定义认证器错误地返回空身份或未登记类别，并以 nil error 放行。
		// 这里只检查公共结果结构；命名空间组合及身份真实性仍由认证器确认。
		if (principal.Kind != authn.PrincipalKindUser && principal.Kind != authn.PrincipalKindService) ||
			strings.TrimSpace(principal.Namespace) == "" || strings.TrimSpace(principal.SubjectID) == "" {
			failAuthentication(ctx, errInvalidAuthenticatedPrincipal)
			return
		}
		setPrincipal(ctx, principal)
		ctx.Next()
	}
}

// readCredential 只读取一个 Authorization 值，不从 Query、Body 或 Cookie 查找凭证。
// Header 外层的 HTTP 可选空白（空格、制表符）可移除；Bearer 与正文之间允许一个或多个
// ASCII 空格。Token 本身必须完整保留，不能用 Fields 拆开后重新拼接，也不进行 URL 解码。
func readCredential(header http.Header) (authn.Credential, error) {
	var values []string
	// HTTP 头名称忽略大小写；遍历也能发现手工构造的 Header 中大小写不同的重复键。
	// Header.Get 只取第一个值，无法据此判断是否存在多份 Authorization。
	for name, entries := range header {
		if strings.EqualFold(name, "Authorization") {
			values = append(values, entries...)
		}
	}
	if len(values) == 0 {
		return authn.Credential{}, authn.ErrMissingCredential
	}
	if len(values) != 1 {
		return authn.Credential{}, errInvalidAuthorizationHeader
	}
	value := strings.Trim(values[0], " \t")
	if value == "" {
		return authn.Credential{}, authn.ErrMissingCredential
	}
	scheme, token, separated := strings.Cut(value, " ")
	token = strings.TrimLeft(token, " ")
	if !separated || scheme == "" || token == "" {
		return authn.Credential{}, errInvalidAuthorizationHeader
	}
	// auth-scheme 使用 HTTP token 字符集：语法非法返回 400，合法但不支持的方案返回 401。
	for _, ch := range scheme {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", ch)) {
			return authn.Credential{}, errInvalidAuthorizationHeader
		}
	}
	// Bearer 正文采用 HTTP b64token 字符集；这只是头格式检查，不代表 JWT 已合法。
	// 拒绝内部空白、逗号拼接、多份凭证及位于正文中间的填充符，不能静默选择其中一份。
	padding := false
	for index, ch := range token {
		if ch == '=' && index > 0 {
			padding = true
			continue
		}
		if padding || !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') || strings.ContainsRune("-._~+/", ch)) {
			return authn.Credential{}, errInvalidAuthorizationHeader
		}
	}
	if !strings.EqualFold(scheme, "Bearer") {
		return authn.Credential{}, authn.ErrUnsupportedScheme
	}
	return authn.Credential{Scheme: scheme, Value: token}, nil
}

// failAuthentication 沿用公共响应结构，原样使用错误消息，不添加业务码或字段明细。
// response.Fail 会 Abort 后续 Gin 处理器；调用方仍必须 return 结束当前处理函数。
func failAuthentication(ctx *gin.Context, err error) {
	failure := response.ErrorInfo{Code: response.CodeInternalError, Message: err.Error()}
	switch {
	case errors.Is(err, errInvalidAuthorizationHeader):
		failure.Code = response.CodeInvalidArgument
		ctx.Header("WWW-Authenticate", `Bearer realm="api", error="invalid_request"`)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, authn.ErrInvalidPrincipal):
		// 执行取消、超时和签发输入错误不代表客户端凭证无效，保持内部错误分类。
	case errors.Is(err, authn.ErrMissingCredential), errors.Is(err, authn.ErrUnsupportedScheme),
		errors.Is(err, authn.ErrInvalidCredential), errors.Is(err, authn.ErrExpiredCredential):
		failure.Code = response.CodeUnauthenticated
		challenge := `Bearer realm="api"`
		if errors.Is(err, authn.ErrInvalidCredential) || errors.Is(err, authn.ErrExpiredCredential) {
			challenge += `, error="invalid_token"`
		}
		ctx.Header("WWW-Authenticate", challenge)
	}
	if failure.Code == response.CodeInternalError {
		// 原始原因由错误产生处保证不包含凭证；这里不额外记录 Header、Token 或身份对象。
		log.Printf("Authenticate HTTP request failed: error=%v", err)
	}
	response.Fail(ctx, failure)
}
