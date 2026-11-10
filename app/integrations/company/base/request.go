package base

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go-service-template/internal/logger"
	"go-service-template/internal/tracing"

	"go.opentelemetry.io/otel/propagation"
)

// GetJSON 向目标服务发送 GET 请求，将 HTTP 200 的 JSON 正文解码到 result。
// query 是可选的查询参数，result 必须是非 nil 指针；本方法不校验业务字段或额外重试。
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, result any) error {
	// 1. 保留具体 Client 已建立的操作作用域，将 GET 请求及其日志一起交给公共流程。
	// 本层不重新识别方法名，避免把业务操作替换成共享底座的 GetJSON。
	return c.requestJSON(ctx, http.MethodGet, path, query, nil, result)
}

// PostJSON 向目标服务发送 JSON POST 请求，将 HTTP 200 的 JSON 正文解码到 result。
// path 是服务内的接口路径，result 必须是非 nil 指针；本方法不校验业务字段或额外重试。
func (c *Client) PostJSON(ctx context.Context, path string, params, result any) error {
	// 1. 沿用传入 Context 的业务操作和 TraceId，由公共流程完成 JSON 编码、发送及日志记录。
	// 不在这里创建操作作用域或增加重试，保持具体 Client 的接口身份和调用次数。
	return c.requestJSON(ctx, http.MethodPost, path, nil, params, result)
}

// requestJSON 统一管理一次公司 HTTP 调用的 Span 和日志生命周期，保留原有请求错误和 panic 语义。
// Logger 和 tracing 必须已在启动流程初始化；单次状态不写入可并发复用的 Client。
func (c *Client) requestJSON(ctx context.Context, method, path string, query url.Values, params, result any) (err error) {
	// 1. 取得共享 Logger，并在请求校验前开始计时，使未发出网络请求的失败也有完整调用记录。
	// Context 中的 operation 已由具体 Client 自动识别，本层不解析 GetJSON、PostJSON 或私有方法名。
	call := &httpCallLog{log: logger.Current(), ctx: ctx, startedAt: time.Now()}
	// 1.1. 有执行 Context 时创建唯一的 Client Span，日志与 HTTP 请求统一使用返回的子 Context。
	// 共享 SDK 沿用有效父链路或兼容 TraceId；没有合法标识的独立调用由 SDK 创建根节点。
	if ctx != nil {
		ctx, call.span = tracing.Current().StartCompanyClient(ctx, logger.Operation(ctx))
		call.ctx = ctx
	}
	// 1.2. 直接登记异常退出兜底；nil Context 不创建 Span，仍由原执行层返回参数错误。
	// 正常收尾会在 finished 日志之前结束 Span，此处的重复 End 幂等，panic 继续按原值传播。
	defer call.span.End()
	returnedNormally := false
	// 2. 无论正常返回还是异常退出，都先提交调用结果、结束 Span，再记录结束事件。
	// sendJSON 内的响应关闭先于本层收尾执行，因此区间覆盖准备、发送、读取、解码及关闭。
	defer func() {
		// 2.1. 正常返回标识避免 panic 时把 nil 错误误记成成功；此处不 recover 或吞掉异常。
		call.finish(err, returnedNormally)
	}()
	// 3. 开始记录不预填耗时、状态或结果；日志输出失败只写独立兜底，不阻止真实调用。
	call.start()
	// 4. 请求只执行一次，网络状态和受控错误阶段由执行层交回本次日志状态。
	// 原始错误继续返回业务方，不把包含 URL、Query 或远端数据的错误文本直接写入日志。
	err = c.sendJSON(ctx, method, path, query, params, result, call)
	// 5. 只有执行层正常返回后才标记完成；defer 统一写 finished，不额外打印重复失败摘要。
	returnedNormally = true
	return err
}

// sendJSON 执行原有 HTTP JSON 协议，并只向调用日志提供实际状态和固定阶段说明。
// call 由 requestJSON 为单次调用创建，不保存请求正文、响应正文或底层错误对象。
func (c *Client) sendJSON(ctx context.Context, method, path string, query url.Values, params, result any, call *httpCallLog) error {
	// 1. 校验底座和请求 Context；保留原有错误返回约定，不因日志作用域支持 nil 而放宽请求契约。
	if c == nil || c.httpClient == nil {
		// 1.1. 未初始化时没有远端 HTTP 状态，只提供固定错误说明。
		call.failureMessage = "Company HTTP client is not initialized"
		return errors.New("company HTTP client is not initialized")
	}
	if ctx == nil {
		// 1.2. 缺少 Context 时拒绝发送；日志可以缺少 trace_id，但不能擅自创建后台请求。
		call.failureMessage = "Company HTTP request context is required"
		return errors.New("HTTP request context is required")
	}
	if err := ctx.Err(); err != nil {
		// 1.3. 已取消或已到期的请求直接返回原 Context 错误，不发请求或伪造状态码。
		call.failureMessage = "Company HTTP request context ended"
		return err
	}
	// 2. 只有 POST 编码 JSON 正文；编码失败发生在网络发送前，保留原始错误链供调用方判断。
	var body io.Reader
	if method == http.MethodPost {
		// 2.1. 不在公共层修改业务字段，编码结果仅用于构造请求，不进入日志。
		encoded, err := json.Marshal(params)
		if err != nil {
			// 2.2. 日志只说明编码阶段失败，避免自定义编码器的错误文本暴露业务参数。
			call.failureMessage = "Company HTTP request encoding failed"
			return fmt.Errorf("encode JSON request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	// 3. 按原有规则拼接目标地址，并绑定调用方的取消及截止时间；地址本身不作为日志字段。
	endpoint := strings.TrimRight(c.baseURL, "/") + "/" + strings.TrimLeft(path, "/")
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		// 3.1. 构造失败尚未取得远端响应，日志保留固定阶段，业务方仍收到原始错误链。
		call.failureMessage = "Company HTTP request creation failed"
		return fmt.Errorf("create HTTP request: %w", err)
	}
	// 4. 合并可选查询参数，继续保留每个 key 的多值语义；不记录实际 Query 或资源标识。
	if len(query) > 0 {
		// 4.1. 从请求已有参数开始追加，编码后仅写回当前请求，不修改调用方的 query。
		values := request.URL.Query()
		for key, entries := range query {
			// 4.2. 同一个 key 的所有值逐个追加，维持既有 GET 参数传递行为。
			for _, value := range entries {
				values.Add(key, value)
			}
		}
		request.URL.RawQuery = values.Encode()
	}
	// 5. 声明 JSON 协议，并把本次 Client 子 Context 的标准追踪头和兼容 TraceId 写入新请求。
	// 每次调用使用独立 Header，不复用其他请求的追踪值，也不记录认证头或任何头部正文。
	if method == http.MethodPost {
		// 5.1. 只有实际携带 JSON 正文的 POST 才设置正文类型。
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	// 5.2. 由标准传播器注入 traceparent 及非空 tracestate，使用当前 Client SpanId 作为远端父节点。
	// 不手工拼接标识、采样标志或状态，不传播 Baggage，也不向第三方 SDK 隐式注入内部头。
	tracing.Inject(ctx, propagation.HeaderCarrier(request.Header))
	if id := tracing.TraceID(ctx); id != "" {
		// 5.3. 兼容头与标准头、日志读取同一个子 Context，保证上下游使用同一 TraceId。
		request.Header.Set(tracing.TraceIDHeaderName, id)
	}
	// 6. 使用既有连接池和请求超时发送一次，不增加重试；收到响应后立即保存真实 HTTP 状态。
	response, err := c.httpClient.Do(request)
	if response != nil {
		// 6.1. 后续读取、大小检查或解码失败仍保留已取得的状态，不把 HTTP 200 等同于调用成功。
		call.statusCode = response.StatusCode
	}
	if err != nil {
		// 6.2. 发送错误可能含完整 URL，因此日志只记录固定阶段，原始错误继续通过 %w 返回。
		call.failureMessage = "Company HTTP request send failed"
		return fmt.Errorf("send HTTP request: %w", err)
	}
	// 7. 正常取得响应后始终关闭正文；继续沿用忽略 Close 返回值的契约，不新增业务失败条件。
	defer response.Body.Close()

	// 8. 有界读取响应，沿用既有内存上限；正文只参与协议处理，不进入日志或错误字段。
	// JSON 响应读取上限为 16 MiB（16,777,216 字节），可根据业务实际情况调整。
	const maxResponseBytes = 16 << 20
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		// 8.1. 读取失败保留已经取得的状态码，固定说明不包含远端返回内容。
		call.failureMessage = "Company HTTP response read failed"
		return fmt.Errorf("read HTTP response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		// 8.2. 读取到上限之外的一个字节即可确认超限，不继续把任意正文载入内存。
		call.failureMessage = "Company HTTP response exceeds size limit"
		return errors.New("JSON response exceeds 16 MiB")
	}
	// 9. 沿用只接受 HTTP 200 的协议；非预期状态记为调用失败，不解析或记录远端错误正文。
	if response.StatusCode != http.StatusOK {
		// 9.1. 状态码由独立字段表达，错误说明保持固定，业务返回错误格式保持不变。
		call.failureMessage = "Company HTTP response returned unexpected status"
		return fmt.Errorf("HTTP request returned status %d", response.StatusCode)
	}
	// 10. 将成功响应解码到业务出参，解码失败仍属于本次调用失败，不能仅凭状态码记为成功。
	if err := json.Unmarshal(responseBody, result); err != nil {
		// 10.1. 解码器可能返回含业务数据的错误，日志仅记录阶段，调用方仍可取得原始错误链。
		call.failureMessage = "Company HTTP response decoding failed"
		return fmt.Errorf("decode JSON response: %w", err)
	}
	// 11. 只有协议处理全部完成才返回成功；统一结束日志由外层在响应关闭后输出。
	return nil
}
