// Package sdkmock 提供跨进程的通用第三方 SDK Mock 控制面与轻量客户端，完全解耦具体供应商与业务模型。
package sdkmock

import "encoding/json"

// InvokeRequest 是被测子进程向父进程 Mock 控制面发起一次第三方 SDK 调用的通用请求包。
type InvokeRequest struct {
	Provider  string          `json:"provider"`
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}

// InvokeResponse 是父进程 Mock 控制面向子进程返回的预设结果或故障描述。
type InvokeResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *FaultSpec      `json:"error,omitempty"`
}

// FaultSpec 描述模拟的第三方 SDK 失败特征，供子进程边界适配器还原为具体 SDK 错误类型。
type FaultSpec struct {
	Code         string `json:"code"`
	StatusCode   int    `json:"status_code,omitempty"`
	ProviderCode int    `json:"provider_code,omitempty"`
	Message      string `json:"message"`
}

// CallError 表示子进程通过 sdkmock.Client 调用控制面收到的受控错误结果。
type CallError struct {
	Fault FaultSpec
}

func (e *CallError) Error() string {
	if e == nil {
		return ""
	}
	if e.Fault.Message != "" {
		return e.Fault.Message
	}
	if e.Fault.Code != "" {
		return "sdkmock fault: " + e.Fault.Code
	}
	return "sdkmock invocation failed"
}
