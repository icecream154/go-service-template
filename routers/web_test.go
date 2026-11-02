package routers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"go-service-template/app/composition"
	"go-service-template/app/domains/health/services"
	binding "go-service-template/app/http/requests"
	"go-service-template/config"
	"go-service-template/routers"
)

func httpConfig() config.HTTPConfig {
	return config.HTTPConfig{Address: ":8080", ReadHeaderTimeout: time.Second,
		WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second}
}

func TestCompleteHealthHTTPContract(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	router, err := routers.InitWebRouter(composition.New(), httpConfig(), zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	cases := []struct {
		method, path, trace string
		status, code        int
	}{
		{"GET", "/health", "caller-1", 200, 200},
		{"GET", "/v1/health", "", 200, 200},
		{"HEAD", "/health", "head-1", 200, 200},
		{"HEAD", "/v1/health", "head-2", 200, 200},
		{"POST", "/health", "method-1", 405, 405},
		{"GET", "/missing", "missing-1", 404, 404},
		{"GET", "/health/", "slash-1", 404, 404},
		{"GET", "/health", "invalid trace", 200, -400300},
	}
	for _, test := range cases {
		t.Run(test.method+test.path+test.trace, func(t *testing.T) {
			req, err := http.NewRequest(test.method, server.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("TraceId", test.trace)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != test.status {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			trace := resp.Header.Get("TraceId")
			if trace == "" || (test.trace != "" && test.code != -400300 && trace != test.trace) {
				t.Fatalf("TraceId=%q", trace)
			}
			if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
				t.Fatal("missing JSON content type")
			}
			if test.method == "HEAD" {
				if len(body) != 0 {
					t.Fatalf("HEAD body=%s", body)
				}
				return
			}
			var result struct {
				Code int
				Msg  string
				Data json.RawMessage
			}
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != test.code {
				t.Fatalf("business code=%d", result.Code)
			}
			if test.code == 200 {
				var data map[string]string
				if err := json.Unmarshal(result.Data, &data); err != nil {
					t.Fatal(err)
				}
				if result.Msg != "Success" || len(data) != 1 || data["status"] != "ok" {
					t.Fatalf("unexpected success: %s", body)
				}
			} else if string(result.Data) != "null" {
				t.Fatalf("error data=%s", result.Data)
			}
			if test.status == 405 && resp.Header.Get("Allow") != "GET, HEAD" {
				t.Fatalf("Allow=%q", resp.Header.Get("Allow"))
			}
		})
	}
	if count := logs.FilterMessage("HTTP request").Len(); count != len(cases) {
		t.Fatalf("access logs=%d, requests=%d", count, len(cases))
	}
}

type checkFunc func(services.Call) services.Result

func (f checkFunc) Check(call services.Call) services.Result { return f(call) }

func TestRuntimeInjectionAndCallContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "caller-value")
	calls := 0
	service := checkFunc(func(call services.Call) services.Result {
		calls++
		if call.Context.Value(contextKey{}) != "caller-value" || call.Trace.TraceId != "shared-call" || call.Trace.StartUnixNano == 0 {
			t.Fatal("call context was not propagated")
		}
		return services.Result{Code: 200, Message: "Success", Data: services.CheckResult{Status: "injected"}}
	})
	cfg := httpConfig()
	cfg.BasePath = "/api"
	router, err := routers.InitWebRouter(&composition.Runtime{Health: service}, cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/health", "/api/v1/health"} {
		req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
		req.Header.Set("TraceId", "shared-call")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"injected"`) {
			t.Fatalf("response=%s", w.Body)
		}
	}
	if calls != 2 {
		t.Fatalf("service calls=%d", calls)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 404 {
		t.Fatalf("unprefixed path status=%d", w.Code)
	}
}

func TestBindingAndPanicUseCommonMiddleware(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	router, err := routers.InitWebRouter(composition.New(), httpConfig(), zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	type input struct {
		Name string `json:"name" binding:"required"`
	}
	called := false
	router.POST("/binding-test", binding.Bind[input](), func(c *gin.Context) { called = true })
	router.GET("/panic-test", func(c *gin.Context) { panic("internal diagnosis") })
	req := httptest.NewRequest("POST", "/binding-test", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if called || w.Code != 200 || !strings.Contains(w.Body.String(), `"code":-400300`) {
		t.Fatalf("binding did not abort: %s", w.Body)
	}
	req = httptest.NewRequest("GET", "/panic-test?secret=private-query", nil)
	req.Header.Set("Authorization", "private-token")
	req.Header.Set("TraceId", "panic-trace")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":-500100`) || strings.Contains(w.Body.String(), "internal diagnosis") || w.Header().Get("TraceId") != "panic-trace" {
		t.Fatalf("panic response=%d %s", w.Code, w.Body)
	}
	access := logs.FilterMessage("HTTP request").All()
	if len(access) != 2 || access[1].ContextMap()["status"] != int64(500) {
		t.Fatalf("access logs=%v", access)
	}
	for _, entry := range logs.All() {
		encoded, err := json.Marshal(entry.ContextMap())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "private-token") || strings.Contains(string(encoded), "private-query") {
			t.Fatal("request secrets entered logs")
		}
	}
}
