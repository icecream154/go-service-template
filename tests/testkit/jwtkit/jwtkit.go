// Package jwtkit 提供与具体业务身份枚举解耦的测试 JWT 密钥生成、签发（含过期与异构签名）和验签工具。
package jwtkit

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"go-service-template/internal/authn"
	authnjwt "go-service-template/internal/authn/jwt"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// Options 定义创建测试轮次独立 JWT 信任域的参数。
type Options struct {
	Issuer            string
	Audience          string
	TokenTTL          time.Duration
	AllowedNamespaces map[authn.PrincipalKind][]string
}

// Authority 持有单轮测试随机生成的 HS256 签名密钥及对应的 Provider。
type Authority struct {
	config     authnjwt.Config
	signingKey []byte
	provider   *authnjwt.Provider
}

type customClaims struct {
	Kind      authn.PrincipalKind `json:"kind"`
	Namespace string              `json:"namespace"`
	jwtlib.RegisteredClaims
}

// NewAuthority 生成密码学随机 32 字节密钥并初始化测试 JWT 签发与验签机构。
func NewAuthority(opts Options) (*Authority, error) {
	issuer := opts.Issuer
	if issuer == "" {
		issuer = "testkit-jwt-issuer"
	}
	audience := opts.Audience
	if audience == "" {
		audience = "testkit-jwt-audience"
	}
	ttl := opts.TokenTTL
	if ttl == 0 {
		ttl = 10 * time.Minute
	}

	var keyBytes [32]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		return nil, fmt.Errorf("generate random JWT key: %w", err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(keyBytes[:])

	cfg := authnjwt.Config{
		Issuer:     issuer,
		Audience:   audience,
		SigningKey: encodedKey,
		TokenTTL:   ttl,
	}
	provider, err := authnjwt.New(cfg, opts.AllowedNamespaces)
	if err != nil {
		return nil, err
	}
	return &Authority{
		config:     cfg,
		signingKey: append([]byte(nil), keyBytes[:]...),
		provider:   provider,
	}, nil
}

// Config 返回可序列化写入 authn/jwt/test.yml 的完整配置副本。
func (a *Authority) Config() authnjwt.Config {
	return a.config
}

// Issue 使用与被测服务完全一致的密钥与算法签发合法 Bearer JWT。
func (a *Authority) Issue(ctx context.Context, principal authn.Principal) (authn.IssuedCredential, error) {
	return a.provider.Issue(ctx, principal)
}

// IssueExpired 使用相同信任域密钥签发一份结构完整但已过期的 JWT，用于验证认证层对过期凭证的精确识别。
func (a *Authority) IssueExpired(principal authn.Principal) (string, error) {
	now := time.Now().UTC().Truncate(time.Second)
	issuedAt := now.Add(-2 * a.config.TokenTTL)
	expiredAt := now.Add(-1 * a.config.TokenTTL)
	if !expiredAt.After(issuedAt) {
		issuedAt = now.Add(-2 * time.Minute)
		expiredAt = now.Add(-1 * time.Minute)
	}
	return a.signCustom(principal, issuedAt, issuedAt, expiredAt, a.signingKey)
}

// IssueWrongKey 使用另一把独立随机密钥签发结构与声明合法但签名不匹配的 JWT。
func (a *Authority) IssueWrongKey(principal authn.Principal) (string, error) {
	var foreignKey [32]byte
	if _, err := rand.Read(foreignKey[:]); err != nil {
		return "", fmt.Errorf("generate foreign JWT key: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return a.signCustom(principal, now, now, now.Add(a.config.TokenTTL), foreignKey[:])
}

// Authenticate 验证服务端返回的原始 JWT 字符串（不含 "Bearer " 前缀），返回解析出的可信身份。
func (a *Authority) Authenticate(ctx context.Context, rawToken string) (authn.Principal, error) {
	if rawToken == "" {
		return authn.Principal{}, errors.New("raw JWT token must not be empty")
	}
	return a.provider.Authenticate(ctx, authn.Credential{
		Scheme: "Bearer",
		Value:  rawToken,
	})
}

func (a *Authority) signCustom(principal authn.Principal, iat, nbf, exp time.Time, key []byte) (string, error) {
	claims := customClaims{
		Kind:      principal.Kind,
		Namespace: principal.Namespace,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Issuer:    a.config.Issuer,
			Subject:   principal.SubjectID,
			Audience:  jwtlib.ClaimStrings{a.config.Audience},
			IssuedAt:  jwtlib.NewNumericDate(iat),
			NotBefore: jwtlib.NewNumericDate(nbf),
			ExpiresAt: jwtlib.NewNumericDate(exp),
		},
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	return token.SignedString(key)
}
