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

type BusinessErrorCode string

type ErrorInfo struct {
	Code         ErrorCode         `json:"code"`
	BusinessCode BusinessErrorCode `json:"business_code,omitempty"`
	Message      string            `json:"message"`
	Details      []FieldError      `json:"details,omitempty"`
}

type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}
```

`code`、`message` 始终存在。`business_code` 为空时省略，`details` 没有条目时省略。HTTP 状态码由 `code` 的固定映射决定，错误对象不提供可以独立设置的 `HTTPStatus` 字段。

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

失败时 `data` 固定为 `null`，`error` 包含通用错误类别、可选业务错误码及英文消息。HTTP 状态码与错误语义保持一致，不以 HTTP 200 承载业务失败。

### 3.1 通用错误码与 HTTP 状态码

`code` 必填，必须使用 `internal/response` 集中声明的固定枚举，不得填入业务错误码或临时字符串。已登记的通用错误码如下：

| 错误码 | HTTP 状态 | 含义 | 默认消息 |
| --- | --- | --- | --- |
| `INVALID_ARGUMENT` | 400 | 请求参数缺失、格式错误或不符合约束 | `invalid request parameters` |
| `UNAUTHENTICATED` | 401 | 未提供有效身份凭证或凭证已失效 | `authentication required` |
| `PERMISSION_DENIED` | 403 | 调用方没有执行该操作的权限 | `permission denied` |
| `NOT_FOUND` | 404 | 请求的资源或路由不存在 | `resource not found` |
| `CONFLICT` | 409 | 请求与资源当前状态冲突 | `resource conflict` |
| `RATE_LIMITED` | 429 | 请求超过允许的频率或额度 | `too many requests` |
| `INTERNAL_ERROR` | 500 | 服务内部错误或未处理异常 | `internal server error` |
| `SERVICE_UNAVAILABLE` | 503 | 服务暂时无法处理请求 | `service temporarily unavailable` |

每个通用错误码固定对应一个 HTTP 状态码，Controller 不单独指定状态码。新增通用错误码时，必须同步维护枚举、HTTP 状态映射、构造器、文档及验证；不得为相同语义随意另起错误码。

### 3.2 业务错误码

`business_code` 可选，表达调用方需要识别的具体业务失败原因。通用 `code` 用于统一判断错误类别，调用方需要处理特定业务场景时再判断 `business_code`。业务码使用大写英文及下划线，并包含所属领域的前缀，以避免跨领域重名。

同一业务原因使用同一个业务码，不按 Controller 或接口方法重复声明。每个业务码固定归属一个通用 `code`；领域接口适配层集中维护该对应关系，Controller 复用映射，不能临时选择不同的通用类别。业务码含义及其归属属于接口契约，变更时需要考虑已有调用方的兼容性。

例如，某领域的目标对象不存在时，可以返回 HTTP 404：

```json
{
  "data": null,
  "error": {
    "code": "NOT_FOUND",
    "business_code": "EXAMPLE_OBJECT_NOT_FOUND",
    "message": "object not found"
  }
}
```

没有需要对外区分的业务原因时，省略 `business_code`。不得以 `message` 或内部错误的 `Error()` 文案作为业务码，也不要求每个内部错误都生成业务码。

### 3.3 参数明细与内部异常

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

## 4. 内部错误与对外错误的职责

Service 返回的 Go `error` 用于内部传递执行失败原因。其中表达业务规则失败的部分属于领域错误，也可能同时返回存储或外部依赖等技术错误。对外 `business_code` 是接口调用方依赖的稳定字符串，二者不要求一一对应：多个内部错误可以映射为同一个业务码；未预期的技术错误统一映射为 `INTERNAL_ERROR`，不附带业务码。

按以下位置组织错误定义和转换：

| 位置 | 职责 |
| --- | --- |
| `internal/response` | 通用错误枚举、固定 HTTP 状态映射、错误响应结构和输出函数 |
| `app/domains/<domain>/errors` | 内部领域错误，不依赖 Gin、HTTP 或通用响应组件 |
| `app/domains/<domain>/apierror` | 对外业务码、领域错误到通用码及业务码的统一映射、可对外展示的文案 |
| Controller | 解析请求、调用 Service、复用领域接口错误映射，并输出响应 |

Service 不构造 `ErrorInfo`，也不需要知道内部错误最终对应的 `business_code`。接口适配层使用 `errors.Is`、`errors.As` 识别内部错误，支持错误包装；不得比较错误字符串决定接口响应。未知错误的原始内容只记录在服务端。

## 5. 错误构造与输出

统一使用 `internal/response` 组件的构造器和输出函数。

| 调用方式 | 作用 |
| --- | --- |
| `InvalidArgument(message, details...)` | 构造 `INVALID_ARGUMENT`，可附带字段明细 |
| `Unauthenticated(message)` | 构造 `UNAUTHENTICATED` |
| `PermissionDenied(message)` | 构造 `PERMISSION_DENIED` |
| `NotFound(message)` | 构造 `NOT_FOUND` |
| `Conflict(message)` | 构造 `CONFLICT` |
| `RateLimited(message)` | 构造 `RATE_LIMITED` |
| `InternalError()` | 构造固定消息的 `INTERNAL_ERROR`，不接收原始错误或自定义消息 |
| `ServiceUnavailable(message)` | 构造 `SERVICE_UNAVAILABLE` |
| `failure.WithBusinessCode(code)` | 按值返回附带业务码的错误对象，不修改原对象；由领域接口适配层固定通用码与业务码的组合 |
| `Success(ctx, data)` | 将用例 Response DTO 包装为成功响应，返回 HTTP 200 |
| `Fail(ctx, failure)` | 输出失败响应，并中止后续 Gin 处理链 |

接收 `message` 的构造器在消息为空或全为空白时，使用第 3.1 节列出的默认英文消息。可预期的业务错误可以使用明确的英文消息；内部错误统一使用固定消息。

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

`Fail` 输出前校验通用码是否属于已登记的枚举，再根据通用码确定 HTTP 状态。错误定义无效时，组件回退为 HTTP 500 和固定的 `INTERNAL_ERROR`，丢弃原定义的业务码、消息和明细。无效情形包括通用码为空或未登记、消息为空或全为空白、业务码非空但全部为空白。

当通用码为 `INTERNAL_ERROR` 时，`Fail` 始终使用 `InternalError()` 的标准内容，清除业务码和明细，确保直接构造结构体或调用 `WithBusinessCode` 也不能更改内部错误的固定输出。业务码的含义及其通用类别归属由所属领域的接口适配层维护，通用组件不维护跨领域业务码注册表。

## 6. 路由与异常的统一处理

未匹配路由统一返回 HTTP 404、`NOT_FOUND` 和消息 `route not found`，不附带业务码，使用同样的 `data: null`、`error` 对象结构。

未处理异常由统一恢复中间件记录日志；尚未写入响应时，输出固定的 HTTP 500 内部错误。响应已经写入后，不追加第二份错误 JSON，也不声明可以撤回已发送内容。

Controller 的可预期错误、未匹配路由和异常恢复均使用公共响应组件输出，避免出现业务响应已经统一、框架错误仍使用其他结构的情况。
