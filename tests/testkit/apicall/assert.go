package apicall

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"go-service-template/internal/response"
)

// RequireSuccess 断言响应为 HTTP 200 JSON 成功信封（data 非 null 且 error 严格为 null），并将 data 解码为 T。
// 状态码或信封不符时仅输出状态码、追踪头及非敏感 error 摘要，绝不打印可能包含凭证的 data 正文。
func RequireSuccess[T any](t testing.TB, resp *Response) T {
	t.Helper()
	if resp == nil {
		t.Fatal("nil HTTP response")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (%s)", resp.StatusCode, safeDiagnosticSummary(resp))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json (%s)", ct, safeDiagnosticSummary(resp))
	}

	var envelope struct {
		Data  *T              `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(resp.RawBody, &envelope); err != nil {
		t.Fatalf("decode success JSON envelope failed (%s): %v", safeDiagnosticSummary(resp), err)
	}
	if envelope.Data == nil || string(envelope.Error) != "null" {
		t.Fatalf("success response must contain non-null data and null error (%s)", safeDiagnosticSummary(resp))
	}
	return *envelope.Data
}

// RequireError 断言响应符合指定的 HTTP 状态码、通用错误码与可选业务错误码（data 严格为 null），返回解析后的 ErrorInfo。
func RequireError(t testing.TB, resp *Response, wantStatus int, wantCode string, wantBusinessCode string) response.ErrorInfo {
	t.Helper()
	if resp == nil {
		t.Fatal("nil HTTP response")
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("HTTP status = %d, want %d (%s)", resp.StatusCode, wantStatus, safeDiagnosticSummary(resp))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json (%s)", ct, safeDiagnosticSummary(resp))
	}

	var envelope struct {
		Data  json.RawMessage     `json:"data"`
		Error *response.ErrorInfo `json:"error"`
	}
	if err := json.Unmarshal(resp.RawBody, &envelope); err != nil {
		t.Fatalf("decode error JSON envelope failed (%s): %v", safeDiagnosticSummary(resp), err)
	}
	if string(envelope.Data) != "null" || envelope.Error == nil {
		t.Fatalf("error response must contain null data and non-null error (%s)", safeDiagnosticSummary(resp))
	}
	if string(envelope.Error.Code) != wantCode {
		t.Fatalf("error.code = %q, want %q (%s)", envelope.Error.Code, wantCode, safeDiagnosticSummary(resp))
	}
	if string(envelope.Error.BusinessCode) != wantBusinessCode {
		t.Fatalf("error.business_code = %q, want %q (%s)", envelope.Error.BusinessCode, wantBusinessCode, safeDiagnosticSummary(resp))
	}
	if strings.TrimSpace(envelope.Error.Message) == "" {
		t.Fatalf("error.message must not be blank (%s)", safeDiagnosticSummary(resp))
	}
	return *envelope.Error
}

// RequireFieldError 断言错误详情中包含指定字段名的校验错误且原因非空。
func RequireFieldError(t testing.TB, errInfo response.ErrorInfo, wantField string) {
	t.Helper()
	for _, detail := range errInfo.Details {
		if detail.Field == wantField && strings.TrimSpace(detail.Reason) != "" {
			return
		}
	}
	t.Fatalf("expected field validation error for %q in details=%+v (message=%q)", wantField, errInfo.Details, errInfo.Message)
}

// safeDiagnosticSummary 从响应中仅提取 TraceId 和结构化 error 元数据，过滤掉任何潜在敏感的 data 载荷。
func safeDiagnosticSummary(resp *Response) string {
	traceID := resp.Header.Get("TraceId")
	var partial struct {
		Error *response.ErrorInfo `json:"error"`
	}
	if err := json.Unmarshal(resp.RawBody, &partial); err == nil && partial.Error != nil {
		return fmt.Sprintf("TraceId=%q error.code=%q error.business_code=%q error.message=%q details=%+v",
			traceID, partial.Error.Code, partial.Error.BusinessCode, partial.Error.Message, partial.Error.Details)
	}
	return fmt.Sprintf("TraceId=%q body_bytes=%d", traceID, len(resp.RawBody))
}
