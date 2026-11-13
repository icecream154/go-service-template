//go:build integration || business

// Package harness 连接通用 tests/testkit 底座与当前项目 cmd/web 服务，为集成测试与业务测试提供零样板环境托管。
package harness

import (
	"strconv"
	"testing"
	"time"

	"go-service-template/app/identity"
	"go-service-template/internal/authn"
	authnjwt "go-service-template/internal/authn/jwt"
	"go-service-template/tests/testkit/jwtkit"
)

// AuthHelper 封装本项目已登记身份命名空间（user/member 与 service/admin）的测试凭证签发和验签。
type AuthHelper struct {
	authority *jwtkit.Authority
}

func newAuthHelper(tokenTTL time.Duration) (*AuthHelper, error) {
	authority, err := jwtkit.NewAuthority(jwtkit.Options{
		Issuer:   "go-service-template/integration-test",
		Audience: "go-service-template",
		TokenTTL: tokenTTL,
		AllowedNamespaces: map[authn.PrincipalKind][]string{
			authn.PrincipalKindUser:    {identity.UserNamespaceMember},
			authn.PrincipalKindService: {identity.ServiceNamespaceAdmin},
		},
	})
	if err != nil {
		return nil, err
	}
	return &AuthHelper{authority: authority}, nil
}

// Config 返回本轮随机密钥对应的 JWT 配置，供配置装配器写入 authn/jwt/test.yml。
func (h *AuthHelper) Config() authnjwt.Config {
	return h.authority.Config()
}

// IssueMemberToken 为指定内部用户 ID 签发合法的 user/member Bearer JWT（返回不含 Bearer 前缀的原文）。
func (h *AuthHelper) IssueMemberToken(t testing.TB, userID int64) string {
	t.Helper()
	issued, err := h.authority.Issue(t.Context(), authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: identity.UserNamespaceMember,
		SubjectID: strconv.FormatInt(userID, 10),
	})
	if err != nil {
		t.Fatalf("issue member token for user %d: %v", userID, err)
	}
	return issued.Credential.Value
}

// IssueRawMemberSubjectToken 允许以自定义 SubjectID 字符串签发 user/member 凭证（用于验证非正整数用户标识被授权层识别为无效成员身份）。
func (h *AuthHelper) IssueRawMemberSubjectToken(t testing.TB, rawSubject string) string {
	t.Helper()
	issued, err := h.authority.Issue(t.Context(), authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: identity.UserNamespaceMember,
		SubjectID: rawSubject,
	})
	if err != nil {
		t.Fatalf("issue custom member subject token: %v", err)
	}
	return issued.Credential.Value
}

// IssueExpiredMemberToken 为指定内部用户 ID 签发结构与密钥合法但已过期的 user/member JWT。
func (h *AuthHelper) IssueExpiredMemberToken(t testing.TB, userID int64) string {
	t.Helper()
	token, err := h.authority.IssueExpired(authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: identity.UserNamespaceMember,
		SubjectID: strconv.FormatInt(userID, 10),
	})
	if err != nil {
		t.Fatalf("issue expired member token: %v", err)
	}
	return token
}

// IssueWrongKeyMemberToken 使用异构密钥签发签名无效的 user/member JWT。
func (h *AuthHelper) IssueWrongKeyMemberToken(t testing.TB, userID int64) string {
	t.Helper()
	token, err := h.authority.IssueWrongKey(authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: identity.UserNamespaceMember,
		SubjectID: strconv.FormatInt(userID, 10),
	})
	if err != nil {
		t.Fatalf("issue wrong-key member token: %v", err)
	}
	return token
}

// IssueServiceAdminToken 为指定调用服务主体签发合法的 service/admin Bearer JWT。
func (h *AuthHelper) IssueServiceAdminToken(t testing.TB, serviceSubject string) string {
	t.Helper()
	issued, err := h.authority.Issue(t.Context(), authn.Principal{
		Kind:      authn.PrincipalKindService,
		Namespace: identity.ServiceNamespaceAdmin,
		SubjectID: serviceSubject,
	})
	if err != nil {
		t.Fatalf("issue service/admin token: %v", err)
	}
	return issued.Credential.Value
}

// AuthenticateToken 使用本轮信任域校验被测服务返回的 JWT（如 createUser 签发的 access_token），返回其携带的身份声明。
func (h *AuthHelper) AuthenticateToken(t testing.TB, rawToken string) authn.Principal {
	t.Helper()
	principal, err := h.authority.Authenticate(t.Context(), rawToken)
	if err != nil {
		t.Fatalf("authenticate issued token failed: %v", err)
	}
	return principal
}
