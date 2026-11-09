// Package traceid 提供 TraceId 的生成及标准 Context 存取，不依赖 HTTP 框架。
package traceid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// HeaderName 是入站、响应及 company 出站请求统一使用的 TraceId 头名称。
const HeaderName = "TraceId"

// contextKey 只供本包存取 TraceId，避免与其他 Context 值发生键冲突。
type contextKey struct{}

// New 生成 16 字节随机数，并编码为 32 位小写十六进制 TraceId。
func New() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// WithContext 返回保存 TraceId 的派生 Context，不修改传入的 Context。
// ctx 必须非 nil；TraceId 按原值保存，不限制上游标识的格式。
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// FromContext 读取当前 Context 中的 TraceId，未设置时返回空字符串。
// ctx 必须非 nil；本方法不生成标识或修改 Context。
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
