package routers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	healthRouters "go-service-template/app/domains/health/routers"
	"go-service-template/app/domains/health/services"
	"go-service-template/app/http/middleware/request_context"

	"github.com/gin-gonic/gin"
)

type recordingUseCases struct {
	calls []services.Call
}

func (s *recordingUseCases) Check(call services.Call) services.Result {
	s.calls = append(s.calls, call)
	return services.Result{Code: 200, Message: "Success", Data: services.CheckResult{Status: "ok"}}
}

func TestPublicAndVersionedRoutesShareServiceAndPassCall(t *testing.T) {
	service := &recordingUseCases{}
	domain := healthRouters.NewRouter(service)
	engine := gin.New()
	engine.Use(request_context.CheckRequestContext())
	domain.RegisterPublicRoutes(engine.Group(""))
	domain.RegisterV1Routes(engine.Group("/v1"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, path := range []string{"/health", "/v1/health"} {
		request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		request.Header.Set("TraceId", "health-test-trace")
		writer := httptest.NewRecorder()
		engine.ServeHTTP(writer, request)
		if writer.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, writer.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(writer.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		data, ok := payload["data"].(map[string]any)
		if len(payload) != 3 || payload["code"] != float64(200) || payload["msg"] != "Success" || !ok || len(data) != 1 || data["status"] != "ok" {
			t.Fatalf("%s: response = %s", path, writer.Body.String())
		}
	}
	if len(service.calls) != 2 {
		t.Fatalf("service calls = %d, want 2", len(service.calls))
	}
	for _, call := range service.calls {
		if call.Context != ctx || call.Context.Err() != context.Canceled {
			t.Fatal("request context and cancellation did not reach use case")
		}
		if call.Trace == nil || call.Trace.TraceId != "health-test-trace" || call.Trace.StartUnixNano == 0 {
			t.Fatalf("request trace did not reach use case: %+v", call.Trace)
		}
	}
}
