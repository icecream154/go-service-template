package sdkmock

import (
	"errors"
	"strings"
	"testing"
)

func TestControllerAndClientRoundTrip(t *testing.T) {
	ctrl := NewController()
	defer ctrl.Close()

	client, err := NewClient(ctrl.URL())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.CloseIdleConnections()

	// 1. 验证正常注册调用与入参捕获。
	rule := ctrl.Expect("vendor.demo", "Synthesize", RespondJSON(map[string]any{
		"output": "mocked-text",
	}))

	var out struct {
		Output string `json:"output"`
	}
	if err := client.Invoke(t.Context(), "vendor.demo", "Synthesize", map[string]string{"input": "hi"}, &out); err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}
	if out.Output != "mocked-text" {
		t.Fatalf("out.Output = %q, want %q", out.Output, "mocked-text")
	}
	calls := rule.RequireCallCount(t, 1)
	if !strings.Contains(string(calls[0].ParamsJSON), `"input":"hi"`) {
		t.Fatalf("recorded params = %s, want input hi", calls[0].ParamsJSON)
	}
	if err := ctrl.VerifyAndReset(); err != nil {
		t.Fatalf("VerifyAndReset = %v, want nil", err)
	}

	// 2. 验证受控错误返回。
	ctrl.Expect("vendor.demo", "Synthesize", RespondError("RATE_LIMIT", 429, 1004, "quota exceeded"))
	err = client.Invoke(t.Context(), "vendor.demo", "Synthesize", map[string]string{"input": "hi"}, &out)
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Fault.StatusCode != 429 || callErr.Fault.ProviderCode != 1004 {
		t.Fatalf("expected structured CallError(429, 1004), got: %v", err)
	}
	if err := ctrl.VerifyAndReset(); err != nil {
		t.Fatalf("VerifyAndReset after expected fault = %v, want nil", err)
	}

	// 3. 验证未注册调用被拦截并在 VerifyAndReset 报告违规。
	err = client.Invoke(t.Context(), "vendor.unregistered", "Action", map[string]int{"x": 1}, nil)
	if err == nil {
		t.Fatal("unregistered SDK call must fail")
	}
	if verifyErr := ctrl.VerifyAndReset(); verifyErr == nil || !strings.Contains(verifyErr.Error(), "vendor.unregistered.Action") {
		t.Fatalf("VerifyAndReset should report unexpected SDK call, got: %v", verifyErr)
	}
}
