package flow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-service-template/tests/testkit/apicall"
	"go-service-template/tests/testkit/httpstub"
	"go-service-template/tests/testkit/sdkmock"
)

type stubSeedGuard struct {
	frozen bool
}

func (g *stubSeedGuard) Freeze()        { g.frozen = true }
func (g *stubSeedGuard) IsFrozen() bool { return g.frozen }

func TestRunnerStepCheckpointIsolationAndTrail(t *testing.T) {
	sdkCtrl := sdkmock.NewController()
	defer sdkCtrl.Close()
	sdkClient, err := sdkmock.NewClient(sdkCtrl.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer sdkClient.CloseIdleConnections()

	downstream := httpstub.NewServer()
	defer downstream.Close()

	// 模拟一个轻量被测应用：/step1 纯本地返回，/step2 访问下游 HTTP 与 SDK。
	appSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("TraceId", "abcdef0123456789abcdef0123456789")
		if r.URL.Path == "/step1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"token":"SENSITIVE_TOKEN_DO_NOT_LOG"},"error":null}`))
			return
		}
		if r.URL.Path == "/step2" {
			var sdkOut map[string]string
			if err := sdkClient.Invoke(r.Context(), "demo.ai", "Prompt", map[string]string{"q": "hi"}, &sdkOut); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"data":null,"error":{"code":"INTERNAL_ERROR","message":"sdk failed"}}`))
				return
			}
			resp, err := http.Get(downstream.URL() + "/internal/ping")
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil {
					_ = resp.Body.Close()
				}
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"data":null,"error":{"code":"INTERNAL_ERROR","message":"http failed"}}`))
				return
			}
			_ = resp.Body.Close()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"done":true},"error":null}`))
		}
	}))
	defer appSrv.Close()

	guard := &stubSeedGuard{}
	runner := New(t, Config{
		HTTP:      apicall.NewClient(appSrv.URL, 0),
		SDKMock:   sdkCtrl,
		HTTPStubs: map[string]*httpstub.Server{"svc-a": downstream},
	}, WithSeed(guard))

	var step1Passed, step2Passed bool

	runner.Step("01_pure_local_step", func(s *Step) {
		if !guard.IsFrozen() {
			t.Fatal("Runner must automatically freeze bound seed guard upon entering first Step")
		}
		resp := s.HTTP().PostJSON(s.T(), "/step1")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		step1Passed = true
	})

	runner.Step("02_external_dependency_step", func(s *Step) {
		sdkRule := s.ExpectSDK("demo.ai", "Prompt", sdkmock.RespondJSON(map[string]string{"ok": "1"}))
		httpRoute := s.ExpectHTTP("svc-a", http.MethodGet, "/internal/ping", httpstub.JSONResponse(http.StatusOK, map[string]bool{"pong": true}))

		resp := s.HTTP().PostJSON(s.T(), "/step2")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		sdkRule.RequireCallCount(s.T(), 1)
		httpRoute.RequireCallCount(s.T(), 1)
		step2Passed = true
	})

	// Step 2 结束后，其注册的外部预期必须已被自动清空：此时直接请求下游应返回 502。
	postCheckResp, err := http.Get(downstream.URL() + "/internal/ping")
	if err != nil {
		t.Fatal(err)
	}
	_ = postCheckResp.Body.Close()
	if postCheckResp.StatusCode != http.StatusBadGateway {
		t.Fatalf("step-scoped HTTP expectation leaked into subsequent state: status=%d, want 502", postCheckResp.StatusCode)
	}
	_ = downstream.VerifyAndReset()

	if !step1Passed || !step2Passed {
		t.Fatal("expected both steps to execute")
	}

	// 验证轨迹表包含步骤名、HTTP 路径与 TraceId，但绝不包含响应体中的敏感 Token。
	trail := runner.FormatTrail("")
	if !strings.Contains(trail, "01_pure_local_step") || !strings.Contains(trail, "TraceId=abcdef0123456789abcdef0123456789") {
		t.Fatalf("unexpected trail format:\n%s", trail)
	}
	if strings.Contains(trail, "SENSITIVE_TOKEN_DO_NOT_LOG") {
		t.Fatalf("trail leaked response payload token:\n%s", trail)
	}
}
