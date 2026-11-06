# 接口返回规范

## 1. 统一响应结构

HTTP 接口统一返回 JSON，顶层固定包含 `data`、`error` 两个字段。两个字段始终存在，不使用 `omitempty` 省略，也不根据业务用例更换名称。

错误声明位置、各层传递方式、业务码归属及消息来源遵守[错误声明、传递与响应规范](api-error.md)。本文说明响应结构和公共输出组件的使用。

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

func (err FieldError) Error() string {
	return err.Field + ": " + err.Reason
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

失败时 `data` 固定为 `null`，`error` 包含通用错误类别、可选业务错误码及错误消息。项目自行声明的错误使用英文消息，第三方错误保留其原始文案。HTTP 状态码与错误语义保持一致，不以 HTTP 200 承载业务失败。

### 3.1 通用错误码与 HTTP 状态码

`code` 必填，必须使用 `internal/response` 集中声明的固定枚举，不得填入业务错误码或临时字符串。已登记的通用错误码如下：

| 错误码 | HTTP 状态 | 含义 | 构造器默认消息 |
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

表中的默认消息仅供构造器准备默认错误使用。接口已经接收到具体错误时，`message` 直接采用该错误的 `Error()`，包括 `INTERNAL_ERROR`，输出组件不根据错误码重新替换消息。

### 3.2 业务错误码

`business_code` 可选，表达调用方需要识别的具体业务失败原因。通用 `code` 用于统一判断错误类别，调用方需要处理特定业务场景时再判断 `business_code`。业务码使用大写英文及下划线，并包含领域或业务对象前缀，以避免重名。

每个 Controller 在自身文件顶部声明自己能够返回的全部业务码，并在文件内部集中维护错误到 `code`、`business_code` 的映射。同一 Controller 的各接口方法复用该映射，相同业务原因不重复声明。每个业务码固定归属一个通用 `code`，不能在不同分支临时改变类别。业务码含义及其归属属于接口契约，变更时需要考虑已有调用方的兼容性。

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
    "message": "item_id: must be a positive 64-bit integer",
    "details": [
      {
        "field": "item_id",
        "reason": "must be a positive 64-bit integer"
      }
    ]
  }
}
```

`field` 使用实际请求参数名，`reason` 说明该字段违反的约束。Validator 在发现问题时准备字段明细，后续直接使用已有 `details`，不再修改字段名、原因或顺序。没有字段明细时，不返回空 `details` 数组；仅接收到 Service 的 `error` 时，Controller 不自行补造明细。

内部错误同样使用收到的 `err.Error()`。例如，下层返回的错误说明为 `database connection failed`，Service 原样返回该错误时，Controller 可以输出：

```json
{
  "data": null,
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "database connection failed"
  }
}
```

Service 和 models 自己声明的错误在 `errors.New` 中确定文案。Service 可以将明确未命中等结果解释为对象不存在等业务错误；公共检查、models 已表达清楚的输入错误，以及数据库连接、SQL 执行、发号等技术故障均原样返回，不能为了响应文案进行包装、重建或替换。日志可以补充上下文。框架需要默认内部错误时使用 `InternalError()`，其消息为 `internal server error`。

## 4. 内部错误与对外错误的职责

Service 返回的 Go `error` 用于传递失败原因：本层独立判断的失败及明确的业务语义映射使用本层以 `errors.New` 声明的错误值，例如状态不允许操作、发号成功却得到非正 ID、结合当前查询解释出的对象不存在；已有输入错误、下层技术故障及无法确定含义的错误原样返回。models 只声明自身独立判断的领域错误，原样返回其调用的公共检查错误。Service 和 models 都不声明新的 error 类型。对外 `business_code` 是接口调用方依赖的稳定字符串，二者不要求一一对应：多个错误可以映射为同一个业务码；未预期的执行错误映射为 `INTERNAL_ERROR`，不附带业务码。

按以下位置组织错误定义和转换：

| 位置 | 职责 |
| --- | --- |
| `internal/response` | 通用错误枚举、固定 HTTP 状态映射、错误响应结构和输出函数 |
| `internal/check/error.go` | 公共检查的错误类型与分类标识 |
| 所属 models 规则文件 | 使用 `errors.New` 声明本层独立错误；下层错误原样返回，不新增 error 类型 |
| 对应 Service 文件 | 顶部使用 `errors.New` 声明本层独立失败及明确业务语义映射的错误；原样返回已有输入错误和下层技术故障，不新增 error 类型 |
| 对应 Validator 文件 | 绑定并校验请求，准备字段明细，发生错误时直接输出并终止 |
| 对应 Controller 文件 | 声明自身业务码、集中映射接收到的错误，使用原错误消息和已有明细输出响应 |

Service 不构造 `ErrorInfo` 或 HTTP 字段明细，也不需要知道错误最终对应的 `business_code`。Service 解释业务含义后，Controller 使用 `errors.Is`、`errors.As` 识别收到的业务错误或原样传递的下层错误并选择两个错误码，不比较错误字符串决定类别；`message` 直接取收到的 `err.Error()`，已有 `details` 原样传递。具体约束见[错误处理规范第 1.1 节](api-error.md#11-service-与-models-的错误声明和返回约束)。

## 5. 错误构造与输出

接口层使用 `internal/response` 的 `ErrorInfo` 或已有构造器准备错误，再统一通过 `Fail` 输出。Controller 的错误映射只选择通用码和业务码，直接构造结构体时采用 `Message: err.Error()` 和已经准备好的 `Details`。

| 调用方式 | 作用 |
| --- | --- |
| `InvalidArgument(message, details...)` | 构造 `INVALID_ARGUMENT`，可附带字段明细 |
| `Unauthenticated(message)` | 构造 `UNAUTHENTICATED` |
| `PermissionDenied(message)` | 构造 `PERMISSION_DENIED` |
| `NotFound(message)` | 构造 `NOT_FOUND` |
| `Conflict(message)` | 构造 `CONFLICT` |
| `RateLimited(message)` | 构造 `RATE_LIMITED` |
| `InternalError()` | 构造框架兜底使用的默认 `INTERNAL_ERROR`，不限制其他合法内部错误的消息 |
| `ServiceUnavailable(message)` | 构造 `SERVICE_UNAVAILABLE` |
| `failure.WithBusinessCode(code)` | 按值返回附带业务码的错误对象，不修改原对象；对应关系由所属 Controller 维护 |
| `Success(ctx, data)` | 将用例 Response DTO 包装为成功响应，返回 HTTP 200 |
| `Fail(ctx, failure)` | 输出失败响应，并中止后续 Gin 处理链 |

接收 `message` 的构造器在消息为空或全为空白时，使用第 3.1 节列出的默认英文消息。已有具体错误时直接传入其 `Error()`。项目自行声明的错误在声明处准备可公开的非空白英文文案；原样传递的第三方故障明确接受原始文案，各层不为输出而翻译、脱敏、包装或替换。Service 对明确业务结果的解释不属于文案加工。具体责任范围见[错误文案的责任](api-error.md#23-错误文案的责任)。

例如，Validator 已经收集到非空字段明细时，使用第一条明细的 `Error()` 作为消息，并原样传递全部明细：

```go
response.Fail(ctx, response.InvalidArgument(
	details[0].Error(),
	details...,
))
return
```

`Fail` 中止后续 Gin 处理链，不会自动退出当前 Go 函数。Validator 或 Controller 在调用 `Fail` 后必须立即 `return`，避免继续调用业务逻辑或重复写入响应。

`Fail` 输出前校验通用码是否属于已登记的枚举，再根据通用码确定 HTTP 状态。错误定义无效时，组件回退为 HTTP 500 和固定的 `INTERNAL_ERROR`，丢弃原定义的业务码、消息和明细。无效情形包括通用码为空或未登记、消息为空或全为空白、业务码非空但全部为空白。

合法错误对象即使使用 `INTERNAL_ERROR`，`Fail` 也保留已经提供的消息、业务码和明细，不因为 HTTP 状态为 500 而改写内容。业务码及其通用类别归属由所属 Controller 维护，通用组件不维护跨领域业务码注册表。

## 6. 路由与异常的统一处理

未匹配路由统一返回 HTTP 404、`NOT_FOUND` 和消息 `route not found`，不附带业务码，使用同样的 `data: null`、`error` 对象结构。

未处理异常由统一恢复中间件记录日志；尚未写入响应时，使用 `InternalError()` 准备默认 HTTP 500 错误，不直接公开原始 `panic` 内容。响应已经写入后，不追加第二份错误 JSON，也不声明可以撤回已发送内容。

Controller 的可预期错误、未匹配路由和异常恢复均使用公共响应组件输出，避免出现业务响应已经统一、框架错误仍使用其他结构的情况。

## 7. 响应编码与写出失败

1. **公共响应组件先完成 JSON 编码，再写出状态码和正文。** `Success`、`Fail` 使用同一条输出路径，先将完整的 `data/error` 外层编码到内存。编码成功后才设置 JSON 响应头并写入正文，避免编码失败时已经发出原定的成功状态。

2. **编码失败且尚未写出响应时，改为输出框架默认的 HTTP 500。** response 记录编码错误，中止后续 Gin 处理链，并使用 `InternalError()` 构造 `data: null`、`error.code: INTERNAL_ERROR`、`error.message: internal server error`，不附业务码或字段明细。兜底响应最多准备一次，不递归调用 `Fail`；如果兜底本身也失败，记录并终止。这是响应组件自身的故障处理，不改变 Service 错误消息直接取 `err.Error()` 的约定。

3. **已经写出的响应不再覆盖或追加。** 公共输出入口发现响应头或正文已经写出时，记录这一情况并中止处理，不再修改状态码，也不追加第二份 JSON。这里的“已写出”指服务端 Writer 的状态，不代表客户端已经完整接收或处理响应。

4. **写入失败只记录并终止，不再发送错误响应。** 写入返回错误，或实际写入字节数小于正文长度时，由 response 记录原因并中止后续处理链；不重试写入，不再次调用 `Fail`，也不追加 HTTP 500。此时可能已有部分内容发出，或者连接已经不可用，不能承诺还能向客户端送达另一份响应。

5. **输出故障由 response 负责记录，调用方继续遵守返回规则。** 编码、写入和重复输出问题使用英文日志，说明失败阶段和原因，不打印整个 DTO 或 JSON 正文。Controller 无须为同一输出故障再次记录日志。`Abort` 只能阻止后续 Gin 处理器，不能退出当前 Go 函数；`Fail` 后仍须立即 `return`，`Success` 作为处理方法的最后一步调用或紧接 `return`。正常成功输出不额外中止处理链。

6. **响应发送结果与业务执行结果分别判断。** 用户创建、资料更新等业务可能已经提交，随后才出现编码或写入失败；响应组件不重跑业务，也不回滚已完成的写入。Writer 未返回错误同样不等于客户端已收到完整响应，客户端收取结果不由 `Success`、`Fail` 保证。
