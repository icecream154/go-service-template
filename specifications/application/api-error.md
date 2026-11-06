# 错误声明、传递与响应规范

## 1. 适用范围与职责

1. **本文统一规定错误在哪里声明、如何传递，以及由谁构造接口响应。** 错误声明位置、业务码归属、`Message` 与 `Details` 的来源、Service 文件组织以本文为准。响应外层的 `data/error` 结构见[接口返回规范](api-response.md)，校验规则的业务归属见[接口与业务校验规范](api-service-validator.md)。

2. **每层维护自己产生的信息，接口层负责选择对外错误码。** 公共检查提供基础检查结果，models 提供领域规则结果，Service 提供用例失败原因，Validator 准备请求字段明细，Controller 维护本 Controller 的业务码及错误分类映射。

   | 内容 | 声明位置 | 产生或使用方式 |
   | --- | --- | --- |
   | 通用 `ErrorCode` 枚举及 HTTP 状态映射 | `internal/response/error.go` | Validator、Controller 和框架从已登记的枚举中选择 |
   | `ErrorInfo`、`FieldError`、`BusinessErrorCode` 类型 | `internal/response/error.go` | 接口层构造，公共响应组件输出；`BusinessErrorCode` 类型不包含各业务码的声明 |
   | 具体 `BusinessErrorCode` 常量 | 对应的 `controller/v<version>/<object>_controller.go` 顶部 | 由该 Controller 声明自己使用的业务码，并在本文件维护映射 |
   | `Violation`、`RuleError`、`IndexError`、`KeyError` | `internal/check/error.go` | 各公共检查方法返回，调用方识别其含义 |
   | models 本层的命名错误值 | 所属 `models/<entity>_validation.go`，与规则放在一起 | 只用 `errors.New` 声明本层独立判断产生的错误；下层错误原样返回 |
   | 接口专属校验问题 | 对应的 `validator/v<version>/<use_case>_validator.go` | Validator 直接构造 `FieldError`，无须额外声明一套 Validator 错误类型 |
   | Service 本层的命名错误值 | 对应的 `service/<object>_service.go` 顶部 | 只用 `errors.New` 声明本层独立判断产生的错误；下层错误原样返回 |
   | 接收到的错误与两个错误码的对应关系 | 对应的 Controller 文件内部 | Controller 的私有映射函数集中处理，包括 Service 返回的错误 |

3. **正常失败通过返回值传递，不使用 `panic`。** 文中的“返回错误”指返回 Go `error` 或已经构造好的字段明细。成功返回 `nil`；`panic` 只由框架异常恢复流程兜底，不作为参数错误和业务失败的传递方式。

### 1.1 Service 与 models 的错误声明和返回约束

1. **Service 和 models 均禁止声明新的 error 类型。** 不新增实现 `error` 的结构体、别名类型或错误包装类型，不自行实现 `Error()`、`Unwrap()`、`Is()` 等错误方法。本层需要声明错误时，只使用标准库 `errors.New` 创建命名错误值。

2. **“错误常量”使用包级 `var` 声明，初始化后不重新赋值。** Go 的 `const` 不能保存 `errors.New` 的结果。这里的“常量”表示供各调用点引用的固定错误值，实际写法如下；同一个错误只声明一次，不在每次失败时重新创建。

   ```go
   // ErrRecordNotEditable 表示本层判断资料当前不允许编辑。
   var ErrRecordNotEditable = errors.New("record is not editable")
   ```

3. **本层错误只表达本层独立判断出的失败，不依赖下层错误。** 例如，下层成功返回资料且没有错误，Service 根据资料状态判断当前不允许编辑，可以返回 `ErrRecordNotEditable`。models 自己检查实体字段关系或状态时，也可以返回本层声明的错误。不能根据下层 `err` 的类型、内容或分类，换成另一个本层错误。

4. **下层只要返回非 `nil` 错误，Service 和 models 就原样返回该错误值。** 该约束覆盖公共检查、models、Repository、数据库驱动、发号器等下层调用，不因错误属于参数问题、业务失败或执行故障而例外。

   ```go
   if err := models.ValidateRecordName(params.Name); err != nil {
       return err
   }

   record, err := repository.GetRecord(params.RecordID)
   if err != nil {
       return nil, err
   }
   ```

5. **原样返回不允许增加包装、复制消息或替换错误。** 禁止使用 `fmt.Errorf` 的 `%w` 或 `%v` 包装下层错误，禁止 `errors.Join`、`errors.New(err.Error())`，也禁止将下层错误替换成本层已声明的错误。需要记录操作上下文时可以写入日志，随后仍返回原来的 `err`；不能以补充字段名、转换业务含义或准备响应文案为由改变错误。

6. **公共检查和下层组件已有的错误类型保持原有归属。** `check` 的四种错误类型由公共包维护，Service 和 models 接收后直接传递；本节不要求修改这些公共类型。Controller、Validator 识别接收到的错误，再完成各自负责的响应处理。

## 2. 接口错误结构与字段来源

### 2.1 公共结构

公共响应组件维护以下类型：

```go
type ErrorCode string

type BusinessErrorCode string

type ErrorInfo struct {
    // Code 必填，只能使用公共响应组件登记的通用错误类别。
    // HTTP 状态码由 Code 的固定映射决定。
    Code ErrorCode `json:"code"`

    // BusinessCode 可选，由所属 Controller 声明和选择。
    // 表达需要向调用方区分的具体业务原因，空值时省略。
    BusinessCode BusinessErrorCode `json:"business_code,omitempty"`

    // Message 必填，直接采用本次返回错误的 Error()，不重新组织文案。
    Message string `json:"message"`

    // Details 可选，直接采用错误发生处已经构造的字段明细。
    // 输出环节不修改字段名、原因或顺序，没有明细时省略。
    Details []FieldError `json:"details,omitempty"`
}

type FieldError struct {
    // Field 表示实际请求字段或接口约定的字段路径。
    Field string `json:"field"`

    // Reason 表示 Validator 为该字段准备的失败说明。
    Reason string `json:"reason"`
}

func (err FieldError) Error() string {
    return err.Field + ": " + err.Reason
}
```

### 2.2 四个字段的处理约定

1. **`Code` 表达通用失败类别，由接收错误的接口层选择。** 它只能使用公共组件登记的枚举，不能填入业务码或临时字符串。HTTP 状态由公共响应组件根据 `Code` 确定。

   | ErrorCode | HTTP 状态 | 含义 |
   | --- | --- | --- |
   | `INVALID_ARGUMENT` | 400 | 请求无法绑定，或输入不满足有效约束 |
   | `UNAUTHENTICATED` | 401 | 缺少有效身份凭证 |
   | `PERMISSION_DENIED` | 403 | 无权执行操作 |
   | `NOT_FOUND` | 404 | 目标资源不存在 |
   | `CONFLICT` | 409 | 当前状态与操作要求冲突 |
   | `RATE_LIMITED` | 429 | 请求超过频率或额度限制 |
   | `INTERNAL_ERROR` | 500 | 内部规则错误或未预期的执行失败 |
   | `SERVICE_UNAVAILABLE` | 503 | 服务暂时无法处理请求 |

2. **`BusinessCode` 由单个 Controller 维护自己能够返回的全部取值。** 每个业务码固定对应一个通用 `Code`，不要求与 Service 的内部错误一一对应；多个内部错误可以映射为同一个业务码。没有需要进一步区分的业务原因时，保持为空。

3. **`Message` 直接取接收到的 `err.Error()`。** Controller 可以沿错误链识别类别，但构造响应时仍使用收到的那个错误的 `Error()`，不另写一套消息映射，也不取出内层错误后重新拼接说明。该约定同样适用于 `INTERNAL_ERROR`；公共响应组件不因为选择了某个 `Code` 就再次替换已经提供的消息。

4. **`Details` 直接采用已经准备好的 `[]response.FieldError`。** 接口层原样赋值，公共响应组件原样序列化，不重新推导字段、不改写原因、不排序、不截断，也不从 `Message` 中解析字段。没有传入明细时保持为空；仅有 Service 的 `error` 时，Controller 不自行补造字段明细。

### 2.3 错误文案的责任

1. **本层自行声明的错误在声明处确定文案。** Service 和 models 使用 `errors.New` 提供非空白英文说明，错误类别由错误值或已有类型决定，不能依赖文案比较。因为接口层直接使用 `Error()`，文案变更也会影响实际接口输出。

2. **下层错误的文案随原错误传递，Service 和 models 不重新准备。** 例如，数据库查询返回 `err` 时，Service 可以记录操作上下文，但仍返回同一个 `err`，不能改成 `ErrRecordQueryFailed` 或其他固定说明。Controller 接收这个错误，选择错误码并直接使用其 `Error()`；不在中间层设置“记录后替换”的例外。

3. **Validator 可以在构造字段明细时准备接口需要的说明。** `Reason` 可以直接使用底层 `err.Error()`，也可以在该 Validator 中组织更明确的字段说明。一旦构造成 `FieldError` 并返回，后续输出环节不再加工其内容。

## 3. 公共检查的错误

### 3.1 四种错误类型

1. **`check` 的检查方法使用根包声明的四种错误类型表达失败。** 子包复用这些类型，不为每一种检查再声明一套错误结构，也不返回 HTTP 错误码或业务错误码。

   | 类型 | 表达的信息 | 示例 | 调用方处理 |
   | --- | --- | --- | --- |
   | `Violation` | 数据不满足一条有效规则 | 要求值在 1～10，实际为 20 | 按输入问题处理，Validator 可以构造字段明细 |
   | `RuleError` | 检查条件本身无效 | 下限为 10，上限为 1 | 修正规则；请求处理中按内部错误处理 |
   | `IndexError` | 原始错误发生在集合的哪个下标 | 第 2 个元素检查失败 | 保留位置，继续识别其内部错误 |
   | `KeyError` | 原始错误发生在 map 的哪个键 | 某个键对应的值检查失败 | 保留位置，继续识别其内部错误 |

2. **分类标识不属于另外的错误结构类型。** `ErrInvalidValue` 用于识别输入违规，`ErrInvalidRule` 用于识别无效规则。调用方使用 `errors.Is` 判断分类，使用 `errors.As` 读取具体错误信息。

3. **位置包装不改变错误性质。** `IndexError`、`KeyError` 通过 `Unwrap()` 保留原因。集合检查回调可以返回领域错误，因此包装中的原因不一定也是 `check` 类型；不能只看到集合位置就认定为参数错误，也不能把未识别的回调错误直接归为 HTTP 400。

### 3.2 RuleError 的含义与处理

1. **`RuleError` 表示调用检查方法时给出了无效的检查条件。** 例如：

   ```go
   scalar.IntInRange(20, 1, 10)
   // 返回 Violation：范围有效，数据超出范围。

   scalar.IntInRange(5, 10, 1)
   // 返回 RuleError：下限大于上限，检查条件不成立。
   ```

2. **无效规则由开发者修正，不能要求请求方修改字段来解决。** 配置产生的规则在启动装配时验证，失败则拒绝启动；请求处理中发现无效规则时，停止当前处理并返回 `INTERNAL_ERROR`。不能因为能够调用 `err.Error()`，就把 `RuleError` 转换为普通字段校验失败。

## 4. Validator 的错误处理

### 4.1 三种校验来源

1. **调用公共检查得到输入违规时，Validator 直接构造字段明细。** Validator 已经知道当前请求字段，把字段名和错误说明组合成 `FieldError` 即可，不要求再包装为 `InputError`。构造之前需要确认属于输入违规，`RuleError` 和无法识别的错误走内部错误出口。

   ```go
   // err 已确认为该字段的输入违规。
   detail := response.FieldError{
       Field:  "record_id",
       Reason: err.Error(),
   }
   ```

2. **接口自己的校验逻辑发现输入不符合要求时，直接生成字段明细。** 例如结束时间早于开始时间，可以在 Validator 中构造 `FieldError{Field: "end_time", Reason: "must not be earlier than start_time"}`。这里不需要先创建一个普通 `error`，再把同一段说明转换为字段明细。

3. **调用 models 的领域规则得到输入违规时，采用同样的构造方式。** models 原样返回下层错误；没有下层错误、由本层独立判断失败时，返回本层通过 `errors.New` 声明的错误值。Validator 识别其含义后，用当前请求字段名构造明细；`Reason` 直接取 `err.Error()`，或由 Validator 按接口需要组织。接收到 `RuleError` 或无法识别的错误时，不构造参数明细，按内部错误处理。

### 4.2 字段明细的准备与传递

1. **字段名、原因和收集顺序在 Validator 中确定。** `Field` 使用当前接口实际接收的字段名；集合是否带下标、采用何种字段路径，也在这一阶段确定。独立字段可以分别检查，同一字段只记录实际发现的问题，不能因一个字段失败而误报另一个字段。

2. **允许直接使用底层说明，也允许在此处组织一次接口说明。** 例如 `IndexError.Error()` 已包含下标时，可以直接放入 `Reason`；接口要求将下标体现在 `Field` 时，也可以在 Validator 中准备相应字段路径和原因。这是构造明细的过程，后续接收方不再重复转换。

3. **私有校验函数统一返回字段明细和内部错误，不增加强制的错误包装。** `[]response.FieldError` 承载已知输入问题，第二个返回值承载规则错误或未知失败。各 Validator 使用相同的返回契约：

   ```go
   func validateCreateRecordRequest(
       request *CreateRecordRequest,
   ) ([]response.FieldError, error)
   ```

   | 结果 | 返回值 | 入口的动作 |
   | --- | --- | --- |
   | 校验通过 | `nil, nil` | 调用 Controller |
   | 已知字段问题 | `details, nil` | 构造 `INVALID_ARGUMENT`，原样使用 `details` |
   | 规则错误或未知失败 | `nil, err` | 构造 `INTERNAL_ERROR`，使用 `err.Error()` |

4. **多条明细的响应消息也来自实际返回的错误。** `FieldError` 本身实现 `error`，入口可以选择第一条明细作为本次响应的主错误，同时保留全部明细。例如：

   ```go
   // details 非空，条目均已由 Validator 准备完成。
   err := details[0]
   response.Fail(ctx, response.ErrorInfo{
       Code:    response.CodeInvalidArgument,
       Message: err.Error(),
       Details: details,
   })
   return
   ```

   这里不需要额外的 `InputError` 或统一明细容器；Controller 也不需要接管这条失败请求。

### 4.3 输出与终止

1. **绑定失败由 Validator 入口直接处理。** 选择 `INVALID_ARGUMENT`，消息使用绑定错误的 `Error()`；绑定过程没有提供字段明细时不推导明细。绑定失败后不检查部分 DTO，也不进入 Controller。

2. **字段校验失败由 Validator 入口输出并终止。** 普通字段问题选择 `INVALID_ARGUMENT`，默认不附业务码。私有校验函数只返回结果，不写 HTTP 响应；入口调用 `response.Fail` 后立即 `return`。

3. **内部错误优先终止，不能混入参数错误集合。** 入口先判断内部错误，再处理字段明细。校验中发现 `RuleError` 或未知失败时，返回该错误并放弃已收集的明细，不使用 `panic` 代替正常错误出口。

## 5. Service 的错误声明与文件组织

### 5.1 错误声明位置

1. **Service 自己定义的错误统一放在对应 Service 文件顶部。** 先声明同一 Service 内多个用例共用的错误，再按照文件中用例的排列顺序声明各用例专属错误。顶部的 `shared` 只表示本 Service 内共享，不代表新增公共错误包。

   ```go
   // errors: shared
   var ErrRecordDisabled = errors.New("record is disabled")

   // errors: CreateRecord
   var ErrRecordLimitReached = errors.New("record limit reached")

   // errors: UpdateRecord
   var ErrRecordVersionConflict = errors.New("record version conflict")
   ```

   上述错误均来自本层对成功取得的数据所做的判断：资料已停用、已有资料数量达到业务限制、提交版本与实际版本不一致。下层若已返回错误，就直接返回下层错误，不改成这些错误值。

2. **所有自有错误都使用 `errors.New` 声明，不定义错误类型。** 名称使用 `Err<对象><原因>`。Controller 需要识别的声明使用导出名称；名称包含所属业务对象，避免同一个 Service 包中不同文件重名。声明方式及禁止事项遵守第 1.1 节。

3. **顶部只收纳本 Service 独立产生的错误值。** 不重新声明 `check` 的四种类型，不复制 models 或其他下层已有的错误，也不为下层故障增加一个同义的 Service 错误。下层返回的错误一律原样返回。

4. **错误声明说明产生条件和文案含义。** 各用例方法的注释说明可能返回的 Service 错误，以及调用链可能返回的下层错误；下层错误必须原样传递。Controller 引用相应声明进行映射，不比较 `Error()` 字符串。

### 5.2 按用例组织文件

1. **每个用例在同一个区域依次声明 Params、参数校验方法和处理方法。** 用例输入相关常量与 Params 放在一起；参数校验方法使用 `validate<UseCase>Params` 命名，保持包内可见。用例处理方法先调用该校验方法，通过后再执行依赖业务状态的操作。

2. **仅供一个用例使用的辅助逻辑留在该用例区域。** 多个用例复用的实现统一放在文件尾部的 `shared` 区域，原则上使用私有方法。文件顶部共享错误和文件尾部共享实现分别组织，不把两者混在一起。

3. **Service 文件采用以下顺序。** 该顺序使阅读者先看到错误契约，再按用例读完输入、检查和执行流程，最后查看公共实现。

   ```text
   package、imports

   errors: shared
     本 Service 多个用例共用的错误声明

   errors: CreateRecord
     CreateRecord 专属错误声明

   errors: UpdateRecord
     UpdateRecord 专属错误声明

   Service 类型与构造函数

   use case: CreateRecord
     CreateRecordParams 及配套输入常量
     validateCreateRecordParams
     (*RecordService).CreateRecord
     本用例专属的私有辅助方法

   use case: UpdateRecord
     UpdateRecordParams 及配套输入常量
     validateUpdateRecordParams
     (*RecordService).UpdateRecord
     本用例专属的私有辅助方法

   shared
     多个用例共用的私有方法
   ```

### 5.3 返回与依赖约定

1. **Service 独立保障自己的输入和业务条件。** Params 校验可以复用 `check` 和 models；依赖数据库状态、操作权限或并发条件的判断由用例组织。Validator 已校验过 HTTP 输入，不代表其他入口也已校验。

2. **Service 区分下层返回错误和本层业务判断失败。** 下层查询返回未命中错误、唯一约束错误或连接错误时，均直接返回原始 `err`；不转换成 `ErrRecordNotFound`、`ErrRecordAlreadyExists` 或执行失败错误。在没有下层错误的前提下，本层独立检查输入、数据或状态发现不满足要求时，才返回本层声明的命名错误。

3. **Service 不决定接口错误码，也不构造 HTTP 字段明细。** Service 不依赖 `response`、Controller、Validator 或 Request DTO。HTTP `Code`、`BusinessCode` 由 Controller 选择；没有既有响应明细时 `Details` 为空，Controller 不在收到 Service 错误后重新执行字段校验来补齐它。

## 6. Controller 的业务码与错误映射

### 6.1 声明与组织

1. **每个 Controller 在自己的文件顶部声明会用到的业务码。** 常量类型使用 `response.BusinessErrorCode`。只供本包使用时采用包内名称，名称包含 Controller 对应的业务对象；字符串使用大写英文及下划线，并包含领域或业务对象前缀。

   ```go
   const (
       codeRecordDisabled        response.BusinessErrorCode = "CATALOG_RECORD_DISABLED"
       codeRecordLimitReached    response.BusinessErrorCode = "CATALOG_RECORD_LIMIT_REACHED"
       codeRecordVersionConflict response.BusinessErrorCode = "CATALOG_RECORD_VERSION_CONFLICT"
   )
   ```

   以上是通用示例，不规定任何具体领域必须提供这些业务码。常量属于所在 Go 包，文件本身不构成可见性边界。

2. **同一 Controller 的全部错误分类集中在本文件的私有映射函数中。** 映射覆盖该 Controller 接收的 Service 错误、需要识别的领域错误及公共检查错误。Controller 的各个处理方法复用该函数，不分别编写同义映射，也不另建领域 `apierror` 包保存本 Controller 的业务码和映射。

3. **下层错误由 Controller 直接识别，不要求 Service 预先转换。** 例如，Controller 可以识别仓储或驱动提供的明确未命中错误，选择 `NOT_FOUND`；具体业务码取决于当前用例能否确定其业务含义。唯一约束错误必须确认具体约束，不能把所有重复键都映射为同一个对象已存在。无法确定含义时按内部错误处理，不比较错误消息来猜测原因。

### 6.2 映射与响应装配

1. **映射函数只选择两个错误码。** 使用 `errors.Is`、`errors.As` 识别错误，明确识别的输入问题选择 `INVALID_ARGUMENT`；已知业务失败选择其对应类别及业务码；`RuleError` 和无法识别的错误选择 `INTERNAL_ERROR`，业务码为空。错误带有字段、下标或键不意味着它一定是输入问题。

   ```go
   // recordservice 表示该 Controller 调用的 Service 包。
   func mapRecordErrorCodes(err error) (response.ErrorCode, response.BusinessErrorCode) {
       switch {
       case errors.Is(err, check.ErrInvalidRule):
           return response.CodeInternalError, ""
       case errors.Is(err, recordservice.ErrRecordDisabled):
           return response.CodeConflict, codeRecordDisabled
       case errors.Is(err, recordservice.ErrRecordLimitReached):
           return response.CodeConflict, codeRecordLimitReached
       case errors.Is(err, recordservice.ErrRecordVersionConflict):
           return response.CodeConflict, codeRecordVersionConflict
       case errors.Is(err, check.ErrInvalidValue):
           return response.CodeInvalidArgument, ""
       default:
           return response.CodeInternalError, ""
       }
   }
   ```

2. **响应装配直接使用错误文案和已经传入的明细。** 以下方法要求 `err` 非 `nil`；`details` 可以为空，收到的明细直接交给响应组件。

   ```go
   func writeRecordError(ctx *gin.Context, err error, details []response.FieldError) {
       code, businessCode := mapRecordErrorCodes(err)
       response.Fail(ctx, response.ErrorInfo{
           Code:         code,
           BusinessCode: businessCode,
           Message:      err.Error(),
           Details:      details,
       })
   }
   ```

   Service 仅返回一个错误时，调用 `writeRecordError(ctx, err, nil)`。调用后，Controller 处理方法立即 `return`；辅助方法返回不代表外层处理方法已经终止。

3. **公共响应组件负责序列化与 HTTP 状态选择。** 它根据已登记的 `Code` 确定状态码，输出 `data: null` 和 `error` 对象，并终止后续处理链。对于已经合法构造的错误对象，不再改写 `Message`、`Details` 或 Controller 选择的业务码。

## 7. 错误的完整传递链

1. **请求字段不符合要求时，在 Validator 内完成响应。** 公共检查、领域规则或接口自定义逻辑发现输入问题，Validator 构造字段明细，入口选择 `INVALID_ARGUMENT` 并输出。请求到此结束，不调用 Controller 的业务方法和 Service。

   ```text
   check / models / 接口自定义校验
     → Validator 确认输入问题并准备 FieldError
     → Validator 入口使用错误的 Error() 和已有 Details
     → response 输出 HTTP 400
   ```

2. **业务用例失败时，由 Controller 选择错误码并输出。** Service 对所有下层错误均原样返回；本层独立判断失败时返回自己声明的命名错误。Controller 识别原因，只选择 `Code`、`BusinessCode`，消息取 `err.Error()`，已有明细原样传递。

   ```text
   Service 校验 Params 或执行用例
     → 返回 error
     → Controller 内部映射 Code / BusinessCode
     → Message = err.Error()，Details = 已有明细
     → response 按 Code 输出对应 HTTP 状态
   ```

3. **规则错误和未知失败保持内部错误类别。** Validator 遇到这类错误时直接走内部错误出口；Service 返回这类错误时由 Controller 映射为 `INTERNAL_ERROR`。文案由实际返回的错误提供，不将错误改造成字段违规，也不为了映射失败主动触发 `panic`。

4. **框架错误由框架准备并输出。** 未匹配路由、绑定失败和异常恢复使用同一响应结构。异常恢复先准备可公开的错误说明，不直接把原始 `panic` 值作为响应内容；尚未写出响应时输出错误，已经写出响应时不追加第二份 JSON。
