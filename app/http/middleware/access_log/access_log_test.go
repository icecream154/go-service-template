package access_log_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-service-template/app/http/middleware/access_log"
	"go-service-template/app/http/middleware/recovery"
	"go-service-template/app/http/middleware/request_context"
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRecoveredPanicHasSafeResponseAndFinalAccessStatus(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)
	engine := gin.New()
	engine.Use(access_log.Logger(log), recovery.Recovery(log), request_context.CheckRequestContext())
	engine.GET("/panic", func(c *gin.Context) { panic("internal-only-panic") })
	request := httptest.NewRequest(http.MethodGet, "/panic?token=query-secret", strings.NewReader("body-secret"))
	request.Header.Set("Authorization", "token-secret")
	request.Header.Set("TraceId", "panic-trace")
	writer := httptest.NewRecorder()
	engine.ServeHTTP(writer, request)
	var payload response.Response
	if err := json.Unmarshal(writer.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if writer.Code != http.StatusInternalServerError || payload.Code != response.CodeInternalError || payload.Msg != "内部服务错误" || payload.Data != nil {
		t.Fatalf("response = %d %s", writer.Code, writer.Body.String())
	}
	if writer.Header().Get("TraceId") != "panic-trace" {
		t.Fatal("recovery response lost trace")
	}
	access := logs.FilterMessage("HTTP request").All()
	if len(access) != 1 {
		t.Fatalf("access logs = %d, want 1", len(access))
	}
	fields := access[0].ContextMap()
	if fields["status"] != int64(500) || fields["path"] != "/panic" || fields["trace_id"] != "panic-trace" {
		t.Fatalf("access fields = %+v", fields)
	}
	if logs.FilterMessage("HTTP panic").Len() != 1 {
		t.Fatal("panic diagnostic was not logged")
	}
	serialized, err := json.Marshal(logs.All())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"query-secret", "body-secret", "token-secret"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("request secret %q leaked to logs", secret)
		}
	}
}
