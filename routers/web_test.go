package routers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
	"go-service-template/internal/response"
	"go-service-template/routers"

	"github.com/gin-gonic/gin"
)

const userProfileQueryPath = "/api/v1/user/userProfile/queryUserProfileByUserId"

func TestUserProfileQueryRejectsInvalidUserID(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"missing", ""},
		{"empty", "?user_id="},
		{"zero", "?user_id=0"},
		{"negative", "?user_id=-1"},
		{"text", "?user_id=abc"},
		{"fraction", "?user_id=1.5"},
		{"positive overflow", "?user_id=9223372036854775808"},
		{"negative overflow", "?user_id=-9223372036854775809"},
	}
	router := routers.New()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, userProfileQueryPath+test.query, nil))
			failure := assertFailure(t, recorder, http.StatusBadRequest, "INVALID_ARGUMENT", "user_id: must be a positive 64-bit integer")
			var details []response.FieldError
			if err := json.Unmarshal(failure["details"], &details); err != nil {
				t.Fatalf("decode field details: %v", err)
			}
			if len(details) != 1 || details[0].Field != "user_id" || details[0].Reason != "must be a positive 64-bit integer" {
				t.Fatalf("unexpected field details: %#v", details)
			}
			if len(failure) != 3 {
				t.Fatalf("unexpected error fields: %s", recorder.Body.String())
			}
		})
	}
}

func TestUnknownRoutesUseCommonErrorWithoutBusinessCode(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"unknown route", http.MethodGet, "/unknown"},
		{"removed health", http.MethodGet, "/health"},
		{"removed versioned health", http.MethodGet, "/api/v1/health"},
		{"previous user route", http.MethodGet, "/api/v1/users/123"},
		{"unsupported method", http.MethodPost, userProfileQueryPath},
	}
	router := routers.New()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			failure := assertFailure(t, recorder, http.StatusNotFound, "NOT_FOUND", "route not found")
			if len(failure) != 2 {
				t.Fatalf("route error must omit business code and details: %s", recorder.Body.String())
			}
		})
	}
}

func TestRecoveryUsesSafeInternalError(t *testing.T) {
	router := routers.New()
	router.GET("/test/panic", func(*gin.Context) {
		panic("private panic explanation")
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test/panic", nil))
	failure := assertFailure(t, recorder, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	if len(failure) != 2 || strings.Contains(recorder.Body.String(), "private panic explanation") {
		t.Fatalf("panic response leaked internal content: %s", recorder.Body.String())
	}
}

func TestRecoveryPreservesWrittenResponseAndAbortsHandlers(t *testing.T) {
	router := routers.New()
	nextHandlerCalled := false
	router.GET("/test/written-panic", func(ctx *gin.Context) {
		ctx.JSON(http.StatusAccepted, gin.H{"accepted": true})
		panic("private panic after response")
	}, func(ctx *gin.Context) {
		nextHandlerCalled = true
		ctx.String(http.StatusOK, "unexpected response")
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test/written-panic", nil))
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != `{"accepted":true}` {
		t.Fatalf("committed response changed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if nextHandlerCalled {
		t.Fatal("handler after panic was executed")
	}
}

func TestSuccessPreservesUserProfileDTO(t *testing.T) {
	router := routers.New()
	router.GET("/test/profile-success", func(ctx *gin.Context) {
		response.Success(ctx, userprofiledto.QueryUserProfileByUserIDResponse{
			UserID:    9223372036854775807,
			UserName:  "example",
			IsEnabled: true,
			Version:   3,
		})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test/profile-success", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", recorder.Code)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode success: %v", err)
	}
	if len(envelope) != 2 || string(envelope["error"]) != "null" {
		t.Fatalf("unexpected success envelope: %s", recorder.Body.String())
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if len(data) != 5 || string(data["user_id"]) != `"9223372036854775807"` || string(data["user_name"]) != `"example"` || string(data["avatar_url"]) != "null" || string(data["is_enabled"]) != "true" || string(data["version"]) != "3" {
		t.Fatalf("user profile DTO changed: %s", envelope["data"])
	}
}

func assertFailure(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode, wantMessage string) map[string]json.RawMessage {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("HTTP status = %d, want %d; body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want JSON", contentType)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode failure: %v; body=%s", err, recorder.Body.String())
	}
	if len(envelope) != 2 || string(envelope["data"]) != "null" {
		t.Fatalf("unexpected failure envelope: %s", recorder.Body.String())
	}
	var failure map[string]json.RawMessage
	if err := json.Unmarshal(envelope["error"], &failure); err != nil {
		t.Fatalf("decode error object: %v", err)
	}
	wantCodeJSON, _ := json.Marshal(wantCode)
	wantMessageJSON, _ := json.Marshal(wantMessage)
	if string(failure["code"]) != string(wantCodeJSON) || string(failure["message"]) != string(wantMessageJSON) {
		t.Fatalf("unexpected error: %s", envelope["error"])
	}
	if _, exists := failure["business_code"]; exists {
		t.Fatalf("common-only failure must omit business_code: %s", envelope["error"])
	}
	return failure
}
