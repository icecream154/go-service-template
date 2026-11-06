package jwt

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"go-service-template/internal/authn"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// Provider 使用固定的 HS256 为用户或服务签发 JWT，并验证同一信任范围内的凭证。
// 必须通过 New 构造；配置、密钥和允许的身份范围在构造后不再修改，可并发使用。
// JWT 仅签名、不加密；本组件不加载业务对象，不判断接口权限，也不维护撤销状态。
type Provider struct {
	issuer            string
	audience          string
	tokenTTL          time.Duration
	signingKey        []byte
	allowedNamespaces map[identityScope]struct{}
	parser            *jwtlib.Parser
}

// identityScope 将类别与命名空间作为整体，避免不同类别的同名空间互相替代。
type identityScope struct {
	kind      authn.PrincipalKind
	namespace string
}

// tokenClaims 是本组件签发与接受的身份声明；注册声明提供签发方、接收方和有效期。
// 不把 HTTP、业务权限或单次请求状态放入凭证，也不在验证完成前信任这些字段。
type tokenClaims struct {
	Kind      authn.PrincipalKind `json:"kind"`
	Namespace string              `json:"namespace"`
	jwtlib.RegisteredClaims
}

var (
	_ authn.Issuer        = (*Provider)(nil)
	_ authn.Authenticator = (*Provider)(nil)
)

// New 校验配置、读取签名密钥并复制允许的身份范围，构造独立的 JWT 签发与认证组件。
// allowedNamespaces 由应用装配层提供，只接受 user、service 及其非空命名空间列表。
// 框架不引用具体业务枚举；调用方后续修改传入的 map 或 slice 不影响本组件。
func New(config Config, allowedNamespaces map[authn.PrincipalKind][]string) (*Provider, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(allowedNamespaces) == 0 {
		return nil, errors.New("JWT allowed namespaces must not be empty")
	}
	scopes := make(map[identityScope]struct{})
	for kind, namespaces := range allowedNamespaces {
		if kind != authn.PrincipalKindUser && kind != authn.PrincipalKindService {
			return nil, errors.New("JWT principal kind is not supported")
		}
		if len(namespaces) == 0 {
			return nil, errors.New("JWT principal kind must have at least one allowed namespace")
		}
		for _, namespace := range namespaces {
			if strings.TrimSpace(namespace) == "" || !utf8.ValidString(namespace) {
				return nil, errors.New("JWT namespace must be nonblank valid UTF-8")
			}
			scopes[identityScope{kind: kind, namespace: namespace}] = struct{}{}
		}
	}
	key, err := config.loadSigningKey()
	if err != nil {
		return nil, err
	}
	return &Provider{
		issuer:            config.Issuer,
		audience:          config.Audience,
		tokenTTL:          config.TokenTTL,
		signingKey:        key,
		allowedNamespaces: scopes,
		parser: jwtlib.NewParser(
			jwtlib.WithValidMethods([]string{jwtlib.SigningMethodHS256.Alg()}),
			jwtlib.WithIssuer(config.Issuer),
			jwtlib.WithAudience(config.Audience),
			jwtlib.WithExpirationRequired(),
			jwtlib.WithNotBeforeRequired(),
			jwtlib.WithIssuedAt(),
			jwtlib.WithStrictDecoding(),
		),
	}, nil
}

// Issue 为调用方已确认可信的身份签发 Bearer JWT，不负责证明传入身份的真实性。
// 身份类别与命名空间必须在允许范围内，主体标识非空白且保留原文，不裁剪或改写。
// 时间统一使用 UTC 整秒；凭证立即生效，超过配置的 TokenTTL 后失效。
// ctx 必须非 nil；签名前后检查取消，失败时不返回部分凭证。
func (provider *Provider) Issue(ctx context.Context, principal authn.Principal) (authn.IssuedCredential, error) {
	if err := ctx.Err(); err != nil {
		return authn.IssuedCredential{}, err
	}
	if !provider.acceptsPrincipal(principal) {
		return authn.IssuedCredential{}, authn.ErrInvalidPrincipal
	}
	issuedAt := time.Now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(provider.tokenTTL)
	claims := tokenClaims{
		Kind:      principal.Kind,
		Namespace: principal.Namespace,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    provider.issuer,
			Subject:   principal.SubjectID,
			Audience:  jwtlib.ClaimStrings{provider.audience},
			IssuedAt:  jwtlib.NewNumericDate(issuedAt),
			NotBefore: jwtlib.NewNumericDate(issuedAt),
			ExpiresAt: jwtlib.NewNumericDate(expiresAt),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	value, err := token.SignedString(provider.signingKey)
	if contextError := ctx.Err(); contextError != nil {
		return authn.IssuedCredential{}, contextError
	}
	if err != nil {
		return authn.IssuedCredential{}, err
	}
	return authn.IssuedCredential{
		Credential: authn.Credential{Scheme: "Bearer", Value: value},
		IssuedAt:   issuedAt,
		ExpiresAt:  expiresAt,
	}, nil
}

// Authenticate 验证完整的 Bearer JWT，成功后才返回可信身份，不检查具体接口权限。
// Bearer 名称忽略大小写；不裁剪方案或凭证原文。算法、签名、类型、签发方、接收方、
// 必填时间及身份范围必须全部符合约定；不设置过期宽限，也不接受未来签发的凭证。
// ctx 必须非 nil；验证前后检查取消，任何失败均返回空 Principal。
func (provider *Provider) Authenticate(ctx context.Context, credential authn.Credential) (authn.Principal, error) {
	if err := ctx.Err(); err != nil {
		return authn.Principal{}, err
	}
	schemeMissing := strings.TrimSpace(credential.Scheme) == ""
	valueMissing := strings.TrimSpace(credential.Value) == ""
	if schemeMissing && valueMissing {
		return authn.Principal{}, authn.ErrMissingCredential
	}
	if schemeMissing || valueMissing {
		return authn.Principal{}, authn.ErrInvalidCredential
	}
	if !strings.EqualFold(credential.Scheme, "Bearer") {
		return authn.Principal{}, authn.ErrUnsupportedScheme
	}
	var claims tokenClaims
	token, err := provider.parser.ParseWithClaims(credential.Value, &claims, func(token *jwtlib.Token) (any, error) {
		// 同时限制算法名称和具体实现，不由凭证或动态注册的其他算法选择验证方式。
		if token.Method != jwtlib.SigningMethodHS256 {
			return nil, authn.ErrInvalidCredential
		}
		return provider.signingKey, nil
	})
	if contextError := ctx.Err(); contextError != nil {
		return authn.Principal{}, contextError
	}
	if err != nil {
		// 密钥类型或哈希实现故障属于运行问题；不能误判成调用方的凭证不合法。
		if errors.Is(err, jwtlib.ErrInvalidKeyType) || errors.Is(err, jwtlib.ErrInvalidKey) || errors.Is(err, jwtlib.ErrHashUnavailable) {
			return authn.Principal{}, err
		}
		// 只归类库已确认的 JWT 输入失败，其他未识别的执行错误仍保留原始原因。
		if !errors.Is(err, jwtlib.ErrTokenMalformed) && !errors.Is(err, jwtlib.ErrTokenUnverifiable) &&
			!errors.Is(err, jwtlib.ErrTokenSignatureInvalid) && !errors.Is(err, jwtlib.ErrTokenInvalidClaims) {
			return authn.Principal{}, err
		}
		// 库可能同时报告过期和接收方错误等多个原因；只有过期不能直接视为已认证。
		if !errors.Is(err, jwtlib.ErrTokenExpired) || !onlyExpirationCauses(err) {
			return authn.Principal{}, authn.ErrInvalidCredential
		}
	}
	principal := authn.Principal{Kind: claims.Kind, Namespace: claims.Namespace, SubjectID: claims.Subject}
	// 标准验证器检查时间是否已生效/已过期；这里补齐 iat 必填和各时间之间的关系。
	// 即使签名有效且已过期，缺失身份、错误 typ 或矛盾时间仍优先归为无效凭证。
	if token == nil || token.Header["typ"] != "JWT" || !provider.acceptsPrincipal(principal) ||
		claims.IssuedAt == nil || claims.NotBefore == nil || claims.ExpiresAt == nil ||
		claims.NotBefore.Before(claims.IssuedAt.Time) || !claims.ExpiresAt.After(claims.NotBefore.Time) {
		return authn.Principal{}, authn.ErrInvalidCredential
	}
	if err != nil {
		return authn.Principal{}, authn.ErrExpiredCredential
	}
	if !token.Valid {
		return authn.Principal{}, authn.ErrInvalidCredential
	}
	return principal, nil
}

// acceptsPrincipal 只检查身份结构和允许范围，不从业务输入推导或替换任何身份字段。
func (provider *Provider) acceptsPrincipal(principal authn.Principal) bool {
	if strings.TrimSpace(principal.SubjectID) == "" || !utf8.ValidString(principal.SubjectID) {
		return false
	}
	_, allowed := provider.allowedNamespaces[identityScope{kind: principal.Kind, namespace: principal.Namespace}]
	return allowed
}

// onlyExpirationCauses 检查 JWT 库的聚合错误树，允许外层声明错误分类及唯一的过期原因。
// 不能只用 errors.Is(err, ErrTokenExpired)：它在过期与其他失败同时出现时也会返回 true。
// 未识别的叶子错误一律拒绝，避免库增加验证规则后被误分类为单纯过期。
func onlyExpirationCauses(err error) bool {
	if errors.Is(err, jwtlib.ErrTokenExpired) || errors.Is(err, jwtlib.ErrTokenInvalidClaims) {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		causes := wrapped.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !onlyExpirationCauses(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return onlyExpirationCauses(wrapped.Unwrap())
	default:
		return false
	}
}
