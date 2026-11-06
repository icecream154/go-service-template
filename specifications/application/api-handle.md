# 一个接口的完整处理流程

本文面向第一次阅读本项目的开发者，说明一个 HTTP 请求如何进入系统、经过各层处理、访问数据库，再转换成最终响应。以 `createUser` 为主线，最后补充查询、分页、批量和更新接口的差异。

本文描述当前代码的实际调用关系。路由与 DTO 的命名规则见[接口路由与命名规范](api-routing.md)，响应结构与错误码规则见[接口返回规范](api-response.md)。文中的源码路径均以仓库根目录为起点，点击文件链接即可继续阅读实现。

## 1. 先看整体链路

一个请求的主要处理顺序是：

```text
客户端
  → net/http.Server 接收请求
  → Gin 匹配路由，执行公共中间件
  → 对应用例的 Validator
      → Bind：HTTP 参数映射为 Request DTO
      → validateXxxRequest：校验请求字段
  → Controller：将 Request DTO 映射为 Service 参数
  → Service：执行业务流程
  → Repository：执行数据库操作
  → MySQL
```

数据库操作完成后，结果沿 Go 函数调用返回：

```text
Repository 返回表结构或 error
  → Service 返回领域模型或用例 Result；明确业务失败返回业务错误，输入错误、技术故障和未知 error 原样返回
  → Controller 转换为 Response DTO 或接口错误
  → response.Success / response.Fail 写入 HTTP JSON
  → 请求处理结束
```

需要先区分两件事：**启动时注册处理链，请求到来后执行处理链。** `NewRouter`、`RegisterV1Routes` 等方法在启动时执行；每次请求不会重新注册路由、创建数据库连接池或创建默认 Service。

## 2. 请求到来前，系统已经准备了什么

### 2.1 从进程入口到开始监听

进程入口是 [cmd/web/main.go](../../cmd/web/main.go)。`main` 处理退出信号并调用 `run`；`run` 解析启动参数、设置初始化时限，再调用 [bootstrap.Init](../../bootstrap/init.go)。

实际顺序如下：

```text
main → run
  → bootstrap.Init
      → env.Init：读取部署环境和区域
      → 取得雪花数据中心编号
      → 加载雪花配置并初始化共享发号器
      → 加载 MySQL 配置
      → runtime.Init：创建数据库连接和 Repository
      → composition.Init：创建默认 Service
  → routers.New：创建 Controller、Validator 并注册路由
  → net.Listen
  → http.Server.Serve：开始接收 HTTP 请求
```

环境、配置和数据库连接初始化成功后，程序才开始监听端口。默认地址为 `:8080`；启动方式和配置准备见 [README](../../README.md)。数据库及表结构需要提前准备，启动流程不自动建表，也不创建示例用户。

`routers.New` 返回的 `*gin.Engine` 被传给 `serve`，作为 `http.Server.Handler`。因此，监听和 HTTP 协议处理由 `net/http` 承担；收到请求后，它调用 Gin 的 `ServeHTTP`，进入已注册的路由处理链。

### 2.2 共享对象分别由谁持有

| 位置 | 启动时准备的对象 | 请求中如何使用 |
| --- | --- | --- |
| [internal/snowflake](../../internal/snowflake/snowflake.go) | 共享雪花发号器 | 创建用户时调用 `snowflake.NextID()` |
| [internal/runtime](../../internal/runtime/runtime.go) | DB 入口和 Repository | Service 调用 `appruntime.Current()` 获取 |
| [internal/composition](../../internal/composition/services.go) | 默认 `UserProfileService` | Controller 调用 `composition.Current().UserProfile` 获取 |
| [用户领域 Router](../../app/domains/user/routers/router.go) | Controller 和各用例的 Validator | Gin 保存其处理方法，收到请求后调用 |

MySQL 组件为每个启用的命名连接创建独立连接池，完成连通性及会话检查后交给 GORM。用户资料 Repository 复用用户数据库入口；Repository 的构造函数不会另建连接池。

`Current()` 的含义是“取得已经初始化的共享对象”。它本身不创建 Service、Repository 或连接池，也不执行 SQL。初始化前调用会触发异常，因此启动顺序是请求能够正常工作的前提。

当前 Controller 和 Service 都不保存单次请求的数据。Request DTO、Service 参数、操作结果和 Response DTO 在每次请求中独立创建。

### 2.3 处理链中的文件在哪里

用户资料接口的 Validator 和 DTO 都按“版本 → Controller → 用例”组织，Controller 目录保持按版本划分：

```text
app/domains/user/
  controller/v1/
    user_profile_controller.go
  validator/v1/user_profile/
    create_user_validator.go
    ...其他用例的 Validator 文件
  dto/v1/user_profile/
    create_user.go
    ...其他用例的请求与响应文件
    user_profile.go
```

两个 `user_profile` 目录分别是独立的 `userprofile` 包。Router 使用 `userprofilevalidator` 别名导入 Validator 包；Validator 和 Controller 使用 `userprofiledto` 别名导入 DTO 包。每个 DTO 用例文件保留完整的 Request、Response 名称，同一分组共享的 `UserProfile` 结构放在 `user_profile.go`，各用例直接在同包引用。

目前这些 DTO 只在用户资料 Controller 内复用，因此不建立 `dto/v1/shared` 包。将来真正存在跨 Controller 且语义一致的 DTO 时，再按需建立版本内的共享包；具体约定见[接口路由与命名规范](api-routing.md)。

## 3. 跟随一次 createUser 请求

### 3.1 客户端发送请求

以首次注册用户为例：

```http
POST /api/v1/user/userProfile/createUser
Content-Type: application/json
```

```json
{
  "account_source": "example_identity",
  "external_account_id": "external-user-001",
  "user_name": "Example User",
  "avatar_url": null
}
```

客户端提交账户身份和初始资料。内部用户 ID、启用状态、版本及持久化时间由服务端生成。

当前未接入身份认证中间件，账户来源和外部账户标识是请求中的身份声明；这一步不会核验外部身份凭证。

### 3.2 Gin 根据方法和路径找到入口

[routers/web.go](../../routers/web.go) 的 `New` 创建 Gin Engine，安装请求日志、异常恢复中间件，并建立 `/api/v1` 分组。

随后，[用户领域 Router](../../app/domains/user/routers/router.go) 注册领域和 Controller 分组：

```go
user := v1.Group("/user")
userProfile := user.Group("/userProfile")
userProfile.POST("/createUser", r.createUserValidator.CreateUser)
```

合并后的完整路径就是 `/api/v1/user/userProfile/createUser`。这里注册的入口是 **Validator 的 `CreateUser` 方法**。

一次请求到来后，Gin 按 HTTP 方法和路径选择已注册的处理链。公共中间件围绕后续处理执行：

- `gin.Logger` 记录方法、路径、最终状态码和耗时等信息。
- `gin.CustomRecovery` 捕获当前请求处理链中的未处理 `panic`，框架错误的返回方式见第 5 节。

Gin 为本次请求准备 `*gin.Context`，其中的 `Request` 提供原始 HTTP 请求，`Writer` 用于写出响应。Validator、Controller 和响应方法接收的是同一个 `ctx`；它属于单次请求，不保存在共享对象中。这里的 Gin Context 与启动入口用于停止进程的 `context.Context` 是不同对象。

### 3.3 Validator 创建 DTO，Bind 填充字段

进入 [CreateUserValidator.CreateUser](../../app/domains/user/validator/v1/user_profile/create_user_validator.go) 后，首先为本次请求创建 DTO：

```go
var request userprofiledto.CreateUserRequest
if err := commonvalidator.Bind(ctx, &request); err != nil {
    response.Fail(ctx, response.InvalidArgument(err.Error()))
    return
}
```

[internal/validator/bind.go](../../internal/validator/bind.go) 是通用绑定入口：

| HTTP 方法 | 参数来源 | Gin 绑定方法 | DTO 标签 |
| --- | --- | --- | --- |
| GET | Query | `ShouldBindQuery` | `form` |
| POST | JSON 请求体 | `ShouldBindJSON` | `json` |

POST 需要 `Content-Type: application/json`。绑定器不把 Query 和 JSON 合并，也不为每个用例手工解析字段。

[CreateUserRequest](../../app/domains/user/dto/v1/user_profile/create_user.go) 通过字段及标签描述参数，例如：

```go
AccountSource string  `form:"account_source" json:"account_source"`
AvatarURL     *string `form:"avatar_url" json:"avatar_url"`
```

Gin 根据标签把 JSON 字段填入 DTO，并完成类型转换。解析或类型转换失败时，Validator 立即返回 HTTP 400，Controller 和 Service 都不会执行。

绑定只负责把输入变成 Go 数据。字段是否为空、长度是否超限等规则，由下一步的请求校验判断。当前通用绑定使用 Gin 默认 JSON 解码行为；它没有另外实现严格字段检查、请求体大小限制或尾部 JSON 检查。Request DTO 不自定义 `UnmarshalJSON`，相关约束见[请求 DTO 规范](api-routing.md#81-request-dto-的标准绑定与输入语义)。

### 3.4 私有校验函数判断输入是否有效

同一个 validator 文件内的 `validateCreateUserRequest` 组织该用例的字段校验，创建头像的可选分支在这里处理。账户、昵称和头像的完整字段规则统一调用 [models 的领域规则](../../app/domains/user/models/user_profile_validation.go)，由领域规则组合公共数据检查：

| 字段 | 当前规则 |
| --- | --- |
| `account_source` | 非空白、合法 UTF-8，最多 32 个 Unicode 码点 |
| `external_account_id` | 非空白、合法 UTF-8，最多 128 个 Unicode 码点 |
| `user_name` | 非空白、合法 UTF-8，最多 64 个 Unicode 码点 |
| `avatar_url` | 创建时可省略、为 `null` 或空字符串；非空时须为有效 HTTP/HTTPS 地址，最多 512 个 Unicode 码点 |

校验针对绑定后的字段，不改变原值，也不查询数据库。领域规则直接返回公共检查错误，Validator 确认属于输入违规后，使用当前请求字段名和错误的 `Error()` 构造 `response.FieldError`。问题按字段顺序汇总，每个字段最多一条。`RuleError` 或无法识别的错误通过第二个返回值交给入口处理，不混入参数明细。

入口方法根据校验结果决定是否继续：

```go
details, err := validateCreateUserRequest(&request)
if err != nil {
    log.Printf("Validate CreateUser request failed: error=%v", err)
    response.Fail(ctx, response.ErrorInfo{
        Code: response.CodeInternalError, Message: err.Error(),
    })
    return
}
if len(details) > 0 {
    response.Fail(ctx, response.InvalidArgument(details[0].Error(), details...))
    return
}
v.controller.CreateUser(ctx, &request)
```

因此，到达 Controller 时，传入的是已经完成绑定和请求校验的 `*CreateUserRequest`。DTO 直接作为方法参数传递，没有存入 Gin Context 再进行类型断言。

### 3.5 Controller 将 DTO 转成 Service 参数

[UserProfileController.CreateUser](../../app/domains/user/controller/v1/user_profile_controller.go) 接收 `ctx` 和已校验的 DTO：

```go
func (c *UserProfileController) CreateUser(ctx *gin.Context, request *userprofiledto.CreateUserRequest) {
    profile, err := composition.Current().UserProfile.CreateUser(userservice.CreateUserParams{
        AccountSource:     request.AccountSource,
        ExternalAccountID: request.ExternalAccountID,
        UserName:          request.UserName,
        AvatarURL:         request.AvatarURL,
    })
    if err != nil {
        failUserProfileOperation(ctx, err)
        return
    }
    result := userprofiledto.CreateUserResponse(userProfileDTO(profile))
    response.Success(ctx, result)
}
```

Controller 将请求字段组织为 [CreateUserParams](../../app/domains/user/service/user_profile_service.go)，然后调用启动时已经创建的 Service。成功时转换结果，失败时通过本文件的 `mapUserProfileErrorCodes` 选择通用码和业务码，由 `failUserProfileOperation` 使用错误的 `Error()` 输出响应。

这一步把 HTTP 契约与业务调用分开。Service 接收自己的参数类型，不接收 Gin Context 或 Request DTO；Controller 负责输入映射和后续响应转换。

Controller 的业务入参只有 Request DTO，`ctx` 是独立的 HTTP 上下文。Validator 不构造 Service Params；需要将 ID 文本转换为整数等操作时，由 Controller 在构造 Params 时完成。Validator 已按相同解析语义验证成功、且 DTO 值未被修改的纯类型转换，允许忽略错误返回值。例如，按 ID 查询接口在 Controller 中使用 `userID, _ := strconv.ParseInt(request.UserID, 10, 64)`。

### 3.6 Service 决定如何创建用户

[UserProfileService.CreateUser](../../app/domains/user/service/user_profile_service.go) 首先调用私有 `validateCreateUserParams`，用于保障非 HTTP 调用也遵守输入约束。同一 Service 文件顶部先声明共享及用例专属错误，再按用例依次组织 Params、可选 Result、输入常量、参数校验和处理方法；Result 紧接 Params，跨用例复用的实现放在文件尾部的 `shared` 区域。创建结果就是 `models.UserProfile`，因此这个用例直接返回模型，不声明 `CreateUserResult`。

Validator 和 Service 分别组织各自入口的校验，共同调用 models 的字段规则，不各自维护长度或 URL 判断。用例的批量、分页和版本边界及分页默认值声明在 [Service 文件](../../app/domains/user/service/user_profile_service.go) 各用例的 Params 旁，由 Validator 和 Service 引用；默认值由入口准备，Service 不自动改写非法值。

输入通过后，Service 从 `appruntime.Current().Repositories.UserProfile` 取得共享仓储，并执行创建流程：

1. 调用 `snowflake.NextID()` 获取内部用户 ID。发号失败或返回非正 ID 时，终止本次创建。
2. 构造 `tables.UserProfile`，填入账户身份、昵称和头像。
3. 明确设置初始 `IsEnabled = true`、`Version = 0`。
4. 将头像转换为 `sql.NullString`，交给 Repository 写入。
5. 写入成功后，把回填后的表对象转换为 `models.UserProfile` 返回。

头像为 `nil` 时转换为 SQL `NULL`；显式空字符串保留为空字符串。这是创建接口当前允许的输入语义。

Service 没有在这个创建流程中显式开启跨步骤事务。单次 GORM 写入是否使用默认事务，由数据库连接配置决定；更新接口的显式事务流程见第 6 节。

### 3.7 Repository 执行数据库写入

[UserProfileRepository.CreateProfile](../../app/domains/user/repository/user_profile_repository.go) 使用启动时注入的 GORM 数据库入口，访问完整表名 `user_service.user_profile`。

它处理的是 [tables.UserProfile](../../app/domains/user/tables/user_profile.go)，流程为：

1. 复制传入的表对象，避免失败时修改调用方的数据。
2. 填写创建、更新时间，使用 UTC 并对齐毫秒精度。
3. 调用 GORM 的 `Create`，由 GORM 经数据库驱动和连接池向 MySQL 执行写入。
4. 写入成功后，才将本次写入结果回填到传入对象。

表及唯一索引定义见 [database/user/mysql/user_profile.sql](../../database/user/mysql/user_profile.sql)。其中 `(account_source, external_account_id)` 联合唯一，数据库负责保证并发创建时不会出现重复账户身份。

Repository 返回原始 GORM 或驱动错误，不在这里决定 HTTP 状态码、业务错误码，也不输出 JSON。

### 3.8 结果沿调用链返回并写成 JSON

创建成功时，Service 使用 `profileModel` 将回填后的表对象转换为 [models.UserProfile](../../app/domains/user/models/user_profile.go)。创建结果来自成功写入的对象，这条成功路径不会再执行一次查询。

Controller 随后调用 `userProfileDTO`，只选择对外公开的字段，构造 `CreateUserResponse`。该响应类型是同一 DTO 包内 [UserProfile](../../app/domains/user/dto/v1/user_profile/user_profile.go) 的别名，既复用用户资料字段，又保留用例专属名称；Controller 通过 `userprofiledto.UserProfile` 引用这个共享结构。

最后由 [response.Success](../../internal/response/response.go) 完成统一外层包装并写入响应：

```go
response.Success(ctx, result)
```

示例响应如下，用户 ID 仅用于说明结构，实际值由雪花生成器产生：

```json
{
  "data": {
    "user_id": "1234567890123456789",
    "user_name": "Example User",
    "avatar_url": null,
    "is_enabled": true,
    "version": 0
  },
  "error": null
}
```

这里有三项转换需要注意：

- HTTP 状态码为 200，成功结果位于 `data`，`error` 为 `null`。
- `user_id` 的十进制字符串格式由 Response DTO 的 `json:"user_id,string"` 标签控制，避免大整数精度丢失。
- 账户来源、外部账户标识和持久化时间保留在内部数据中，普通资料响应不返回这些字段。

统一响应由代码显式调用 `Success` 或 `Fail` 写出。框架不会在任意返回值外面自动再包装一层 JSON。

## 4. 数据对象为什么会多次转换

创建接口的数据形态依次为：

```text
HTTP JSON
  → CreateUserRequest
  → CreateUserParams
  → tables.UserProfile
  → Repository / MySQL
  → 回填后的 tables.UserProfile
  → models.UserProfile
  → CreateUserResponse
  → response.Response[T]
  → HTTP JSON
```

| 对象 | 表达什么 | 转换发生在哪里 |
| --- | --- | --- |
| Request DTO | 当前接口的输入字段、JSON/Query 格式 | Gin 绑定到 DTO |
| Service Params | 业务用例接受的输入 | Controller 手工映射 |
| `tables.UserProfile` | 数据库列、GORM 标签、SQL 可空值 | Service 构造，Repository 写入或读取 |
| `models.UserProfile` | 业务层返回的用户资料 | Service 的 `profileModel` 转换 |
| Service `<UseCase>Result` | 某个用例组合的业务结果，例如资料列表和总数、缺失 ID | Service 组织；类型紧接同文件对应 Params 声明 |
| 用例 Response DTO | 当前接口允许对外返回的字段 | Controller 从模型或 Result 映射，资料字段由 `userProfileDTO` 转换 |
| `response.Response[T]` | 统一的 `data/error` 外层 | `response.Success` 或 `response.Fail` 构造 |

当前 Repository 直接使用 `tables`，不接收或返回 `models`。当前 `models.UserProfile` 是业务字段结构，其校验和业务流程由现有 Validator、Service 实现。

单条查询、创建和更新的 Service 结果就是 `models.UserProfile`，直接返回该模型。批量查询的 `BatchQueryUserProfilesResult` 将 `UserProfiles` 和 `MissingUserIDs` 放在一起；分页查询的 `QueryUserProfilePageResult` 包含 `UserProfiles` 和 `TotalCount`。这两个结构都声明在 Service 对应用例的 Params 下方，Controller 再将它们映射成各自的 Response DTO，HTTP 字段及外层结构保持一致。返回类型和空结果约定见[Service 的输出契约](api-service-validator.md#74-service-的输出契约)。

这些转换只有 HTTP 到 Request DTO 的部分交给 Gin 自动完成。其余映射在 Controller 和 Service 中明确编写，阅读时可以直接跟随字段赋值查看数据去向。

## 5. 失败时，请求在哪里结束

失败可以在不同阶段发生，不需要把所有错误都交给 Service。

| 失败位置 | 示例 | 当前处理结果 | 是否继续后续业务调用 |
| --- | --- | --- | --- |
| Gin 路由匹配 | 方法或路径未匹配 | 404、`NOT_FOUND`、无业务码 | 不进入用例 Validator |
| 通用绑定 | JSON 格式错误、字段类型不匹配 | 400、`INVALID_ARGUMENT` | 不进入 Controller |
| 请求字段校验 | 昵称为空、字段超长 | 400、`INVALID_ARGUMENT`、字段明细 | 不进入 Controller |
| Service 业务处理 | 更新时实际版本与提交版本不同 | 409、`CONFLICT`、`USER_PROFILE_VERSION_CONFLICT` | Controller 输出错误 |
| Repository 查询 | 目标资料未找到 | 404、`NOT_FOUND`、`USER_PROFILE_NOT_FOUND` | Service 解释为资料不存在，Controller 分类并输出 |
| Service 或数据库 | 发号失败、未预期的 SQL 错误 | 500、`INTERNAL_ERROR`、无业务码 | Controller 直接输出收到的错误文案 |
| 请求处理链 | 未处理的 `panic` | 交给公共异常恢复中间件 | 结束当前请求处理链 |

### 5.1 内部错误如何变成接口错误

1. **Service 解释业务失败，models 检查领域规则，两层都不声明新的 error 类型。** 需要命名错误时使用 `errors.New`，Service 的声明放在[文件顶部](../../app/domains/user/service/user_profile_service.go)。发号器成功返回非正 ID、用户状态不允许修改、提交版本与实际版本不一致，由 Service 自己判断；目标资料明确未命中，则由 Service 解释为资料不存在。公共检查和 models 已表达清楚的输入错误、数据库连接或执行故障、发号失败等原样返回，不增加包装或替换成统一失败错误。

2. **Controller 识别 Service 返回的错误，只选择通用码和业务码。** [Controller](../../app/domains/user/controller/v1/user_profile_controller.go) 在文件顶部声明业务码，由 `mapUserProfileErrorCodes` 集中分类。以资料查询未命中为例，Repository 返回 `gorm.ErrRecordNotFound`，Service 返回 `ErrUserProfileNotFound`；Controller 识别这个业务错误，选择 `NOT_FOUND` 和 `USER_PROFILE_NOT_FOUND`。`failUserProfileOperation` 使用收到的 `err.Error()`，不重新组织文案或补造字段明细。

   查询未命中的响应示例：

   ```json
   {
     "data": null,
     "error": {
       "code": "NOT_FOUND",
       "business_code": "USER_PROFILE_NOT_FOUND",
       "message": "user profile not found"
     }
   }
   ```

   HTTP 状态码为 404。`code` 表达通用类别，`business_code` 表达具体业务原因；通用类别与 HTTP 状态码的映射由 [internal/response/error.go](../../internal/response/error.go) 统一维护。

3. **重复键只有确认业务条件后，才能返回账户已存在。** Service 通过 [mysql.IsDuplicateKey](../../internal/database/mysql/error.go) 识别重复键；MySQL 编号和 GORM 错误兼容逻辑集中在数据库组件，Service 不自行比较魔法数字。该方法不判断具体约束或业务含义，也不修改原始错误。确认属于重复键后，Service 按账户来源及外部标识复查；成功找到该账户后，返回 `ErrAccountAlreadyExists`，由 Controller 输出 HTTP 409、`CONFLICT`、`USER_ACCOUNT_ALREADY_EXISTS` 和 `user account already exists`。这个判断确认账户已经存在，不声称识别了实际冲突索引。复查明确未命中时，原样返回最初的写入错误；复查发生技术故障时，原样返回复查错误，并在日志中记录两次失败原因，不用 `errors.Join` 合并错误。不能仅凭重复键类别将任意冲突归为账户已存在。

4. **执行故障和第三方文案沿同一条原样返回链路处理。** Service 可以在故障出口记录操作上下文，随后仍返回原错误。Controller 对无法识别的失败选择 `INTERNAL_ERROR`，响应消息直接采用收到的 `err.Error()`，包括第三方数据库错误的原始文案。项目自行声明的错误提供可公开的非空白英文说明；第三方文案不翻译、不脱敏、不替换。框架异常恢复仍使用默认的 `internal server error`。完整约定见[错误声明、传递与响应规范](api-error.md)。

### 5.2 Fail、return 和异常恢复各负责什么

`response.Fail` 调用 `AbortWithStatusJSON`，写出统一错误 JSON，并阻止后续 Gin 处理器执行。**它不会自动结束当前 Go 函数**，调用处仍需紧接 `return`，避免继续访问数据库或重复写出响应。

普通 `error` 通过上述分支显式处理；`panic` 由 `gin.CustomRecovery` 捕获。尚未写出响应时，恢复回调输出统一内部错误；响应头或正文已经写出时，只终止后续处理，不覆盖原状态或追加第二段 JSON。

## 6. 其余接口如何套用这条链路

当前五个用户资料接口都注册到各自的 Validator，文件均按“结构体、构造函数、入口方法、私有校验函数”组织。各 `validateXxxRequest` 完整组合自身接口规则，基础类型检查复用 `internal/check`，领域字段复用 models，用例边界和默认值引用 Service Params 的声明。

| 接口 | Validator 入口 | 相比创建接口的主要差异 |
| --- | --- | --- |
| `queryUserProfileByUserId` | [QueryUserProfileByUserID](../../app/domains/user/validator/v1/user_profile/query_user_profile_by_user_id_validator.go) | GET Query 绑定 ID 文本，校验为正 int64；Controller 转为整数，Service 将明确未命中解释为资料不存在，技术故障原样返回 |
| `queryUserProfilePage` | [QueryUserProfilePage](../../app/domains/user/validator/v1/user_profile/query_user_profile_page_validator.go) | 绑定前填默认页码 1、每页 20；仓储执行总数及本页查询，Service 通过 `QueryUserProfilePageResult` 返回资料和总数 |
| `batchQueryUserProfiles` | [BatchQueryUserProfiles](../../app/domains/user/validator/v1/user_profile/batch_query_user_profiles_validator.go) | 绑定 ID 字符串数组，Controller 转为整数数组；Service 去重后通过 `BatchQueryUserProfilesResult` 返回资料与缺失 ID |
| `updateUserProfile` | [UpdateUserProfile](../../app/domains/user/validator/v1/user_profile/update_user_profile_validator.go) | 校验目标、发起方、版本和完整资料；Service 使用显式事务、行锁及版本条件完成更新 |

补充阅读时容易混淆的细节：

- 分页默认值仅在未提供参数时保留，显式零值会被校验拒绝。状态筛选先绑定成可选字符串，只允许 `true`、`false`，Controller 再映射为 Service 的可选布尔值。
- 分页总数与本页数据分别读取，固定按用户 ID 降序，不承诺并发写入期间来自同一个数据库快照。
- 批量查询的数量限制按去重前计算。部分或全部用户不存在仍是成功查询，缺失 ID 放在 Result 字段中；成功时资料列表和缺失 ID 列表均为非 nil 切片。分页无匹配记录时仍返回 Result，列表为空并保留实际总数。Service 执行失败返回 `nil, err`，不提供可继续使用的部分结果。
- 更新版本使用 `*int64`，缺失或 `null` 不合法，版本 `0` 合法。更新头像是必填的有效 URL 字符串，这个 HTTP 接口不允许清空；创建及查询结果中的可空头像仍保留原有语义。
- 更新 Service 先判断本人操作，再在事务中锁读用户，检查启用状态及实际版本，执行带版本条件的更新，读取更新结果，提交成功后才返回。事务内的 Repository 使用本次 `tx`，不会切回普通连接。

## 7. 请求完成与进程退出

正常响应写出后，Controller 和 Validator 依次返回，Gin 的请求日志中间件记录最终状态与耗时，HTTP 服务将响应发送给客户端。共享 Service、Repository 和数据库连接池继续供后续请求使用，不在每个请求结束时关闭。

进程收到退出信号时，由 [main.serve](../../cmd/web/main.go) 调用 `http.Server.Shutdown`，停止接收新连接并等待在途请求。HTTP 收尾后，`run` 的 `defer` 才调用 `Runtime.Close`，由 MySQL Manager 关闭其持有的连接池。

当前启动总时限默认 30 秒，HTTP 收尾时限默认 10 秒。收尾超时会强制关闭 HTTP 连接；网络连接关闭不代表业务事务已经回滚。当前 Service、Repository 方法也没有逐次接收 HTTP 请求的 Context，不能将客户端断线理解为 SQL 必然自动取消。

## 8. 第一次看代码时的阅读顺序

建议先完整跟随 `createUser`，再对照其他接口：

| 顺序 | 文件 | 重点查看 |
| --- | --- | --- |
| 1 | [cmd/web/main.go](../../cmd/web/main.go) | `run`、`serve`：依赖初始化与 HTTP 服务启动、关闭 |
| 2 | [bootstrap/init.go](../../bootstrap/init.go) | `Init`：启动顺序 |
| 3 | [internal/runtime/runtime.go](../../internal/runtime/runtime.go)、[internal/composition/services.go](../../internal/composition/services.go) | DB、Repository、Service 分别在哪里创建与保存 |
| 4 | [routers/web.go](../../routers/web.go) | Gin、中间件、API 前缀和版本 |
| 5 | [用户领域 Router](../../app/domains/user/routers/router.go) | 完整路径如何拼接、实际注册哪个 Validator 方法 |
| 6 | [create_user_validator.go](../../app/domains/user/validator/v1/user_profile/create_user_validator.go)、[bind.go](../../internal/validator/bind.go) | 请求绑定、字段校验、失败时提前返回 |
| 7 | [create_user.go](../../app/domains/user/dto/v1/user_profile/create_user.go)、[user_profile.go](../../app/domains/user/dto/v1/user_profile/user_profile.go) | 请求字段类型及标签、专属响应类型及同组共享字段 |
| 8 | [user_profile_controller.go](../../app/domains/user/controller/v1/user_profile_controller.go) | `CreateUser`、`userProfileDTO`、`failUserProfileOperation` |
| 9 | [user_profile_service.go](../../app/domains/user/service/user_profile_service.go) | 共享仓储查找、发号、创建流程及模型转换 |
| 10 | [user_profile_repository.go](../../app/domains/user/repository/user_profile_repository.go)、[user_profile.go](../../app/domains/user/tables/user_profile.go) | SQL 的执行入口、表映射及回填 |
| 11 | [Controller 错误映射](../../app/domains/user/controller/v1/user_profile_controller.go)、[response.go](../../internal/response/response.go) | `mapUserProfileErrorCodes` 如何分类，最终 JSON 如何写出 |

沿这个顺序阅读，可以把一个请求的入口、数据形态、业务执行、数据库访问和最终响应对应到具体方法，而无需先遍历整个项目。
