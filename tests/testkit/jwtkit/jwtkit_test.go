package jwtkit

import (
	"errors"
	"testing"
	"time"

	"go-service-template/internal/authn"
)

func TestAuthorityIssueAndFailureVariants(t *testing.T) {
	auth, err := NewAuthority(Options{
		Issuer:   "unit-issuer",
		Audience: "unit-audience",
		TokenTTL: 5 * time.Minute,
		AllowedNamespaces: map[authn.PrincipalKind][]string{
			authn.PrincipalKindUser:    {"alpha"},
			authn.PrincipalKindService: {"ops"},
		},
	})
	if err != nil {
		t.Fatalf("NewAuthority failed: %v", err)
	}

	target := authn.Principal{
		Kind:      authn.PrincipalKindUser,
		Namespace: "alpha",
		SubjectID: "9001",
	}
	issued, err := auth.Issue(t.Context(), target)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	parsed, err := auth.Authenticate(t.Context(), issued.Credential.Value)
	if err != nil || parsed != target {
		t.Fatalf("Authenticate(valid) = (%+v, %v), want (%+v, nil)", parsed, err, target)
	}

	expiredToken, err := auth.IssueExpired(target)
	if err != nil {
		t.Fatalf("IssueExpired failed: %v", err)
	}
	if _, err := auth.Authenticate(t.Context(), expiredToken); !errors.Is(err, authn.ErrExpiredCredential) {
		t.Fatalf("Authenticate(expired) err = %v, want ErrExpiredCredential", err)
	}

	wrongKeyToken, err := auth.IssueWrongKey(target)
	if err != nil {
		t.Fatalf("IssueWrongKey failed: %v", err)
	}
	if _, err := auth.Authenticate(t.Context(), wrongKeyToken); !errors.Is(err, authn.ErrInvalidCredential) {
		t.Fatalf("Authenticate(wrongKey) err = %v, want ErrInvalidCredential", err)
	}
}
