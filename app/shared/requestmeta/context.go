package requestmeta

import "time"

// RequestContext 保存请求元信息；取消与超时仍由 context.Context 传递。
type RequestContext struct {
	TraceId       string `header:"TraceId"`
	StartUnixNano int64
}

func (c RequestContext) GetMillCost() int64 {
	if c.StartUnixNano == 0 {
		return 0
	}
	return (time.Now().UnixNano() - c.StartUnixNano) / int64(time.Millisecond)
}
