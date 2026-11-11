package validator_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go-service-template/internal/validator"
)

type bindRequest struct {
	ID      int64    `form:"query_id" json:"body_id,string"`
	Version *int64   `form:"query_version" json:"version"`
	Limit   int      `form:"limit" json:"limit"`
	Tags    []string `form:"tag" json:"tags"`
	// 仅供测试，用未提供的必填字段确认 Bind 不执行 Gin 的自动校验。
	Required string `form:"required" json:"required" binding:"required"`
}

type bindSuccessCase struct {
	name           string
	method         string
	target         string
	contentType    string
	body           string
	initialVersion *int64
	want           bindRequest
}

func TestBind(t *testing.T) {
	tests := []bindSuccessCase{
		// 同时传入表单标签、JSON 标签和冲突的请求体，验证 GET 只按 form 标签读取 Query，并保留重复参数的顺序。
		{
			name: "GET maps query only", method: http.MethodGet,
			target:      "/?query_id=42&body_id=99&query_version=0&tag=first&tag=second",
			contentType: "application/json", body: `{"body_id":"77"}`,
			want: bindRequest{ID: 42, Version: new(int64(0)), Limit: 25, Tags: []string{"first", "second"}},
		},
		// 空 Query 省略所有字段，验证指针保持 nil、预填默认值保留且必填规则由调用方执行。
		{name: "GET preserves defaults", method: http.MethodGet, target: "/", want: bindRequest{Limit: 25}},
		// 用超出浮点精确整数范围的字符串 ID 和冲突的 Query，验证 POST 保持 ID 精度且只从 JSON 读取。
		{
			name: "POST maps JSON only", method: http.MethodPost, target: "/?query_id=99&query_version=7&limit=5",
			contentType: "application/json; charset=utf-8", body: `{"body_id":"9007199254740993","version":0,"tags":["one"]}`,
			want: bindRequest{ID: 9007199254740993, Version: new(int64(0)), Limit: 25, Tags: []string{"one"}},
		},
		// 空 JSON 对象省略指针及必填字段，验证缺失字段保留默认值，且绑定仍然成功。
		{
			name: "POST preserves defaults", method: http.MethodPost, target: "/",
			contentType: "application/json", body: `{}`, want: bindRequest{Limit: 25},
		},
		// 省略预填的版本，同时显式传入 limit=0，验证缺失字段保留已有值，显式零值覆盖默认值。
		{
			name: "POST distinguishes missing from zero", method: http.MethodPost, target: "/",
			contentType: "application/json", body: `{"limit":0}`, initialVersion: new(int64(7)),
			want: bindRequest{Version: new(int64(7))},
		},
		// 对预填的版本指针传入 null，验证显式 null 会清除该指针。
		{
			name: "POST clears pointer with null", method: http.MethodPost, target: "/",
			contentType: "application/json", body: `{"version":null}`, initialVersion: new(int64(7)),
			want: bindRequest{Limit: 25},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := newBindContext(test.method, test.target, test.contentType, test.body)
			got := bindRequest{Version: test.initialVersion, Limit: 25}
			if err := validator.Bind(ctx, &got); err != nil {
				t.Fatalf("Bind() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("Bind() populated %+v, want %+v", got, test.want)
			}
		})
	}
}

type bindFailureCase struct {
	name          string
	method        string
	target        string
	contentType   string
	body          string
	nilBody       bool
	wantUnchanged bool
	wantErr       error
	checkError    func(*testing.T, error)
}

func TestBindRejectsInvalidRequests(t *testing.T) {
	tests := []bindFailureCase{
		// 整数字段使用非数字 Query，验证底层转换错误可由调用方识别。
		{name: "GET rejects malformed integer", method: http.MethodGet, target: "/?query_id=abc", wantErr: strconv.ErrSyntax},
		// 比 int64 上界大一的 Query 值验证溢出不会被截断或接受。
		{name: "GET rejects integer overflow", method: http.MethodGet, target: "/?query_id=9223372036854775808", wantErr: strconv.ErrRange},
		// 合法 JSON 缺少 Content-Type，验证不会自动推断格式或填充 DTO。
		{
			name: "POST requires Content-Type", method: http.MethodPost, target: "/",
			body: `{"limit":1}`, wantUnchanged: true,
		},
		// 合法 JSON 标为表单，验证 POST 拒绝其他媒体类型，且不会写入 DTO。
		{
			name: "POST rejects form Content-Type", method: http.MethodPost, target: "/",
			contentType: "application/x-www-form-urlencoded", body: `{"limit":1}`, wantUnchanged: true,
		},
		// 有正确媒体类型但 Body 为 nil，验证缺少请求体时返回错误。
		{
			name: "POST rejects nil body", method: http.MethodPost, target: "/",
			contentType: "application/json", nilBody: true, wantUnchanged: true,
		},
		// 非 nil 的请求体没有任何字节，验证保留 JSON 解码器的 EOF。
		{
			name: "POST rejects empty JSON", method: http.MethodPost, target: "/",
			contentType: "application/json", wantErr: io.EOF, wantUnchanged: true,
		},
		// JSON 属性缺少值，验证语法错误保留结构化的解码错误。
		{
			name: "POST rejects malformed JSON", method: http.MethodPost, target: "/",
			contentType: "application/json", body: `{"version":}`, checkError: assertBindJSONSyntaxError,
		},
		// 给整数版本传字符串，验证类型错误定位到 version 字段。
		{
			name: "POST rejects wrong JSON field type", method: http.MethodPost, target: "/",
			contentType: "application/json", body: `{"version":"1"}`, checkError: assertBindJSONVersionTypeError,
		},
		// PUT 同时带合法 Query 和 JSON，验证非 GET 或 POST 的方法不会进入任一种绑定路径。
		{
			name: "PUT is unsupported", method: http.MethodPut, target: "/?limit=2",
			contentType: "application/json", body: `{"limit":1}`, wantUnchanged: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := newBindContext(test.method, test.target, test.contentType, test.body)
			if test.nilBody {
				ctx.Request.Body = nil
			}
			initial := bindRequest{Limit: 25}
			got := initial
			err := validator.Bind(ctx, &got)
			if err == nil {
				t.Fatal("Bind() error = nil, want rejection")
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Errorf("Bind() error = %v, want %v", err, test.wantErr)
			}
			if test.checkError != nil {
				test.checkError(t, err)
			}
			if test.wantUnchanged && !reflect.DeepEqual(got, initial) {
				t.Errorf("Bind() populated %+v, want unchanged %+v", got, initial)
			}
		})
	}
}

func newBindContext(method, target, contentType, body string) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		ctx.Request.Header.Set("Content-Type", contentType)
	}
	return ctx
}

func assertBindJSONSyntaxError(t *testing.T, err error) {
	t.Helper()
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Errorf("Bind() error = %T (%v), want *json.SyntaxError", err, err)
	}
}

func assertBindJSONVersionTypeError(t *testing.T, err error) {
	t.Helper()
	var mismatch *json.UnmarshalTypeError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Bind() error = %T (%v), want *json.UnmarshalTypeError", err, err)
	}
	if mismatch.Field != "version" {
		t.Errorf("Bind() error field = %q, want %q", mismatch.Field, "version")
	}
}
