package jwt_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"

	"go-service-template/internal/authn"
	"go-service-template/internal/authn/jwt"
)

const (
	jwtUserNamespace    = "test-members"
	jwtServiceNamespace = "test-services"
)

type newJWTProviderCase struct {
	name       string
	namespaces map[authn.PrincipalKind][]string
	badConfig  bool
	wantError  string
}

func TestNew(t *testing.T) {
	tests := []newJWTProviderCase{
		// 同时登记用户与服务范围，验证两种已声明身份可以共用同一组件。
		{name: "user and service scopes", namespaces: jwtTestNamespaces()},
		// 身份范围正确但有效期无效，验证构造前会检查配置。
		{name: "invalid config", namespaces: jwtTestNamespaces(), badConfig: true, wantError: "token_ttl"},
		// 缺少范围列表时无法授权任何身份，验证构造失败。
		{name: "empty allowed namespaces", wantError: "allowed namespaces must not be empty"},
		// 非空命名空间不能让未声明的主体类别变得有效。
		{name: "unsupported kind", namespaces: map[authn.PrincipalKind][]string{"other": {"test"}}, wantError: "principal kind is not supported"},
		// 已声明的类别也必须至少有一个具体命名空间。
		{name: "kind with no scopes", namespaces: map[authn.PrincipalKind][]string{authn.PrincipalKindUser: {}}, wantError: "at least one allowed namespace"},
		// 纯空白的命名空间不能代表已登记的身份范围。
		{name: "blank namespace", namespaces: map[authn.PrincipalKind][]string{authn.PrincipalKindUser: {" \t"}}, wantError: "namespace must be nonblank valid UTF-8"},
		// 无效 UTF-8 无法在凭证中精确还原，验证构造阶段拒绝。
		{name: "invalid UTF-8 namespace", namespaces: map[authn.PrincipalKind][]string{authn.PrincipalKindService: {"\xff"}}, wantError: "namespace must be nonblank valid UTF-8"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := jwtTestConfig()
			if test.badConfig {
				config.TokenTTL = 0
			}
			provider, err := jwt.New(config, test.namespaces)
			assertJWTConfigError(t, err, test.wantError)
			if (provider == nil) != (test.wantError != "") {
				t.Error("New() returned an unexpected provider presence")
			}
		})
	}
}

type issueJWTCase struct {
	name      string
	principal authn.Principal
	canceled  bool
	wantErr   error
}

func TestNewCopiesAllowedNamespaces(t *testing.T) {
	config := jwtTestConfig()
	namespaces := jwtTestNamespaces()
	provider := newJWTTestProvider(t, config, namespaces)

	// 修改原始切片的元素并从原始映射删除服务范围，验证构造后的身份范围仍是独立快照。
	namespaces[authn.PrincipalKindUser][0] = "new-members"
	delete(namespaces, authn.PrincipalKindService)
	tests := []issueJWTCase{
		// 用户范围原先登记过，修改传入的切片后仍可签发。
		{name: "original user scope", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: "user-1"}},
		// 已删除的服务键仍保留在组件快照内。
		{name: "original service scope", principal: authn.Principal{Kind: authn.PrincipalKindService, Namespace: jwtServiceNamespace, SubjectID: "service-1"}},
		// 修改后出现的新命名空间未被登记，验证组件不会签发对应凭证。
		{name: "replacement scope", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: "new-members", SubjectID: "user-1"}, wantErr: authn.ErrInvalidPrincipal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issued, err := provider.Issue(t.Context(), test.principal)
			assertJWTIssueResult(t, issued, err, test.wantErr)
		})
	}
}

func TestIssue(t *testing.T) {
	config := jwtTestConfig()
	provider := newJWTTestProvider(t, config, jwtTestNamespaces())
	tests := []issueJWTCase{
		// 主体含前后空格但并非空白字符串，验证签发会保留主体原文。
		{name: "preserves user subject", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: " user-1 "}},
		// 服务类别与服务命名空间配对，验证其主体同样能正确签发。
		{name: "issues service credential", principal: authn.Principal{Kind: authn.PrincipalKindService, Namespace: jwtServiceNamespace, SubjectID: "service-1"}},
		// 用有效身份配已取消上下文，验证取消后不会生成部分凭证。
		{name: "canceled before signing", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: "user-1"}, canceled: true, wantErr: context.Canceled},
		// 命名空间在服务类别下有效，但不能替代用户类别的范围。
		{name: "scope belongs to another kind", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtServiceNamespace, SubjectID: "user-1"}, wantErr: authn.ErrInvalidPrincipal},
		// 同名范围不能给组件未接受的类别签发凭证。
		{name: "unregistered kind", principal: authn.Principal{Kind: "other", Namespace: jwtUserNamespace, SubjectID: "user-1"}, wantErr: authn.ErrInvalidPrincipal},
		// 使用合法范围隔离主体规则，验证纯空白主体无法签发。
		{name: "blank subject", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: " \t"}, wantErr: authn.ErrInvalidPrincipal},
		// 无效 UTF-8 不能作为签发主体，避免编码阶段替换原始字节。
		{name: "invalid UTF-8 subject", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: "\xff"}, wantErr: authn.ErrInvalidPrincipal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				if test.canceled {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				// 推进固定时钟到非整秒，验证签发时间按 UTC 整秒计算。
				time.Sleep(350 * time.Millisecond)
				wantIssuedAt := time.Now().UTC().Truncate(time.Second)
				issued, err := provider.Issue(ctx, test.principal)
				assertJWTIssueResult(t, issued, err, test.wantErr)
				if test.wantErr == nil {
					assertJWTIssuedClaims(t, config, test.principal, issued, wantIssuedAt)
				}
			})
		})
	}
}

type authenticateJWTCase struct {
	name       string
	credential authn.Credential
	canceled   bool
	wantErr    error
}

func TestAuthenticate(t *testing.T) {
	provider := newJWTTestProvider(t, jwtTestConfig(), jwtTestNamespaces())
	tests := []authenticateJWTCase{
		// 两个字段均未提供，验证返回缺失凭证错误。
		{name: "missing credential", wantErr: authn.ErrMissingCredential},
		// 两个字段都只有空白，验证等同未提供凭证。
		{name: "blank credential", credential: authn.Credential{Scheme: " ", Value: "\t"}, wantErr: authn.ErrMissingCredential},
		// 有内容但无方案，验证残缺凭证属于无效而非缺失。
		{name: "missing scheme", credential: authn.Credential{Value: "fixture"}, wantErr: authn.ErrInvalidCredential},
		// 有方案但无内容，验证同样归类为无效凭证。
		{name: "missing value", credential: authn.Credential{Scheme: "Bearer", Value: " "}, wantErr: authn.ErrInvalidCredential},
		// 完整提供另一方案，验证此时区分不支持的凭证方案。
		{name: "unsupported scheme", credential: authn.Credential{Scheme: "Basic", Value: "fixture"}, wantErr: authn.ErrUnsupportedScheme},
		// Bearer 左侧有空格，验证认证器保持方案原文而不裁剪。
		{name: "scheme with leading space", credential: authn.Credential{Scheme: " Bearer", Value: "fixture"}, wantErr: authn.ErrUnsupportedScheme},
		// 提供不能解析成 JWT 的文本，验证返回无效凭证和空身份。
		{name: "malformed token", credential: authn.Credential{Scheme: "Bearer", Value: "fixture"}, wantErr: authn.ErrInvalidCredential},
		// 预先取消上下文，即使凭证缺失也应优先返回取消原因。
		{name: "canceled before validation", canceled: true, wantErr: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			if test.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := provider.Authenticate(ctx, test.credential)
			assertJWTAuthentication(t, got, err, authn.Principal{}, test.wantErr)
		})
	}
}

func TestAuthenticateExpirationBoundary(t *testing.T) {
	config := jwtTestConfig()
	provider := newJWTTestProvider(t, config, jwtTestNamespaces())
	synctest.Test(t, func(t *testing.T) {
		want := authn.Principal{Kind: authn.PrincipalKindService, Namespace: jwtServiceNamespace, SubjectID: "service-1"}
		issued, err := provider.Issue(t.Context(), want)
		assertJWTIssueResult(t, issued, err, nil)

		// 刚签发的凭证应立刻生效，混合大小写的 Bearer 方案仍有效。
		credential := issued.Credential
		credential.Scheme = "bEaReR"
		got, err := provider.Authenticate(t.Context(), credential)
		assertJWTAuthentication(t, got, err, want, nil)

		// 推进到失效前 1 纳秒，验证临界点之前仍接受凭证。
		time.Sleep(config.TokenTTL - time.Nanosecond)
		got, err = provider.Authenticate(t.Context(), credential)
		assertJWTAuthentication(t, got, err, want, nil)

		// 恰好到失效时刻应返回过期错误，不返回原身份。
		time.Sleep(time.Nanosecond)
		got, err = provider.Authenticate(t.Context(), credential)
		assertJWTAuthentication(t, got, err, authn.Principal{}, authn.ErrExpiredCredential)
	})
}

func TestAuthenticateRejectsDifferentSigningKey(t *testing.T) {
	config := jwtTestConfig()
	provider := newJWTTestProvider(t, config, jwtTestNamespaces())

	// 相同签发方和接收方使用不同测试密钥，验证凭证仍须通过当前组件的签名校验。
	config.SigningKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	other := newJWTTestProvider(t, config, jwtTestNamespaces())
	synctest.Test(t, func(t *testing.T) {
		principal := authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: "user-1"}
		issued, err := other.Issue(t.Context(), principal)
		assertJWTIssueResult(t, issued, err, nil)

		got, err := provider.Authenticate(t.Context(), issued.Credential)
		assertJWTAuthentication(t, got, err, authn.Principal{}, authn.ErrInvalidCredential)
	})
}

type fixtureJWTClaims struct {
	Kind      authn.PrincipalKind `json:"kind"`
	Namespace string              `json:"namespace"`
	jwtlib.RegisteredClaims
}

type authenticateJWTAlgorithmCase struct {
	name       string
	method     jwtlib.SigningMethod
	signingKey any
	wantErr    error
}

func TestAuthenticateSigningAlgorithms(t *testing.T) {
	config := jwtTestConfig()
	provider := newJWTTestProvider(t, config, jwtTestNamespaces())
	key := decodeJWTTestKey(t, config)
	tests := []authenticateJWTAlgorithmCase{
		// 合法密钥与完整有效声明使用 HS256，作为同一组凭证的成功对照。
		{name: "accepts HS256", method: jwtlib.SigningMethodHS256, signingKey: key},
		// 同一合法密钥改用 HS384，验证签名算法不在允许范围时仍须拒绝。
		{name: "rejects HS384", method: jwtlib.SigningMethodHS384, signingKey: key, wantErr: authn.ErrInvalidCredential},
		// 同一合法密钥改用 HS512，验证不能因属于 HMAC 算法就接受凭证。
		{name: "rejects HS512", method: jwtlib.SigningMethodHS512, signingKey: key, wantErr: authn.ErrInvalidCredential},
		// 显式允许测试库生成合法的 none 凭证，验证认证器不会接受无签名身份。
		{name: "rejects none", method: jwtlib.SigningMethodNone, signingKey: jwtlib.UnsafeAllowNoneSignatureType, wantErr: authn.ErrInvalidCredential},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now().UTC()
				principal := authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: "user-1"}
				claims := fixtureJWTClaims{
					Kind: principal.Kind, Namespace: principal.Namespace,
					RegisteredClaims: jwtlib.RegisteredClaims{
						Issuer: config.Issuer, Audience: jwtlib.ClaimStrings{config.Audience}, Subject: principal.SubjectID,
						IssuedAt: jwtlib.NewNumericDate(now.Add(-time.Minute)), NotBefore: jwtlib.NewNumericDate(now.Add(-time.Minute)),
						ExpiresAt: jwtlib.NewNumericDate(now.Add(time.Minute)),
					},
				}
				token := jwtlib.NewWithClaims(test.method, claims)
				token.Header["typ"] = "JWT"
				value, err := token.SignedString(test.signingKey)
				if err != nil {
					t.Fatalf("sign %s fixture credential: %v", test.method.Alg(), err)
				}

				got, err := provider.Authenticate(t.Context(), authn.Credential{Scheme: "Bearer", Value: value})
				if test.wantErr != nil {
					principal = authn.Principal{}
				}
				assertJWTAuthentication(t, got, err, principal, test.wantErr)
			})
		})
	}
}

type authenticateJWTClaimsCase struct {
	name    string
	change  func(*fixtureJWTClaims, time.Time)
	typ     string
	wantErr error
}

func TestAuthenticateClaims(t *testing.T) {
	config := jwtTestConfig()
	provider := newJWTTestProvider(t, config, jwtTestNamespaces())
	tests := []authenticateJWTClaimsCase{
		// 测试密钥签发的独立基线凭证应还原原有身份字段。
		{name: "valid claims"},
		// 签名与声明都正确但类型属于另一种协议，验证类型字段也参与验证。
		{name: "different token type", typ: "fixture-type", wantErr: authn.ErrInvalidCredential},
		// 其他签发方的凭证不属于本组件的信任范围。
		{name: "incorrect issuer", change: func(c *fixtureJWTClaims, _ time.Time) { c.Issuer = "other-issuer" }, wantErr: authn.ErrInvalidCredential},
		// 签发给其他接收方的凭证不能用于当前接收方。
		{name: "incorrect audience", change: func(c *fixtureJWTClaims, _ time.Time) { c.Audience = jwtlib.ClaimStrings{"other-audience"} }, wantErr: authn.ErrInvalidCredential},
		// 过期与接收方不匹配同时出现时应归为无效凭证。
		{name: "expired with incorrect audience", change: func(c *fixtureJWTClaims, now time.Time) {
			c.Audience = jwtlib.ClaimStrings{"other-audience"}
			c.ExpiresAt = jwtlib.NewNumericDate(now.Add(-time.Second))
		}, wantErr: authn.ErrInvalidCredential},
		// 声明缺少签发时间，即使其他字段有效也应被拒绝。
		{name: "missing issued at", change: func(c *fixtureJWTClaims, _ time.Time) { c.IssuedAt = nil }, wantErr: authn.ErrInvalidCredential},
		// 声明缺少生效时间，验证必填的时间声明会被检查。
		{name: "missing not before", change: func(c *fixtureJWTClaims, _ time.Time) { c.NotBefore = nil }, wantErr: authn.ErrInvalidCredential},
		// 声明缺少失效时间，验证凭证不能无限期有效。
		{name: "missing expiry", change: func(c *fixtureJWTClaims, _ time.Time) { c.ExpiresAt = nil }, wantErr: authn.ErrInvalidCredential},
		// 签发时间在当前时刻之后，即使生效字段已满足也应被拒绝。
		{name: "future issued at", change: func(c *fixtureJWTClaims, now time.Time) { c.IssuedAt = jwtlib.NewNumericDate(now.Add(time.Second)) }, wantErr: authn.ErrInvalidCredential},
		// 生效时间在当前时刻之后，验证尚未生效的凭证被拒绝。
		{name: "future not before", change: func(c *fixtureJWTClaims, now time.Time) { c.NotBefore = jwtlib.NewNumericDate(now.Add(time.Second)) }, wantErr: authn.ErrInvalidCredential},
		// 生效时间早于签发时间且两者均在过去，验证认证器补充检查时间关系。
		{name: "not before precedes issue", change: func(c *fixtureJWTClaims, now time.Time) {
			c.NotBefore = jwtlib.NewNumericDate(now.Add(-2 * time.Minute))
		}, wantErr: authn.ErrInvalidCredential},
		// 失效时间等于生效时间没有有效区间，应归为无效而非单纯过期。
		{name: "empty validity interval", change: func(c *fixtureJWTClaims, _ time.Time) { c.ExpiresAt = c.NotBefore }, wantErr: authn.ErrInvalidCredential},
		// 有效签名也要求身份完整，缺少主体时返回空身份和无效错误。
		{name: "missing subject", change: func(c *fixtureJWTClaims, _ time.Time) { c.Subject = "" }, wantErr: authn.ErrInvalidCredential},
		// 只更换命名空间，验证用户不能使用登记给服务类别的范围。
		{name: "scope belongs to another kind", change: func(c *fixtureJWTClaims, _ time.Time) { c.Namespace = jwtServiceNamespace }, wantErr: authn.ErrInvalidCredential},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now().UTC()
				principal := authn.Principal{Kind: authn.PrincipalKindUser, Namespace: jwtUserNamespace, SubjectID: "user-1"}
				claims := fixtureJWTClaims{
					Kind: principal.Kind, Namespace: principal.Namespace,
					RegisteredClaims: jwtlib.RegisteredClaims{
						Issuer: config.Issuer, Audience: jwtlib.ClaimStrings{config.Audience}, Subject: principal.SubjectID,
						IssuedAt: jwtlib.NewNumericDate(now.Add(-time.Minute)), NotBefore: jwtlib.NewNumericDate(now.Add(-time.Minute)),
						ExpiresAt: jwtlib.NewNumericDate(now.Add(time.Minute)),
					},
				}
				if test.change != nil {
					test.change(&claims, now)
				}
				token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
				if test.typ != "" {
					token.Header["typ"] = test.typ
				}
				value, err := token.SignedString(decodeJWTTestKey(t, config))
				if err != nil {
					t.Fatal("could not sign fixture credential")
				}
				got, err := provider.Authenticate(t.Context(), authn.Credential{Scheme: "Bearer", Value: value})
				if test.wantErr != nil {
					principal = authn.Principal{}
				}
				assertJWTAuthentication(t, got, err, principal, test.wantErr)
			})
		})
	}
}

func jwtTestNamespaces() map[authn.PrincipalKind][]string {
	return map[authn.PrincipalKind][]string{
		authn.PrincipalKindUser:    {jwtUserNamespace},
		authn.PrincipalKindService: {jwtServiceNamespace},
	}
}

func newJWTTestProvider(t *testing.T, config jwt.Config, namespaces map[authn.PrincipalKind][]string) *jwt.Provider {
	t.Helper()
	provider, err := jwt.New(config, namespaces)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return provider
}

func decodeJWTTestKey(t *testing.T, config jwt.Config) []byte {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(config.SigningKey)
	if err != nil {
		t.Fatal("could not decode fixture signing key")
	}
	return key
}

func assertJWTIssueResult(t *testing.T, issued authn.IssuedCredential, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Issue() error = %v, want %v", err, want)
	}
	if want != nil && issued != (authn.IssuedCredential{}) {
		t.Error("Issue() returned a partial credential")
	}
	if want == nil && (issued.Credential.Scheme != "Bearer" || issued.Credential.Value == "") {
		t.Error("Issue() did not return a complete Bearer credential")
	}
}

func assertJWTIssuedClaims(t *testing.T, config jwt.Config, principal authn.Principal, issued authn.IssuedCredential, wantIssuedAt time.Time) {
	t.Helper()
	wantExpiry := wantIssuedAt.Add(config.TokenTTL)
	if issued.IssuedAt != wantIssuedAt || issued.ExpiresAt != wantExpiry {
		t.Errorf("Issue() times = %v and %v, want %v and %v", issued.IssuedAt, issued.ExpiresAt, wantIssuedAt, wantExpiry)
	}
	var claims fixtureJWTClaims
	token, err := jwtlib.ParseWithClaims(issued.Credential.Value, &claims, func(*jwtlib.Token) (any, error) {
		return decodeJWTTestKey(t, config), nil
	}, jwtlib.WithValidMethods([]string{"HS256"}))
	if err != nil || token == nil || !token.Valid {
		t.Fatal("Issue() did not produce a valid HS256 credential")
	}
	if token.Header["typ"] != "JWT" || claims.Kind != principal.Kind || claims.Namespace != principal.Namespace || claims.Subject != principal.SubjectID {
		t.Error("Issue() changed token type or identity claims")
	}
	if claims.Issuer != config.Issuer || len(claims.Audience) != 1 || claims.Audience[0] != config.Audience {
		t.Error("Issue() did not encode the configured issuer and audience")
	}
	if claims.IssuedAt == nil || !claims.IssuedAt.Time.Equal(wantIssuedAt) ||
		claims.NotBefore == nil || !claims.NotBefore.Time.Equal(wantIssuedAt) ||
		claims.ExpiresAt == nil || !claims.ExpiresAt.Time.Equal(wantExpiry) {
		t.Error("Issue() did not encode the expected validity interval")
	}
}

func assertJWTAuthentication(t *testing.T, got authn.Principal, err error, want authn.Principal, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Errorf("Authenticate() error = %v, want %v", err, wantErr)
	}
	if got != want {
		t.Errorf("Authenticate() principal = %+v, want %+v", got, want)
	}
}
