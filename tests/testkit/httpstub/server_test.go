package httpstub

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServerExpectCaptureAndVerifyReset(t *testing.T) {
	stub := NewServer()
	defer stub.Close()

	route := stub.Expect(http.MethodPost, "/api/v1/demo/echo", JSONResponse(http.StatusOK, map[string]any{
		"ok": true,
	}))

	req, err := http.NewRequest(http.MethodPost, stub.URL()+"/api/v1/demo/echo?lang=zh", bytes.NewBufferString(`{"hello":"world"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Trace-Check", "trace-123")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("unexpected stub response: status=%d body=%s", resp.StatusCode, body)
	}

	calls := route.RequireCallCount(t, 1)
	if calls[0].RawQuery != "lang=zh" || calls[0].Header.Get("X-Trace-Check") != "trace-123" || string(calls[0].Body) != `{"hello":"world"}` {
		t.Fatalf("unexpected recorded request: %+v", calls[0])
	}
	if err := stub.VerifyAndReset(); err != nil {
		t.Fatalf("VerifyAndReset should pass when only expected route was called, got: %v", err)
	}

	// 重置后同一路径未重新注册，再次请求应返回 502 并在 VerifyAndReset 中报告违规。
	resp2, err := http.Get(stub.URL() + "/api/v1/demo/unregistered")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadGateway {
		t.Fatalf("unregistered route status = %d, want 502", resp2.StatusCode)
	}
	if err := stub.VerifyAndReset(); err == nil || !strings.Contains(err.Error(), "GET /api/v1/demo/unregistered") {
		t.Fatalf("VerifyAndReset should report unexpected call, got: %v", err)
	}
	// 再次重置后状态应已恢复干净。
	if err := stub.VerifyAndReset(); err != nil {
		t.Fatalf("second VerifyAndReset after clear = %v, want nil", err)
	}
}
