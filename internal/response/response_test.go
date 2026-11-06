package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

func TestFailureFactoriesBindCommonCodeToHTTPStatus(t *testing.T) {
	tests := []struct {
		name    string
		factory func(string) response.ErrorInfo
		code    response.ErrorCode
		status  int
		message string
	}{
		{"invalid argument", func(message string) response.ErrorInfo { return response.InvalidArgument(message) }, response.CodeInvalidArgument, http.StatusBadRequest, "invalid request parameters"},
		{"unauthenticated", response.Unauthenticated, response.CodeUnauthenticated, http.StatusUnauthorized, "authentication required"},
		{"permission denied", response.PermissionDenied, response.CodePermissionDenied, http.StatusForbidden, "permission denied"},
		{"not found", response.NotFound, response.CodeNotFound, http.StatusNotFound, "resource not found"},
		{"conflict", response.Conflict, response.CodeConflict, http.StatusConflict, "resource conflict"},
		{"rate limited", response.RateLimited, response.CodeRateLimited, http.StatusTooManyRequests, "too many requests"},
		{"internal error", func(string) response.ErrorInfo { return response.InternalError() }, response.CodeInternalError, http.StatusInternalServerError, "internal server error"},
		{"service unavailable", response.ServiceUnavailable, response.CodeServiceUnavailable, http.StatusServiceUnavailable, "service temporarily unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, message := range []string{"", " \t\n"} {
				recorder := writeFailure(test.factory(message))
				if recorder.Code != test.status {
					t.Fatalf("HTTP status = %d, want %d", recorder.Code, test.status)
				}
				failure := decodeFailure(t, recorder)
				assertJSONValue(t, failure, "code", test.code)
				assertJSONValue(t, failure, "message", test.message)
				if len(failure) != 2 {
					t.Fatalf("error contains unexpected fields: %s", recorder.Body.String())
				}
			}
			if test.code != response.CodeInternalError {
				failure := decodeFailure(t, writeFailure(test.factory("public explanation")))
				assertJSONValue(t, failure, "message", "public explanation")
			}
		})
	}
}

func TestFailureBusinessCodeAndFieldDetails(t *testing.T) {
	tests := []struct {
		name         string
		original     response.ErrorInfo
		businessCode response.BusinessErrorCode
		status       int
	}{
		{
			name:         "input failure",
			original:     response.InvalidArgument("invalid input", response.FieldError{Field: "item_id", Reason: "must be positive"}),
			businessCode: "ITEM_ID_INVALID",
			status:       http.StatusBadRequest,
		},
		{
			name: "internal failure preserves supplied content",
			original: response.ErrorInfo{
				Code:    response.CodeInternalError,
				Message: "failed to process request",
				Details: []response.FieldError{
					{Field: "second_field", Reason: "second field explanation"},
					{Field: "first_field", Reason: "first field explanation"},
				},
			},
			businessCode: "REQUEST_PROCESSING_FAILED",
			status:       http.StatusInternalServerError,
		},
		{
			name:         "default internal failure preserves business code",
			original:     response.InternalError(),
			businessCode: "REQUEST_PROCESSING_FAILED",
			status:       http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withBusinessCode := test.original.WithBusinessCode(test.businessCode)
			if test.original.BusinessCode != "" {
				t.Fatal("WithBusinessCode modified the original error value")
			}
			recorder := writeFailure(withBusinessCode)
			if recorder.Code != test.status {
				t.Fatalf("HTTP status = %d, want %d", recorder.Code, test.status)
			}
			failure := decodeFailure(t, recorder)
			assertJSONValue(t, failure, "code", test.original.Code)
			assertJSONValue(t, failure, "business_code", test.businessCode)
			assertJSONValue(t, failure, "message", test.original.Message)
			expectedFields := 3
			if len(test.original.Details) > 0 {
				assertJSONValue(t, failure, "details", test.original.Details)
				expectedFields++
			} else if _, exists := failure["details"]; exists {
				t.Fatal("empty details must be omitted")
			}
			if len(failure) != expectedFields {
				t.Fatalf("unexpected error fields: %s", recorder.Body.String())
			}

			withoutBusinessCode := withBusinessCode.WithBusinessCode("")
			failure = decodeFailure(t, writeFailure(withoutBusinessCode))
			if _, exists := failure["business_code"]; exists {
				t.Fatal("empty business_code must be omitted")
			}
		})
	}
}

func TestMalformedFailureFallsBackWithoutOriginalContent(t *testing.T) {
	tests := []struct {
		name    string
		failure response.ErrorInfo
	}{
		{"zero value", response.ErrorInfo{}},
		{"empty common code", response.ErrorInfo{Message: "private explanation"}},
		{"whitespace common code", response.ErrorInfo{Code: " \t", Message: "private explanation"}},
		{"unknown common code", response.ErrorInfo{Code: "CUSTOM_FAILURE", BusinessCode: "PRIVATE_BUSINESS_REASON", Message: "private explanation", Details: []response.FieldError{{Field: "private field", Reason: "private detail"}}}},
		{"incorrect case", response.ErrorInfo{Code: "not_found", Message: "private explanation"}},
		{"blank message", response.ErrorInfo{Code: response.CodeNotFound, BusinessCode: "PRIVATE_BUSINESS_REASON", Message: " \n\t", Details: []response.FieldError{{Field: "private field", Reason: "private detail"}}}},
		{"whitespace business code", response.ErrorInfo{Code: response.CodeNotFound, BusinessCode: " \n\t", Message: "private explanation", Details: []response.FieldError{{Field: "private field", Reason: "private detail"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := writeFailure(test.failure)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("HTTP status = %d, want 500", recorder.Code)
			}
			failure := decodeFailure(t, recorder)
			assertJSONValue(t, failure, "code", response.CodeInternalError)
			assertJSONValue(t, failure, "message", "internal server error")
			if len(failure) != 2 {
				t.Fatalf("fallback retained original metadata: %s", recorder.Body.String())
			}
		})
	}
}

func TestFailAbortsSubsequentHandlers(t *testing.T) {
	router := gin.New()
	nextHandlerCalled := false
	currentHandlerContinued := false
	router.GET("/failure", func(ctx *gin.Context) {
		response.Fail(ctx, response.NotFound(""))
		// Abort 只阻止后续处理器；当前函数仍需自行 return。
		currentHandlerContinued = true
	}, func(ctx *gin.Context) {
		nextHandlerCalled = true
		ctx.String(http.StatusOK, "unexpected response")
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/failure", nil))
	if nextHandlerCalled {
		t.Fatal("handler after Fail was executed")
	}
	if !currentHandlerContinued {
		t.Fatal("Fail unexpectedly stopped the current Go function")
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("HTTP status = %d, want 404", recorder.Code)
	}
	decodeFailure(t, recorder)
}

func TestSuccessPreservesDTOJSONContract(t *testing.T) {
	type result struct {
		ID       int64    `json:"item_id,string"`
		Items    []string `json:"items"`
		Optional *string  `json:"optional"`
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	response.Success(ctx, result{ID: 9223372036854775807, Items: []string{}})
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
		t.Fatalf("decode data: %v", err)
	}
	assertJSONValue(t, data, "item_id", "9223372036854775807")
	if string(data["items"]) != "[]" || string(data["optional"]) != "null" {
		t.Fatalf("DTO empty/null semantics changed: %s", envelope["data"])
	}
}

func TestSuccessWithEmptyDTO(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	response.Success(ctx, struct{}{})
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"data":{},"error":null}` {
		t.Fatalf("unexpected empty success: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func writeFailure(failure response.ErrorInfo) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	response.Fail(ctx, failure)
	return recorder
}

func decodeFailure(t *testing.T, recorder *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
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
	if failure == nil {
		t.Fatalf("failure error must be an object: %s", recorder.Body.String())
	}
	return failure
}

func assertJSONValue(t *testing.T, object map[string]json.RawMessage, field string, want any) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode expected %s: %v", field, err)
	}
	if string(object[field]) != string(wantJSON) {
		t.Fatalf("%s = %s, want %s", field, object[field], wantJSON)
	}
}
