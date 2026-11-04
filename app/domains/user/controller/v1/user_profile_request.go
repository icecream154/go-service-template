package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"unicode/utf8"

	userv1dto "go-service-template/app/domains/user/dto/v1"
	userservice "go-service-template/app/domains/user/service"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

// decodeUserProfileJSON 只接受单个 JSON 对象，拒绝未知字段，避免静默忽略调用方的写入意图。
// required 中的字段必须显式出现，nullable 列出允许为 null 的字段，也可包含可选字段。
// 当前资料写入请求最大为 1 MiB；失败时输出稳定参数错误，不返回解析器的内部文案。
func decodeUserProfileJSON(ctx *gin.Context, target any, required []string, nullable ...string) bool {
	mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		failRequestField(ctx, "body", "Content-Type must be application/json")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 1<<20))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		failRequestField(ctx, "body", "must contain a single JSON object with declared fields")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		failRequestField(ctx, "body", "must contain a single JSON object with declared fields")
		return false
	}
	raw = bytes.TrimSpace(raw)
	// JSON 解码器会替换非法 UTF-8；在解码身份字段前拒绝原始非法字节，避免修改标识。
	if !utf8.Valid(raw) {
		failRequestField(ctx, "body", "must contain valid UTF-8 JSON")
		return false
	}
	if len(raw) == 0 || raw[0] != '{' {
		failRequestField(ctx, "body", "must contain a single JSON object with declared fields")
		return false
	}
	strictDecoder := json.NewDecoder(bytes.NewReader(raw))
	strictDecoder.DisallowUnknownFields()
	if err := strictDecoder.Decode(target); err != nil {
		failRequestField(ctx, "body", "must contain a single JSON object with declared fields")
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		failRequestField(ctx, "body", "must contain a single JSON object with declared fields")
		return false
	}
	allowsNull := make(map[string]bool, len(nullable))
	allowedFields := make(map[string]bool, len(required)+len(nullable))
	for _, field := range required {
		allowedFields[field] = true
	}
	for _, field := range nullable {
		allowsNull[field] = true
		allowedFields[field] = true
	}
	// encoding/json 还接受字段名大小写别名，此处只允许契约声明的 snake_case 名称。
	for field := range fields {
		if !allowedFields[field] {
			failRequestField(ctx, "body", "must contain a single JSON object with declared fields")
			return false
		}
	}
	for _, field := range required {
		value, exists := fields[field]
		if !exists || (!allowsNull[field] && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			failRequestField(ctx, field, "must be explicitly provided with a valid value")
			return false
		}
	}
	return true
}

// parsePageQuery 区分省略参数和显式空值，后者不采用默认值。
func parsePageQuery(ctx *gin.Context, request *userv1dto.QueryUserProfilePageRequest) bool {
	query := ctx.Request.URL.Query()
	for _, field := range []string{"page_number", "page_size"} {
		if _, exists := query[field]; !exists {
			continue
		}
		value, err := strconv.Atoi(query.Get(field))
		if err != nil || value < 1 || (field == "page_size" && value > userservice.MaxUserProfilePageSize) {
			if field == "page_size" {
				failRequestField(ctx, field, "must be an integer between 1 and 100")
			} else {
				failRequestField(ctx, field, "must be a positive integer")
			}
			return false
		}
		if field == "page_number" {
			request.PageNumber = value
		} else {
			request.PageSize = value
		}
	}
	// 检查分页偏移的计算范围，避免整数溢出后查询到错误页。
	if request.PageNumber-1 > int(^uint(0)>>1)/request.PageSize {
		failRequestField(ctx, "page_number", "page offset exceeds the supported range")
		return false
	}
	request.UserName = query.Get("user_name")
	if _, exists := query["is_enabled"]; exists {
		switch query.Get("is_enabled") {
		case "true":
			value := true
			request.IsEnabled = &value
		case "false":
			value := false
			request.IsEnabled = &value
		default:
			failRequestField(ctx, "is_enabled", "must be true or false")
			return false
		}
	}
	return true
}

// requirePositiveUserID 验证已解码的内部用户 ID，避免非法参数进入 Service。
func requirePositiveUserID(ctx *gin.Context, field string, userID int64) bool {
	if userID <= 0 {
		failRequestField(ctx, field, "must be a positive 64-bit integer")
		return false
	}
	return true
}

// failRequestField 输出固定通用参数错误和明确的字段约束。
func failRequestField(ctx *gin.Context, field, reason string) {
	response.Fail(ctx, response.InvalidArgument("", response.FieldError{Field: field, Reason: reason}))
}
