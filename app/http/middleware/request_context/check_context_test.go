package request_context_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-service-template/app/http/middleware/request_context"

	"github.com/gin-gonic/gin"
)

func TestTraceHeaderIsGeneratedPreservedOrRejected(t *testing.T) {
	for _, test := range []struct {
		name  string
		trace string
		valid bool
	}{
		{name: "generated", valid: true},
		{name: "preserved", trace: "client-trace_1:part.2", valid: true},
		{name: "unsafe", trace: "unsafe trace"},
		{name: "too long", trace: strings.Repeat("a", 129)},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			engine := gin.New()
			engine.Use(request_context.CheckRequestContext())
			engine.GET("/trace", func(c *gin.Context) {
				called = true
				meta := request_context.GetRequestContext(c)
				if meta.TraceId == "" || meta.StartUnixNano == 0 {
					t.Fatalf("metadata = %+v", meta)
				}
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/trace", nil)
			request.Header.Set("TraceId", test.trace)
			writer := httptest.NewRecorder()
			engine.ServeHTTP(writer, request)
			trace := writer.Header().Get("TraceId")
			if trace == "" || called != test.valid {
				t.Fatalf("trace = %q, handler called = %v", trace, called)
			}
			if test.valid && test.trace != "" && trace != test.trace {
				t.Fatalf("trace = %q, want %q", trace, test.trace)
			}
			if !test.valid && trace == test.trace {
				t.Fatal("invalid client trace was reflected")
			}
		})
	}
}
