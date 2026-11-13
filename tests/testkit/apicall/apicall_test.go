package apicall

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAndAssertionsDoNotLeakSensitiveData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("TraceId", "0123456789abcdef0123456789abcdef")
		if r.URL.Path == "/ok" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"item":"book"},"error":null}`))
			return
		}
		if r.URL.Path == "/sensitive" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"access_token":"SUPER_SECRET_JWT_VALUE"},"error":null}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"data":null,"error":{"code":"INVALID_ARGUMENT","message":"user_id: required","details":[{"field":"user_id","reason":"required"}]}}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 0)

	type itemDTO struct {
		Item string `json:"item"`
	}
	got := RequireSuccess[itemDTO](t, client.Get(t, "/ok"))
	if got.Item != "book" {
		t.Fatalf("got.Item = %q, want book", got.Item)
	}

	errResp := client.Get(t, "/bad")
	errInfo := RequireError(t, errResp, http.StatusBadRequest, "INVALID_ARGUMENT", "")
	RequireFieldError(t, errInfo, "user_id")

	// 验证脱敏诊断函数不会把 data 内的敏感 token 带入错误摘要。
	sensitiveResp := client.Get(t, "/sensitive")
	summary := safeDiagnosticSummary(sensitiveResp)
	if strings.Contains(summary, "SUPER_SECRET_JWT_VALUE") {
		t.Fatalf("safeDiagnosticSummary leaked sensitive token: %s", summary)
	}
}
