package jwt

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"go-service-template/internal/authn"
)

func TestInit(t *testing.T) {
	isolateSharedJWTProvider(t)
	config := Config{
		Issuer: "runtime-test-issuer", Audience: "runtime-test-audience",
		SigningKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("r", 32))),
		TokenTTL:   time.Minute,
	}
	namespaces := map[authn.PrincipalKind][]string{authn.PrincipalKindUser: {"runtime-test-members"}}

	// 先以缺少命名空间的配置初始化，验证失败不会发布部分组件。
	if err := Init(config, nil); err == nil {
		t.Fatal("Init() error = nil with missing namespaces")
	}
	assertCurrentJWTPanics(t)

	// 在初始化失败后再次传入有效参数，验证默认组件仍可正常发布。
	if err := Init(config, namespaces); err != nil {
		t.Fatalf("Init() after failure = %v, want nil", err)
	}
	original := Current()

	// 用另一份有效配置重复初始化，验证拒绝替换已发布的组件。
	config.Issuer = "replacement-issuer"
	if err := Init(config, namespaces); err == nil {
		t.Error("Init() error = nil on duplicate initialization")
	}
	if got := Current(); got != original {
		t.Error("Current() changed after duplicate initialization")
	}
}

func TestCurrent(t *testing.T) {
	isolateSharedJWTProvider(t)

	// 在任何初始化之前读取默认组件，验证能尽早暴露启动顺序错误。
	assertCurrentJWTPanics(t)
}

func isolateSharedJWTProvider(t *testing.T) {
	t.Helper()
	shared.Lock()
	original := shared.provider
	shared.provider = nil
	shared.Unlock()
	t.Cleanup(func() {
		shared.Lock()
		shared.provider = original
		shared.Unlock()
	})
}

func assertCurrentJWTPanics(t *testing.T) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("Current() did not panic before successful initialization")
		}
	}()
	Current()
}
