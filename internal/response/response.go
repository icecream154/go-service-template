// Package response 统一输出 HTTP JSON 响应，不依赖具体业务领域。
package response

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Response 的两个顶层字段始终存在；T 是业务用例专属的 Response DTO。
// 成功传已构造的 DTO 值，无业务字段时使用该用例的空结构体。
type Response[T any] struct {
	Data  *T         `json:"data"`
	Error *ErrorInfo `json:"error"`
}

// Success 输出当前接口约定的 HTTP 200 成功响应，不更改 DTO 内部的空值语义。
// 编码或写入失败由本组件处理；响应已经开始写出时不追加另一份响应。
// 正常成功时不会 Abort，因此应将本调用作为当前处理函数的最后一步，或在调用后 return。
// 即使内部遇到失败并 Abort，也不会自动跳过调用方在 Success 后面编写的代码。
func Success[T any](ctx *gin.Context, data T) {
	writeJSON(ctx, http.StatusOK, Response[T]{Data: &data, Error: nil})
}

// Fail 输出结构化错误并终止后续 Gin 处理链；调用方仍须 return 结束当前函数。
// 通用类别必须已登记，HTTP 状态码由类别的固定映射决定。
// 合法错误对象的消息、业务码和字段明细原样输出，包括 INTERNAL_ERROR。
// 未登记的类别或空白必填内容属于无效错误定义，使用框架默认内部错误兜底。
// Abort 只阻止 Gin 继续调度尚未执行的处理器，不会结束当前函数，也不会立即写出响应。
// 因此这里 Abort 后仍会继续构造并输出错误；调用方也必须用 return 结束自己的错误分支。
func Fail(ctx *gin.Context, failure ErrorInfo) {
	ctx.Abort()
	definition, valid := failure.Code.definition()
	if !valid || strings.TrimSpace(failure.Message) == "" ||
		(failure.BusinessCode != "" && strings.TrimSpace(string(failure.BusinessCode)) == "") {
		failure = InternalError()
		definition, _ = failure.Code.definition()
	}
	writeJSON(ctx, definition.httpStatus, Response[any]{Data: nil, Error: &failure})
}

// writeJSON 先编码完整响应，再写入状态码和正文，避免编码失败后留下成功状态码。
//
// 编码是把 Go 值转换为内存中的 JSON 字节；写入是把这些字节交给 HTTP Writer。
// 两个阶段的恢复条件不同：尚未提交响应时，可以改为统一的 500；开始写出后，
// 已提交的状态码无法替换，继续写正文只会追加内容，所以只能记录错误并停止输出。
//
// Gin 的 ctx.JSON 本身也先编码再写入，但编码失败时仅记录到 ctx.Errors 并 Abort，
// 不会自动把待发送的状态改成 500，原本的成功请求可能因此返回 200 和空正文。
// 本方法增加统一的编码失败兜底、写入失败处理和重复输出保护。
// 这些失败由本组件直接记录日志，不再加入 ctx.Errors，也不依赖 panic 才能处理。
//
// Abort 与 return 分别控制 Gin 的后续调度和当前 Go 函数的执行。
// 这里的 return 只退出 writeJSON；Abort 不会退出 Success、Fail 或调用它们的函数，
// 也不会跳过已经进入的中间件中 ctx.Next() 后面的收尾代码。
// 本方法不撤销已经完成的业务操作；例如写入响应失败不会自动回滚已提交的数据库事务。
func writeJSON(ctx *gin.Context, status int, payload any) {
	// Written 表示服务端已经提交响应头，不表示客户端已经收到完整响应。
	// 可能是前面的代码已输出成功、错误、部分正文，或主动提交了响应头。
	// 此时不能覆盖状态码，也不能再追加一份 JSON；先记录，再终止后续处理。
	if ctx.Writer.Written() {
		log.Printf("Skip JSON response: response already written, status=%d", ctx.Writer.Status())
		ctx.Abort()
		return
	}

	// 编码在内存中完成，成功前不设置本次响应的状态码，也不写出正文。
	// 例如 DTO 含有 channel、函数、NaN 或循环引用，或自定义 MarshalJSON 返回错误，
	// 都可能导致编码失败；这些属于服务端输出问题，不是请求参数错误。
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Encode JSON response failed: status=%d error=%v", status, err)
		ctx.Abort()
		// Abort 后本函数仍继续执行，才能在尚未写出时构造下面的 500 兜底。
		// 自定义 MarshalJSON 可能间接操作 Writer，因此失败后再次检查响应是否已提交。
		// 若已经提交，就保留现有输出并返回；无法再用完整的 500 JSON 替换它。
		if ctx.Writer.Written() {
			return
		}
		// 丢弃无法编码的业务响应，改用不包含业务 DTO 的固定错误结构。
		// 原始编码错误只记入日志，对外输出 INTERNAL_ERROR 和默认消息。
		// 只直接编码一次兜底对象，不递归调用 Fail，避免失败后反复尝试输出。
		failure := InternalError()
		body, err = json.Marshal(Response[any]{Data: nil, Error: &failure})
		if err != nil {
			// 当前固定结构只含可编码的基础字段，正常情况下不会进入此分支。
			// 防御将来结构变化：若兜底也编码失败，只记录并返回，不再重试。
			// 注意此分支尚未设置或写出 500，不能保证客户端获得完整的 500 响应。
			log.Printf("Encode fallback JSON response failed: error=%v", err)
			return
		}
		status = http.StatusInternalServerError
	}

	// 编码成功也可能伴随自定义 MarshalJSON 的写出副作用，因此在真正写入前再检查。
	// 这些检查防止顺序执行中的重复输出，不代表可以并发使用同一个 Gin Context。
	if ctx.Writer.Written() {
		log.Printf("Skip JSON response: response already written, status=%d", ctx.Writer.Status())
		ctx.Abort()
		return
	}
	ctx.Header("Content-Type", "application/json; charset=utf-8")
	// 正文由本组件重新编码，清除此前预设的长度，让 HTTP 层按实际输出处理。
	ctx.Header("Content-Length", "")
	// ctx.Status 仅设置 Gin 暂存的状态码；下面的 Write 会先提交响应头，再写正文。
	// 因此即使 Write 返回错误，也不能假定状态码尚未发送，更不能再尝试返回另一份 500。
	ctx.Status(status)
	written, err := ctx.Writer.Write(body)
	// 正常 Writer 在未写满时应返回错误；这里额外防御只返回较少字节数、却没有报错的实现。
	if err == nil && written != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// 客户端断开等情况可能导致写入报错，正文可能尚未写入，也可能只写入一部分。
		// 记录本次写入的状态码、实际与预期字节数及原因，然后 Abort 并 return。
		// 不重试、不改状态码、不调用 Fail，避免把第二份 JSON 追加到已写出的正文后面。
		log.Printf("Write JSON response failed: status=%d written=%d expected=%d error=%v", status, written, len(body), err)
		ctx.Abort()
		return
	}
	// Write 成功仅表示本次写入未报告失败，数据仍可能处于缓冲区，不保证客户端完整收到。
}
