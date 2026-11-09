package base

import (
	"context"
	"time"

	"go-service-template/internal/logger"
)

// httpCallLog 保存一次公共 JSON 调用的日志状态，仅由当前调用所在的 goroutine 使用。
// 它不放入共享 Client，也不保存 URL、请求参数、响应正文或原始错误对象。
type httpCallLog struct {
	log            *logger.Logger
	ctx            context.Context
	startedAt      time.Time
	statusCode     int
	failureMessage string
}

// start 记录调用开始，操作身份及 TraceId 均由 Logger 从具体 Client 传入的 Context 自动读取。
func (call *httpCallLog) start() {
	// 1. 开始时尚未取得远端结果，不预填耗时、状态码或错误；日志写入失败不替换业务返回。
	// 共享 Logger 在内部统一处理编码及输出故障，本层直接记录本次调用的开始。
	call.log.Info(call.ctx, "Company HTTP call started", logger.Fields{Event: logger.EventStarted})
}

// finish 记录调用结束，正常结果来自返回错误，异常终止由外层显式传入，不在此恢复 panic。
func (call *httpCallLog) finish(callErr error, returnedNormally bool) {
	// 1. 在输出结束日志前截取总耗时，覆盖准备、HTTP 交互、协议处理及响应关闭。
	// 开始日志同步写入所用的时间也可能计入，结束日志自身的编码与输出时间不计入。
	durationMS := float64(time.Since(call.startedAt)) / float64(time.Millisecond)
	fields := logger.Fields{Event: logger.EventFinished, DurationMS: &durationMS}
	// 2. 只记录从远端响应实际取得的状态；连接、编码等响应前失败不伪造状态或使用 0 充数。
	if call.statusCode != 0 {
		// 2.1. 状态和最终调用结果相互独立，HTTP 200 后的读取或解码失败仍携带真实的 200。
		fields.StatusCode = &call.statusCode
	}
	// 3. 使用固定错误说明区分异常终止与正常错误返回，不序列化原始错误或 panic 内容。
	if !returnedNormally {
		// 3.1. panic 等导致执行层未正常返回时不能因返回错误仍为 nil 而记成成功。
		fields.Error = "Company HTTP call terminated before completion"
	} else if callErr != nil {
		// 3.2. 正常失败使用执行层提供的固定阶段说明，保留底层错误链给业务方而非日志。
		fields.Error = call.failureMessage
		if fields.Error == "" {
			// 3.3. 后续新增错误路径若遗漏阶段说明，仍提供固定失败兜底，不输出任意错误文本。
			fields.Error = "Company HTTP call failed"
		}
	}
	// 4. 每次调用只写一条结束记录；失败使用 ERROR，成功使用 INFO，不额外打印重复错误摘要。
	// Logger 自行处理输出故障，不改变业务结果、响应出参或正在传播的 panic。
	if fields.Error != "" {
		// 4.1. finished 只表示结束，失败事实通过日志级别及受控 error 字段表达。
		call.log.Error(call.ctx, "Company HTTP call finished", fields)
	} else {
		// 4.2. 协议处理正常完成时不填 error，结果字段仍仅包含实际采集到的数据。
		call.log.Info(call.ctx, "Company HTTP call finished", fields)
	}
}
