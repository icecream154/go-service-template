# 接口返回规范

## 1. 统一响应结构

HTTP 接口统一返回 JSON，顶层固定包含 `data`、`error` 两个字段。两个字段始终存在，不使用 `omitempty` 省略，也不根据业务用例更换名称。

| 字段 | 成功时 | 失败时 |
| --- | --- | --- |
| `data` | 对应业务用例的 Response DTO 值 | `null` |
| `error` | `null` | 统一错误对象 |

通用组件使用以下结构表达响应：

```go
type Response[T any] struct {
	Data  *T         `json:"data"`
	Error *ErrorInfo `json:"error"`
}

type ErrorCode string

type ErrorInfo struct {
	Code       ErrorCode    `json:"code"`
	Message    string       `json:"message"`
	Details    []FieldError `json:"details,omitempty"`
	HTTPStatus int          `json:"-"`
}

type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}
```

`HTTPStatus` 仅用于选择 HTTP 状态码，不进入 JSON。`details` 没有条目时省略；`code`、`message` 始终存在。

## 2. 成功响应与业务 DTO

成功返回 HTTP 200，`error` 固定为 `null`，`data` 使用该业务用例专属的 Response DTO。例如，某个用例的业务结果包含 `value` 字段时：

```json
{
  "data": {
    "value": "example"
  },
  "error": null
}
```

业务用例与 DTO 的名称继续遵守[接口路由与命名规范](api-routing.md)。`<完整用例名>Response` 描述 `data` 的业务结构，通用 `Response[T]` 只负责公共外层；不将用例 Response DTO 重命名为通用名称，也不为同一用例再增加一套外层业务 DTO。

没有业务返回字段的用例，也声明专属的空 Response DTO，并返回：

```json
{
  "data": {},
  "error": null
}
```

成功时不以 `data: null` 代替空 Response DTO，也不通过省略响应体表示成功。DTO 中的集合无元素时初始化为空切片，输出 `[]`；具有可空语义的其他字段按契约保留 `null`。这些业务字段由具体用例的 DTO 定义，公共外层不修改它们的空值或序列化方式。

## 3. 失败响应与错误码

失败时 `data` 固定为 `null`，`error` 包含稳定的机器可读错误码及英文消息。HTTP 状态码与错误语义保持一致，不以 HTTP 200 承载业务失败。

初始通用错误码如下：

| 错误码 | HTTP 状态 | 含义 | 默认消息 |
| --- | --- | --- | --- |
| `INVALID_ARGUMENT` | 400 | 请求参数缺失、格式错误或不符合约束 | `invalid request parameters` |
| `NOT_FOUND` | 404 | 请求的资源或路由不存在 | `resource not found` |
| `INTERNAL_ERROR` | 500 | 服务内部错误或未处理异常 | `internal server error` |

错误码使用大写英文及下划线。新增通用错误码时，统一维护其含义、HTTP 状态和调用方处理规则；不得为相同语义随意另起错误码。

参数错误可以附带字段明细：

```json
{
  "data": null,
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "invalid request parameters",
    "details": [
      {
        "field": "item_id",
        "reason": "must be a positive 64-bit integer"
      }
    ]
  }
}
```

`field` 使用实际请求参数名，`reason` 说明该字段违反的约束。没有字段明细时，不返回空 `details` 数组。

内部错误固定返回以下内容，不将底层错误、数据库语句、堆栈或其他内部信息拼入消息及明细：

```json
{
  "data": null,
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "internal server error"
  }
}
```

原始错误按日志规范记录在服务端，供排查使用。

## 4. 错误构造与输出

统一使用 `internal/response` 组件的构造器和输出函数。

| 调用方式 | 作用 |
| --- | --- |
| `InvalidArgument(message, details...)` | 构造 `INVALID_ARGUMENT`，可附带字段明细 |
| `NotFound(message)` | 构造 `NOT_FOUND` |
| `InternalError()` | 构造固定消息的 `INTERNAL_ERROR`，不接收原始错误或自定义消息 |
| `Success(ctx, data)` | 将用例 Response DTO 包装为成功响应，返回 HTTP 200 |
| `Fail(ctx, failure)` | 输出失败响应，并中止后续 Gin 处理链 |

`InvalidArgument`、`NotFound` 的消息为空或全为空白时，使用第 3 节列出的默认英文消息。可预期的业务错误可以使用明确的英文消息；内部错误统一使用固定消息。

例如，在 Controller 校验失败分支中：

```go
response.Fail(ctx, response.InvalidArgument(
	"",
	response.FieldError{
		Field:  "item_id",
		Reason: "must be a positive 64-bit integer",
	},
))
return
```

`Fail` 中止后续 Gin 处理链，不会自动退出当前 Go 函数。Controller 在调用 `Fail` 后必须立即 `return`，避免继续调用业务逻辑或重复写入响应。

错误定义无效时，组件回退为 HTTP 500 和固定的 `INTERNAL_ERROR`，不输出该错误定义中的自定义内容。无效情形包括 HTTP 状态不在 400～599 范围、错误码为空或全为空白、消息为空或全为空白。

## 5. 路由与异常的统一处理

未匹配路由统一返回 HTTP 404、`NOT_FOUND` 和消息 `route not found`，使用同样的 `data: null`、`error` 对象结构。

未处理异常由统一恢复中间件记录日志；尚未写入响应时，输出固定的 HTTP 500 内部错误。响应已经写入后，不追加第二份错误 JSON，也不声明可以撤回已发送内容。

Controller 的可预期错误、未匹配路由和异常恢复均使用公共响应组件输出，避免出现业务响应已经统一、框架错误仍使用其他结构的情况。
