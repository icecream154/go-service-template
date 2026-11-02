package requests_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-service-template/app/http/middleware/request_context"
	"go-service-template/app/http/requests"
	"go-service-template/app/utils/response"

	"github.com/gin-gonic/gin"
)

type lookupRequest struct {
	ID int `form:"id" binding:"required,gt=0"`
}

func TestBindingStopsInvalidRequestsAndPassesTypedDTO(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
		valid bool
	}{
		{name: "missing"},
		{name: "wrong type", query: "?id=secret-invalid-value"},
		{name: "valid", query: "?id=5", valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			engine := gin.New()
			engine.Use(request_context.CheckRequestContext())
			engine.GET("/lookup", requests.Bind[lookupRequest](), func(c *gin.Context) {
				called = true
				dto, ok := requests.Get[lookupRequest](c)
				if !ok || dto.ID != 5 {
					t.Fatalf("typed DTO = %+v, ok = %v", dto, ok)
				}
				c.Status(http.StatusNoContent)
			})
			writer := httptest.NewRecorder()
			engine.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/lookup"+test.query, nil))
			if called != test.valid {
				t.Fatalf("controller called = %v, want %v", called, test.valid)
			}
			if writer.Header().Get("TraceId") == "" {
				t.Fatal("response is missing trace")
			}
			if test.valid {
				if writer.Code != http.StatusNoContent {
					t.Fatalf("status = %d", writer.Code)
				}
				return
			}
			var payload response.Response
			if err := json.Unmarshal(writer.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if writer.Code != http.StatusOK || payload.Code != response.CodeInvalidParameter || payload.Msg != "参数校验失败" || payload.Data != nil {
				t.Fatalf("invalid request response = %d %s", writer.Code, writer.Body.String())
			}
		})
	}
}
