# 公共身份认证与 JWT 实现

## 1. 适用范围与文件职责

1. **公共契约、凭证实现、HTTP 适配和项目身份名称各自归属对应层。** `authn` 表示 authentication，即身份认证。`internal/authn` 回答“如何签发和验证凭证、认证成功得到什么身份、失败如何返回”，JWT 子包提供具体实现；`app/http/middleware` 负责读取凭证、保存身份及输出认证失败；`app/http/authz` 为 Controller 提供共用的身份要求检查与项目主体标识转换；`app/identity` 集中定义项目身份体系的名称及含义，具体约定见第 2.4 节。

   | 文件 | 声明内容 | 职责 |
   | --- | --- | --- |
   | [principal.go](../../internal/authn/principal.go) | `PrincipalKind`、身份类别常量、`Principal` | 统一表达认证成功后的调用方身份 |
   | [authenticator.go](../../internal/authn/authenticator.go) | `Credential`、`Authenticator` | 统一认证输入和调用方法 |
   | [issuer.go](../../internal/authn/issuer.go) | `IssuedCredential`、`Issuer` | 统一签发输入、凭证结果和有效期信息 |
   | [error.go](../../internal/authn/error.go) | 通用认证错误值 | 区分签发输入与凭证验证的明确失败 |
   | [jwt/config.go](../../internal/authn/jwt/config.go) | JWT 配置与加载方法 | 加载指定环境配置，校验字段及直接配置的签名密钥 |
   | [jwt/provider.go](../../internal/authn/jwt/provider.go) | `Provider` | 使用 HS256 签发和验证 JWT |
   | [jwt/runtime.go](../../internal/authn/jwt/runtime.go) | `Init`、`Current` | 初始化并访问进程共享的 JWT 实例 |
   | [authentication.go](../../app/http/middleware/authentication.go) | `AuthenticateIfPresent` 及私有凭证读取、错误处理方法 | 无凭证时匿名放行，有凭证时执行认证并控制后续处理 |
   | [HTTP principal.go](../../app/http/middleware/principal.go) | `PrincipalFromContext` 及私有保存方法 | 在当前 Gin Context 中按值保存、读取可信身份 |
   | [authz/principal.go](../../app/http/authz/principal.go) | `RequirePrincipal`、公共身份检查错误值 | 检查当前请求是否已有身份，以及类别和命名空间是否符合调用方要求 |
   | [authz/member.go](../../app/http/authz/member.go) | `RequireMemberUserID`、成员标识错误值 | 按项目约定把 `user/member` 的主体标识转换为正 int64 |
   | [user_namespace.go](../../app/identity/user_namespace.go) | 项目用户命名空间常量 | 维护用户身份体系的名称、语义与扩展约定 |
   | [service_namespace.go](../../app/identity/service_namespace.go) | 项目服务命名空间常量 | 维护管理服务等服务身份体系的名称、语义与扩展约定 |

2. **根包类型只表达契约，签发与验证必须显式调用具体实现。** 创建 `Credential`、填写 `Principal` 或声明一个 `Authenticator` 变量，都不会校验凭证、建立可信身份或访问外部依赖。`internal/authn/jwt` 的 `Issue`、`Authenticate` 执行具体签发与验证；导入包或初始化共享实例不会自动让 HTTP 接口启用认证。

3. **公共认证契约与业务模型、传输方式和响应格式独立。** 包不依赖 Gin、业务 domain、Request DTO 或 `response`，不声明业务权限、对外状态码或业务错误码。业务用户资料、组织关系及其他领域数据保留在各自模型中。

4. **项目身份枚举与公共契约分别维护，由使用方组合。** `app/identity` 只声明项目常量及其含义，不包含认证实现，也不依赖 Service 或 Repository。`internal/authn` 不反向导入 `app/identity`，`app/identity` 的字符串常量也不需要导入 `internal/authn`；认证实现和身份使用方分别引用两者，将项目常量填入通用的 `Principal.Namespace`。

## 2. 可信身份 Principal

### 2.1 结构与字段

```go
type PrincipalKind string

const (
    PrincipalKindUser    PrincipalKind = "user"
    PrincipalKindService PrincipalKind = "service"
)

type Principal struct {
    Kind      PrincipalKind
    Namespace string
    SubjectID string
}
```

1. **`Kind` 表达调用方的主体大类。** 当前有效值为 `PrincipalKindUser` 和 `PrincipalKindService`；空字符串及未纳入公共契约的自定义字符串不能作为成功认证的结果。普通用户、员工和外部用户都属于用户类别，具体身份体系通过 `Namespace` 区分。新增主体大类遵守第 2.3 节的扩展约定。

2. **`Namespace` 标识主体所属的身份命名空间。** 成功结果必须提供非空白的命名空间；其取值由可信配置或已验证的身份信息确定，不直接采信凭证中尚未验证的声明。不同来源可以映射到同一项目命名空间中的不同主体；只有存在明确、可信的身份映射时，才能把多个来源的账户认定为同一主体。不能因为外部账户 ID 或名称相同，就返回相同的 `Kind`、`Namespace` 和 `SubjectID`。

3. **`SubjectID` 标识该命名空间中的具体主体。** 成功结果必须提供非空白的标识，使用字符串以兼容数字用户 ID、外部账户标识和服务标识。公共包不要求主体已经拥有某个业务系统的内部用户 ID，也不负责将外部标识转换成内部 ID。

### 2.2 身份的确定与使用

1. **完整身份由 `Kind`、`Namespace`、`SubjectID` 共同确定。** 比较身份时必须同时考虑三个字段，不能只比较 `SubjectID`。例如下列结果表示不同的主体；表格用于说明身份语义，项目已声明的命名空间为 `member` 和 `admin`，其他名称不是已登记常量：

   | Kind | Namespace | SubjectID | 含义 |
   | --- | --- | --- | --- |
   | `user` | `member` | `123` | 普通用户体系中的用户 123 |
   | `user` | `staff` | `123` | 员工体系中的员工 123 |
   | `user` | `externalAccounts` | `account-abc` | 已验证的外部账户，不要求已有内部用户 ID |
   | `service` | `admin` | `adminService` | 管理服务体系中的一个具体服务，主体标识仅为示例 |
   | `service` | `internalServices` | `orderService` | 内部服务体系中的订单服务 |

2. **身份字段按认证实现约定的原文保存，不由公共包自动规范化。** 公共结构不会修剪首尾空白、转换大小写或解析数字。身份比较采用字段的精确值；如果某个身份体系需要规范化，必须由该认证实现按明确规则完成，不能在不同使用位置临时采用不同处理方式。

3. **可信性来自认证过程，不来自结构体本身。** `Principal{}` 是无效的零值，不表示匿名认证成功；手工填写一份非零结构体也不能证明凭证真实。只有认证器完整验证后返回的成功结果，才能作为本次认证的身份依据。

4. **成功返回的身份按只读值使用。** `Principal` 仅包含身份类别、命名空间和主体标识，不包含原始凭证或完整业务用户资料。字段导出便于认证实现构造结果，但接收方不得通过修改类别、命名空间或主体标识冒充另一个认证结果。

5. **多个来源对应同一主体，必须有项目确认的账户对应关系。** 例如来源 A 的账户 `abc` 和来源 B 的账户 `xyz`，只有在项目经过凭证验证、账户绑定等明确过程，确认它们都对应普通用户 `1001` 后，才可以都返回 `Kind=user`、`Namespace=member`、`SubjectID=1001`。这份由项目确认并维护的对应关系，就是“可信身份映射”；不能仅凭请求方声称、外部 ID 相同或姓名相同建立关系。不同来源也可以分别对应 `member/1001`、`member/1002`，共享命名空间不代表身份相同。这里仅说明契约，不表示公共类型已实现账户绑定或映射存储。

### 2.3 Namespace 与 PrincipalKind 的扩展约定

1. **同一主体大类下新增独立身份体系，由业务扩展 `Namespace`。** 当调用方仍然属于用户或服务，但需要区分独立的账户体系或主体标识范围时，业务定义相应命名空间，并明确其可信来源及映射规则，不需要修改公共 `PrincipalKind`。例如普通用户与员工可以采用不同命名空间，内部服务与合作方服务也可以分别定义。仅增加同一项目账户的另一种可信登录来源，不一定需要新增命名空间，应按第 2.2 节的身份映射约定处理。

2. **命名空间划分身份范围，不代替角色、权限或具体主体标识。** 同一个用户获得管理员权限或改变会员等级，不因此更换 `Namespace`；同一身份体系新增用户或服务，通常只需新增 `SubjectID`。同一身份跨业务使用时，也不需要按业务 domain 重新划分命名空间，避免把同一主体误建为多个身份。

3. **现有主体大类无法表达新主体时，统一扩展公共 `PrincipalKind`。** 例如业务确实需要将设备作为独立身份大类，而现有用户、服务语义均不适用时，可以在公共包中增加相应常量。`device` 仅用于说明扩展条件，不属于当前已经支持的类别；不能只因某个业务增加了一种用户或服务，就新增一个 Kind。

4. **新增主体大类必须同步公共契约及受影响的认证和使用逻辑。** 在 `principal.go` 中集中声明新常量及语义，同步本文的有效类别说明、受影响认证器的类别允许值与身份生成逻辑，以及使用方对类别的判断。不支持新类别的认证器仍然拒绝该类别；声明常量本身不会自动增加认证能力，也不会授予业务权限。

5. **Go 类型允许构造字符串，不代表公共契约允许任意扩展。** `PrincipalKind` 的底层类型是 `string`，业务包可以在语法上声明新的值，但只有纳入公共契约并完成相应实现的类别，才能用于成功认证结果。业务可以自行定义命名空间；主体大类必须在公共包中统一维护，不能由各业务私自增加后直接使用。

### 2.4 项目命名空间的声明、引用与扩展

1. **命名空间按主体大类分文件声明，两个文件都属于 `identity` 包。** 文件划分用于组织代码，不产生两个独立的包，也不提供文件级可见性。用户与服务的声明位置、命名前缀及适用类别如下：

   | 声明位置 | 常量命名方式 | 必须搭配的 Kind | 已声明内容 |
   | --- | --- | --- | --- |
   | `app/identity/user_namespace.go` | `UserNamespace<身份体系>` | `authn.PrincipalKindUser` | `UserNamespaceMember = "member"` |
   | `app/identity/service_namespace.go` | `ServiceNamespace<身份体系>` | `authn.PrincipalKindService` | `ServiceNamespaceAdmin = "admin"` |

2. **常量名称表达主体类别和具体身份体系，注释说明身份范围。** 使用 Go 导出标识符的命名方式，不使用全大写下划线。`UserNamespaceMember` 表示普通用户账户体系，`member` 不表示会员等级或角色；`ServiceNamespaceAdmin` 表示管理服务身份体系，`admin` 不表示用户的管理员角色。新增常量时说明其可信身份来源、覆盖的账户或服务范围，以及 `SubjectID` 在该体系中的含义。服务文件在实际身份体系明确后再增加对应常量，不因公共包存在 `PrincipalKindService` 就登记一个空泛的服务命名空间。

3. **认证实现与身份使用方引用同一常量，并确认类别与命名空间匹配。** 例如，以下片段只演示如何组合公共类型与项目常量，`verifiedSubjectID` 表示认证过程已经确认的普通用户标识，结构体赋值本身不执行认证：

   ```go
   principal := authn.Principal{
       Kind:      authn.PrincipalKindUser,
       Namespace: identity.UserNamespaceMember,
       SubjectID: verifiedSubjectID,
   }
   ```

   使用方判断命名空间时也引用 `identity.UserNamespaceMember`，不另写 `"member"`。用户常量与用户类别配合，服务常量与服务类别配合；不能返回 `PrincipalKindService` 搭配 `UserNamespaceMember`。`Namespace` 保持 `string` 类型，常量命名和文件分类都不会自动校验这种对应关系。

4. **新增命名空间时，同步可信身份来源映射及受影响的身份判断。** 新增独立用户身份体系，在用户文件中增加 `UserNamespace` 前缀的常量；新增独立服务身份体系，在服务文件中增加 `ServiceNamespace` 前缀的常量。随后明确哪些认证实现能产生该身份、使用方接受哪些 `Kind + Namespace` 组合，并同步相关约定。同一体系只新增具体主体时使用新的 `SubjectID`，不新增命名空间。常量声明不会自动增加认证能力、业务权限或代表其他主体调用的资格。

5. **项目命名空间扩展不要求公共包依赖业务枚举。** `PrincipalKind` 及公共结构仍在 `internal/authn` 维护，具体命名空间仍在 `app/identity` 维护。认证实现和身份使用方可同时依赖这两个包；公共包不导入项目包，项目常量包也不导入认证实现或业务执行层，避免为读取常量而形成反向依赖。

## 3. 认证输入 Credential

```go
type Credential struct {
    Scheme string
    Value  string
}
```

1. **`Scheme` 表示采用哪一种凭证方案。** 例如 `Bearer`。具体认证实现决定支持哪些方案，并按照相应协议判断方案名；公共结构不自动改写大小写、不选择认证器，也不因为方案名称相同就认定主体属于同一身份类别。

2. **`Value` 保存该方案使用的凭证原文。** 例如访问令牌的完整值，不包含方案名称。原文交给认证器验证，公共结构不自行解码、修剪或转换。凭证包含敏感信息，不记录完整 `Credential`、不将 `Value` 放入错误文案，也不复制到 `Principal` 中。

3. **缺少凭证与凭证不完整是不同失败。** `Scheme` 和 `Value` 均为空或仅含空白时，使用 `ErrMissingCredential`；仅有一个字段缺失或为空白，以及凭证不符合对应方案要求时，使用 `ErrInvalidCredential`。已经提供完整输入、但认证实现不支持该方案时，使用 `ErrUnsupportedScheme`。

## 4. 认证与签发的调用契约

### 4.1 Authenticator：验证已有凭证

```go
type Authenticator interface {
    Authenticate(
        ctx context.Context,
        credential Credential,
    ) (Principal, error)
}
```

1. **成功必须同时满足凭证验证通过与身份结果完整。** 认证实现确认凭证真实性、适用范围及有效性后，返回 `Principal` 和 `nil`。其中 `Kind` 必须属于已声明类别，`Namespace` 与 `SubjectID` 必须非空白。不能把仅解析出来、尚未验证的字段作为成功结果，也不能返回 `Principal{}, nil`。

2. **失败必须返回零值身份和非 nil 错误。** 返回方式为 `return Principal{}, err`。接收方先检查 `err`，失败时不继续使用身份结果；不能返回一部分可信、一部分未验证的身份，让接收方自行猜测是否可以使用。

3. **Context 只服务于本次认证执行。** 调用方传入非 nil 的标准 `context.Context`；不需要取消或截止时间时可以使用 `context.Background()`。认证实现应尊重取消与截止时间；需要访问外部依赖时，为调用设置有限的执行预算，并且不延长调用方已有的截止时间。取消和超时按实际错误返回，不伪装成凭证无效。

4. **认证实现应支持并发调用。** 实例可以持有只读配置以及可并发使用的依赖，但本次凭证、解析结果和身份保存在调用局部变量中，不存入共享实例字段。存在可变缓存等共享状态时，由实现自行保证同步；接口声明本身不提供并发保护。

5. **身份类别与凭证方案分别判断。** 同一种令牌方案可以表示用户，也可以表示服务。认证实现必须依据经过验证的信息及可信配置生成 `Kind`、`Namespace` 和 `SubjectID`，不能仅按凭证名称或未经验证的身份声明决定结果。

### 4.2 Issuer：为可信身份签发凭证

```go
type IssuedCredential struct {
    Credential Credential
    IssuedAt   time.Time
    ExpiresAt  time.Time
}

type Issuer interface {
    Issue(ctx context.Context, principal Principal) (IssuedCredential, error)
}
```

1. **签发者接收由可信调用流程确定的身份，不自行证明身份来源。** 调用方必须先确认有权为该主体签发凭证，再调用 `Issue`。主体格式及允许组合的检查不能代替业务授权；不能把未经验证的外部请求字段直接作为签发身份。

2. **签发成功同时返回凭证及其签发、失效时间。** `Credential` 保存方案与凭证正文，`IssuedAt`、`ExpiresAt` 与凭证内的时间一致。签发失败返回零值 `IssuedCredential` 和非 nil 错误，不交付部分签名结果；签发输入无效使用 `ErrInvalidPrincipal`，取消和技术故障保留实际错误。

3. **签发和认证共用 Context 与并发使用约定。** 调用方传入非 nil Context；执行过程应响应取消，失败后不返回可用结果。实现可以共享只读配置和密钥，单次身份及凭证使用局部变量。

## 5. 通用认证错误

### 5.1 错误的声明与含义

1. **通用认证错误使用包级 `var` 和 `errors.New` 声明。** 这些值初始化后不重新赋值，不为它们增加自定义错误类型。错误消息为固定英文说明，不包含凭证原文、签名密钥或具体主体信息。

   | 错误值 | 英文消息 | 使用条件 |
   | --- | --- | --- |
   | `ErrMissingCredential` | `authentication credential is missing` | `Scheme` 和 `Value` 均为空或仅含空白 |
   | `ErrUnsupportedScheme` | `authentication scheme is not supported` | 完整凭证使用了当前认证实现不支持的方案 |
   | `ErrInvalidCredential` | `authentication credential is invalid` | 凭证不完整、格式或验证结果无效，或已确认被撤销 |
   | `ErrExpiredCredential` | `authentication credential has expired` | 已确认凭证真实性，并确定其有效期已经结束 |
   | `ErrInvalidPrincipal` | `authentication principal is invalid` | 签发调用传入不完整或不受支持的身份 |

2. **凭证过期必须是经过验证后的判断。** 不能只解码出一个过去的时间，就返回 `ErrExpiredCredential`；未通过真实性验证的输入仍属于无效凭证。无法区分具体凭证失败原因时，可以返回 `ErrInvalidCredential`，但技术故障不属于此范围。

3. **签发身份无效与接收到的凭证无效分别表达。** `ErrInvalidPrincipal` 用于 `Issue` 拒绝调用方给出的身份，不表示客户端持有一份无效凭证。认证时发现凭证中的身份不符合要求，返回 `ErrInvalidCredential`，不使用签发输入错误。

### 5.2 错误的识别与传递

1. **调用方使用 `errors.Is` 识别错误，不比较错误文案。** 上述错误是彼此独立的值，没有父子分类关系。例如：

   ```go
   errors.Is(authn.ErrExpiredCredential, authn.ErrExpiredCredential) // true
   errors.Is(authn.ErrExpiredCredential, authn.ErrInvalidCredential) // false
   ```

   需要统一处理多种凭证失败时，显式判断对应错误值；不能假设判断 `ErrInvalidCredential` 就会覆盖缺失、方案不支持或过期。

2. **认证实现只把能够确认的凭证失败映射为通用认证错误。** 具体凭证验证组件明确报告签名不匹配、已撤销或过期时，可以按照其含义返回对应错误值。无法确认是凭证问题时，不替换成 `ErrInvalidCredential`，也不通过重新创建相同文案的 `errors.New` 值模拟已有错误。

3. **技术故障和 Context 错误原样返回。** 网络、数据库、配置或其他执行故障，以及取消和截止时间错误，保留收到的原错误。不得为了统一认证失败而将其替换成四个凭证错误之一，也不得在失败后返回零值身份和 `nil`。公共包不负责重试、记录日志或决定错误的对外表现。

## 6. HS256 JWT 实现

### 6.1 组件职责与依赖

1. **`jwt.Provider` 同时实现 `authn.Issuer` 和 `authn.Authenticator`。** 组件使用 [`github.com/golang-jwt/jwt/v5` v5.3.1](https://pkg.go.dev/github.com/golang-jwt/jwt/v5@v5.3.1) 执行 JWT 签名与解析，算法固定为 HS256，不接受凭证自行选择其他算法。它不依赖 Gin、业务模型或 `app/identity`，不从数据库查询用户或服务状态。

2. **允许的身份组合由应用装配传入。** `New(config, allowedNamespaces)` 的第二个参数为 `map[authn.PrincipalKind][]string`，明确每种主体类别允许使用哪些命名空间。构造时校验并复制这份配置；空允许集合、未知主体大类或无效命名空间不能创建组件。命名空间必须为非空白的有效 UTF-8 文本，按原文精确匹配。组件保存的配置和密钥不随调用方修改输入集合而改变。

3. **签名保证凭证完整性，不对正文加密。** 凭证只保存身份及验证必需的信息，不放入密码、密钥或其他敏感业务字段。HS256 使用共享密钥，持有该密钥的一方同时具有签发能力；普通调用方只持有签发后的凭证，不持有服务端密钥。

### 6.2 配置文件与字段

1. **每个部署环境只读取自己的 JWT 配置文件。** 配置位于 `config/authn/jwt/dev.yml`、`test.yml`、`prod.yml`。`LoadConfig` 接收该叶子目录及 `dev`、`test` 或 `prod`，文件缺失不回退到其他环境；拒绝空文档、多文档、未知或重复字段及不符合约定的字段类型。

   ```yaml
   issuer: go-service-template/dev
   audience: go-service-template
   signing_key: "" # 仅为占位，使用前替换为标准 Base64 编码的随机密钥
   token_ttl: 24h
   ```

   | 字段 | 含义及约束 |
   | --- | --- |
   | `issuer` | 本组件认可的签发方，非空白且不带首尾空白；不同环境模板分别使用对应环境后缀 |
   | `audience` | 凭证适用的接收方，非空白且不带首尾空白；验证时必须匹配 |
   | `signing_key` | 签名密钥的标准 Base64 文本，解码后至少 32 字节；直接保存在 `Config.SigningKey`，不填写环境变量名或占位表达式 |
   | `token_ttl` | 新凭证有效期，使用 `24h` 等 Go 时长文本，至少 1 秒且必须是整秒 |

2. **四个字段都必须显式提供，密钥随配置一起读取和校验。** `Config.SigningKey` 使用 `string` 和 `yaml:"signing_key"` 保存配置中的密钥文本；`LoadConfig`、`Validate` 检查其内容，`New` 再从该值解码并保存只读的签名密钥。整个过程不读取或展开任何密钥环境变量。环境文件中的 `24h` 是显式配置值，不是缺失时的回退值；上例的空密钥只是文档占位，未替换就会校验失败。

3. **密钥在准备配置时生成，运行时不生成或补入默认值。** 可使用 `openssl rand -base64 32` 生成 32 字节安全随机数据的标准 Base64 文本，将结果直接填入对应环境文件的 `signing_key`。缺失、空值、非规范 Base64、包含空白或解码长度不足均使配置校验失败。长度检查不证明密钥具有足够随机性，不能用人工口令代替随机密钥。HS256 的密钥长度要求见 [RFC 7518 §3.2](https://www.rfc-editor.org/rfc/rfc7518.html#section-3.2)。

4. **同一部署的实例使用相同配置密钥，不同环境使用独立密钥。** 服务重启时重新读取环境文件中的既有值，运行中修改文件不影响已创建的 Provider。替换 `signing_key` 并重启后，使用旧密钥签发的凭证无法通过新实例验证；本组件不提供多密钥轮换或在线重载。`Config` 本身包含敏感密钥，不能记录整个配置对象；配置及初始化错误只说明字段或失败原因，不回显密钥值。

### 6.3 启动初始化与访问

1. **`bootstrap.Init` 在数据库创建前完成 JWT 配置加载和初始化。** 启动层按已确定的环境读取 JWT 配置，通过 `authnjwt.Init` 发布共享实例。装配层使用项目常量传入 `user/member`、`service/admin` 两个允许组合，公共 JWT 包不写死这些业务命名空间。配置或密钥不合法时，错误向上传递，应用停止启动并且不监听 HTTP。

2. **`Init` 只允许成功初始化一次，`Current` 返回已经发布的实例。** 初始化失败不发布部分实例，成功初始化后再次调用 `Init` 返回错误并保留原实例。初始化必须早于业务并发调用；成功后使用 `authnjwt.Current()` 获取同一个只读配置的 Provider。初始化前调用 `Current` 会 panic，表示调用顺序错误；也可以通过 `New` 单独构造不影响共享实例的 Provider。组件不持有连接或后台任务，无须增加停机关闭步骤。

3. **组件初始化不等于接口认证已启用。** 初始化只创建可被调用的签发与验证组件，不自动注册 HTTP 认证中间件或调用 `Issue`。是否签发凭证、验证身份及执行授权，由各业务入口显式调用并按领域专属规范处理，不能因为 Provider 已创建就认为接口已经受到保护。

### 6.4 签发与验证过程

1. **签发将可信身份和固定验证参数写入凭证。** `Issue` 先检查 Context 以及身份类别、命名空间、主体标识；主体标识必须为非空白的有效 UTF-8 文本，保留原文，不自动修剪。随后产生 UTC 秒级的签发与到期时间，凭证包含 `kind`、`namespace`、`sub`、`iss`、`aud`、`iat`、`nbf`、`exp`。`sub` 对应 `SubjectID`，`nbf` 与签发时间一致，`exp` 按 `token_ttl` 计算；签名算法为 HS256，头部 `typ` 为 `JWT`，返回方案为 `Bearer`。

2. **认证先验证凭证，再返回身份。** `Authenticate` 接受大小写不敏感的 `Bearer` 方案，但不修剪方案或凭证正文。它检查固定算法、签名、`typ`、签发方、接收方、必填时间字段、有效期和完整身份；要求 `iat <= nbf < exp`，不接纳未来签发或尚未生效的凭证，不提供过期宽限。验证以已签名的 `exp` 为准，不用当前 `token_ttl` 重新计算已有凭证的期限。身份组合必须在构造时传入的允许集合中，成功后才返回 `Principal`。

3. **过期错误只用于其他验证均已通过的凭证。** 签名错误、签发方或接收方不符、身份组合无效等情况使用 `ErrInvalidCredential`，即使同时过期也不降为单纯过期。单纯过期使用 `ErrExpiredCredential`；凭证缺失、残缺、方案不支持分别遵守第 3、5 节约定。Context 取消保持其原始错误，任何失败均返回零值结果，不输出部分身份或凭证。

4. **Provider 可由多个调用并发复用。** 每次操作单独创建声明和结果，构造后不更改密钥、配置或允许集合。签发和认证检查调用方 Context 的取消；本地密码运算不是可抢占的任务，不通过另起后台任务来模拟取消。

### 6.5 调用方式与能力边界

1. **调用方通过公共接口使用同一个 Provider。** 以下片段中，`authnjwt` 是 `go-service-template/internal/authn/jwt` 的导入别名，`principal` 已由可信流程确定；必须先处理错误再使用结果，不记录凭证正文：

   ```go
   provider := authnjwt.Current()
   issued, err := provider.Issue(ctx, principal)
   if err != nil {
       return err
   }

   verified, err := provider.Authenticate(ctx, issued.Credential)
   if err != nil {
       return err
   }
   // verified 是已通过本组件验证的身份，后续业务仍需执行自己的授权规则。
   ```

2. **凭证有效不代表主体的实时业务状态或操作权限已通过。** 本组件不检查用户是否停用、服务是否被移出管理名单或某个对象是否属于调用方；这些判断不能由验签结果代替。它也不提供凭证刷新、丢失恢复或单个凭证撤销机制，不能将重复注册或自行提交一个主体 ID 当作重新签发凭证的依据。

## 7. HTTP 认证适配层

### 7.1 文件职责与公开方法

1. **HTTP 认证放在 `app/http/middleware`，公共认证包不依赖 Gin。** [authentication.go](../../app/http/middleware/authentication.go) 负责 HTTP 凭证提取、调用认证器、处理失败和放行；[principal.go](../../app/http/middleware/principal.go) 负责保存和读取本次请求的身份。该适配层依赖 `authn` 接口及统一 `response`，不导入具体 JWT 包或项目 `identity`，也不判断某个业务接口允许哪种身份。

2. **中间件构造器只接收已经装配好的认证器。** 公开方法如下；构造时传入 `authn.Authenticator`，每次请求使用同一个可并发调用的实例。未提供认证器或接口值中包含 nil 指针等空依赖时，构造器直接 panic，作为装配错误处理。更换认证实现时，不需要修改 HTTP 凭证读取和身份保存方式。

   ```go
   func AuthenticateIfPresent(authenticator authn.Authenticator) gin.HandlerFunc

   func PrincipalFromContext(ctx *gin.Context) (authn.Principal, bool)
   ```

3. **HTTP 入口统一执行可选认证，Controller 决定接口是否要求身份。** [routers/web.go](../../routers/web.go) 的 `New(authenticator)` 接收装配好的认证器，在 `/api` 分组统一挂载一次 `AuthenticateIfPresent`，各版本及领域的已注册路由继承该步骤，不逐接口挂载强制认证中间件。完全未提供 Authorization 头时匿名进入后续流程；携带凭证时必须认证成功。Validator 完成请求绑定及字段校验，Controller 随后检查本接口是否要求身份、允许哪些身份以及哪些数据范围，具体规则由领域专属规范维护。未匹配路由沿用 `NoRoute` 的 404，不补做认证。

### 7.2 Authorization 头如何映射为凭证

1. **仅在完全没有 `Authorization` 头时按匿名请求处理。** 不读取 Query、请求体或 Cookie，也不在其他来源中回退寻找 Token。头已经出现但没有值、值为空或仅含空格和制表符时，属于错误输入，返回 400，不降级为匿名。重复的 Authorization 头直接拒绝，不选第一条或最后一条，逗号拼接的多份凭证也不能当作一份输入。头名称忽略大小写，大小写不同的重复头也一并计数。

2. **方案与 Token 之间使用 ASCII 空格，Token 正文保持原样。** HTTP 字段值外层允许的空格、制表符可以剥离；方案和正文之间至少需要一个 ASCII 空格，方案名按 HTTP `token` 字符集检查，`Bearer` 忽略大小写。正文按 HTTP `b64token` 字符集检查：至少包含一个英文字母、数字或 `-._~+/` 字符，只允许在末尾附加 `=`。内部空白、逗号、非 ASCII 字符及位于正文中间的 `=` 均拒绝；不执行 URL 解码，不修复或重新拼接 Token，也不把 `Bearer` 前缀送入 `Credential.Value`。

3. **HTTP 格式错误与凭证验证失败分别处理。** 缺少分隔、空 Token、错误分隔符、重复头或逗号多凭证属于请求格式错误，在调用认证器前返回 400。结构完整但方案不是 Bearer 时返回方案不支持的 401；结构合法的 Bearer 凭证才交给认证器判断签名、有效期及身份。仅凭格式正确不能认定凭证有效。

### 7.3 一次请求的执行顺序

1. **先清除旧身份，再区分匿名请求和携带凭证的请求。** 中间件先清空本存储键中的已有值，避免沿用前序处理器留下的身份。完全没有 Authorization 头时，不调用认证器，不保存 Principal，直接继续处理；存在该头时，先检查格式，再构造 `authn.Credential` 并执行 `authenticator.Authenticate(ctx.Request.Context(), credential)`。使用当前 Request 的标准 Context，不把 Gin Context 传入公共认证层，也不在这里创建新的等待超时。

2. **认证成功后检查结果完整性，再保存身份并继续处理。** 中间件确认 `Principal.Kind` 为 `user` 或 `service`，且命名空间、主体标识非空白；结果不满足公共契约时按认证器实现错误返回 500，不保存身份或继续处理。结果完整时，通过私有 `setPrincipal` 按值保存，再调用 `ctx.Next()`。这项检查不重复验签或判断业务命名空间；Token 原文不存入 Principal，不供后续 Controller 重新解析身份。

3. **已提供凭证却认证失败时，立即终止，不降级为匿名。** 中间件按照第 7.4 节选择响应和挑战头，通过 `response.Fail` 输出后立即 `return`，不进入 Validator 或 Controller。这同样适用于允许匿名调用的公开接口。`Fail` 中的 `Abort` 阻止后续 Gin 处理器运行，当前方法仍必须返回；不能输出失败后继续保存身份或调用后续处理。

4. **匿名请求的参数校验先于 Controller 的身份要求检查。** 完整顺序为“可选认证 → Validator → Controller → Service”。匿名请求若绑定失败或字段非法，先返回 400；参数通过后，受保护 Controller 再返回缺少身份的 401。这是明确的处理顺序，不将身份检查复制进 Validator，也不额外增加路由级强制认证入口。

### 7.4 错误响应与认证挑战

1. **HTTP 格式、身份认证和技术故障使用不同响应类别。** 本节中间件产生的失败均使用统一 `data/error` 结构，不附 `business_code` 或 `details`；消息使用对应错误的 `Error()`，适配层不将 Authorization 头、Token 或签名密钥拼入消息。具体映射如下：

   | 情况 | HTTP 状态与通用码 | `WWW-Authenticate` |
   | --- | --- | --- |
   | 完全没有 Authorization 头 | 不输出错误，匿名继续后续处理 | 不设置认证挑战 |
   | 头已提供，但认证器返回 `ErrMissingCredential` | 401、`UNAUTHENTICATED` | `Bearer realm="api"` |
   | 完整凭证的方案不支持，或认证器返回 `ErrUnsupportedScheme` | 401、`UNAUTHENTICATED` | `Bearer realm="api"` |
   | Authorization 显式为空、重复或结构不合法 | 400、`INVALID_ARGUMENT` | `Bearer realm="api", error="invalid_request"` |
   | 认证器返回 `ErrInvalidCredential` 或 `ErrExpiredCredential` | 401、`UNAUTHENTICATED` | `Bearer realm="api", error="invalid_token"` |
   | 认证器成功返回了不完整身份，或返回 `ErrInvalidPrincipal` | 500、`INTERNAL_ERROR` | 不设置认证挑战 |
   | 未知技术错误或 Context 取消、截止时间错误 | 500、`INTERNAL_ERROR` | 不设置认证挑战 |

2. **技术故障由中间件记录并原样说明，不能伪装成无效凭证。** 500 错误记录英文日志，响应保留原始 `err.Error()`；日志不额外记录凭证或完整请求头，错误产生处也不能把凭证写入错误消息。成功却返回不完整身份时使用固定消息 `authenticator returned an invalid principal`。已确认的缺失、不支持、无效或过期等认证失败，使用公共认证错误识别，不根据英文消息猜测类别；Context 错误优先按 500 处理。

3. **认证挑战只说明凭证要求，不承诺业务权限。** `WWW-Authenticate` 的 Bearer 表示该入口接受的凭证方案；`invalid_request` 表示请求格式问题，`invalid_token` 表示凭证无效或过期。即使认证通过，也仍需执行接口自己的授权与业务约束。

### 7.5 Principal 的保存、读取与缺失处理

1. **保存入口保持私有，认证结果只属于当前请求。** `setPrincipal` 由认证中间件在成功后调用，内部存储键不作为业务接口开放；不提供从 Request DTO、Query 或请求自报 ID 设置可信身份的方法。不同请求各自使用自己的 Gin Context，不把 Principal 写入共享 Controller、Service 或全局变量。

2. **读取方法返回值和存在标记，不产生响应副作用。** `PrincipalFromContext(ctx)` 返回 `authn.Principal` 的值及 `bool`，调用方先检查标记，再使用身份；取得副本后修改字段不会改变 Context 中保存的身份。身份未保存、已被中间件清空或存储类型不正确时，返回零值和 `false`；读取方法不写 HTTP 响应、不调用 `Abort`，也不临时解析 Token 补建身份。

3. **正常处理链中没有 Principal 表示匿名，由 Controller 决定是否允许。** 公开 Controller 可以继续执行；要求身份的 Controller 返回 HTTP 401、`UNAUTHENTICATED`，并设置 `WWW-Authenticate: Bearer realm="api"`，不把正常匿名情况视为 500 装配错误。已有身份但不满足接口的类别、命名空间或对象访问规则时，按领域规范返回 403；不能改用请求中的发起方 ID 继续执行。

4. **Controller 使用共用身份检查，再构造业务参数。** 可复用的身份要求和主体标识转换由第 8 节的 `app/http/authz` 提供；Controller 调用后检查错误，继续执行本接口的目标访问规则，并将可信发起方标识填入 Service Params。请求 DTO 只保留调用方能够指定的业务输入，Service 不接收 Gin Context、Principal 或 Token。主体标识转换已经由共用方法明确检查，不能套用“请求字段已由 Validator 校验”的省略错误规则。

## 8. Controller 共用的身份检查

### 8.1 声明位置与方法职责

1. **`app/http/authz` 是由项目业务维护、供各 Controller 复用的身份检查包。** `authz` 表示 authorization，即授权；项目可以根据自己的身份体系新增、调整或移除其中的业务封装，具体扩展方式见第 8.4 节。这里提供身份要求检查和项目主体标识转换，读取中间件保存的 Principal，不重新认证、不查询数据库，也不代替接口自己的数据访问规则。现有公开方法为：

   ```go
   func RequirePrincipal(ctx *gin.Context, kind authn.PrincipalKind, namespace string) (authn.Principal, error)

   func RequireMemberUserID(ctx *gin.Context) (int64, error)
   ```

2. **`principal.go` 负责“必须有身份，且属于指定身份范围”。** `RequirePrincipal` 调用 `middleware.PrincipalFromContext` 读取结果；没有身份返回 `ErrAuthenticationRequired`，`Kind` 或 `Namespace` 不匹配返回 `ErrPrincipalNotAllowed`，成功则返回 Principal 的值。允许的类别和命名空间由 Controller 传入，这个方法不维护各接口的权限清单。

3. **`member.go` 负责本项目成员身份到内部用户 ID 的转换。** `RequireMemberUserID` 先调用 `RequirePrincipal`，要求 `authn.PrincipalKindUser` 与 `identity.UserNamespaceMember`；通过后按十进制解析 `SubjectID` 为 int64，并要求大于 0。解析失败、溢出或非正数返回 `ErrInvalidMemberIdentity`，成功返回内部用户 ID。该方法属于项目 HTTP 适配层，因为“member 使用正 int64 用户 ID”是项目约定；通用 `internal/authn` 仍允许其他身份体系使用字符串主体标识。

### 8.2 Controller 与共用方法的边界

1. **共用方法只返回值或错误，不直接处理响应。** 方法不调用 `response.Fail`、`Abort` 或 `Next`，不设置状态码、业务码或挑战头。Controller 必须先检查返回错误，选择响应后立即返回；不能忽略错误并继续使用零值身份或用户 ID。`RequireMemberUserID` 收到 `RequirePrincipal` 的错误时直接返回，不包装成另一种错误。

2. **具体接口的目标访问规则仍由 Controller 明确执行。** 共用方法确认“当前调用方是谁、是否属于要求的身份体系”，不接收目标对象 ID，也不判断对象是否属于调用方。Controller 在取得可信标识后，直接执行本接口要求的对象关系检查，再构造 Service Params；需要由业务状态决定的规则继续交给 Service。各接口允许的具体身份和数据范围只在所属领域规范中定义。

3. **包依赖从 Controller 指向 HTTP 适配，不反向进入业务层。** 依赖方向为 `Controller → app/http/authz → app/http/middleware、app/identity、internal/authn`；中间件、身份枚举和公共认证包不导入 `authz`。Service、models 不调用这些 HTTP 方法，而是接收入口已经构造好的业务参数，继续独立保障用例约束。

### 8.3 错误声明与响应映射

1. **共用检查声明自己的错误值，Controller 继续决定对外表现。** `ErrAuthenticationRequired`、`ErrPrincipalNotAllowed` 在 `authz/principal.go` 中以包级 `var` 和 `errors.New` 声明，`ErrInvalidMemberIdentity` 在 `authz/member.go` 中同样声明；不新增错误类型，不在各 Controller 重复声明同义错误。Controller 使用 `errors.Is` 识别它们，不比较文案。

   | 共用错误 | 表达的失败 | Controller 选择的响应 |
   | --- | --- | --- |
   | `ErrAuthenticationRequired` | 当前请求没有可信身份 | 401、`UNAUTHENTICATED`，挑战头为 `Bearer realm="api"` |
   | `ErrPrincipalNotAllowed` | 已有身份，但不属于要求的类别或命名空间 | 403、`PERMISSION_DENIED`，具体业务码由 Controller 选择 |
   | `ErrInvalidMemberIdentity` | 成员身份的主体标识不符合项目用户 ID 约定 | 401、`UNAUTHENTICATED`，挑战头附加 `error="invalid_token"` |

2. **共用身份错误与接口自己的访问拒绝分别声明。** 目标对象不属于调用方等接口规则，不在 `authz` 中增加目标专属错误，而由 Controller 声明或沿用自己的访问拒绝错误。不同来源的错误可以映射为同一通用码和业务码；消息仍直接使用收到的 `err.Error()`，不因提取共用方法而增加包装层。

### 8.4 按项目业务定制与扩展

1. **项目身份封装可以按实际业务新增、修改或移除。** `member.go` 表达本项目“普通用户使用正 int64 内部 ID”的约定，不是所有项目必须保留的身份类型或转换方式。业务没有 member 身份体系时，可以移除相应封装并调整调用方；使用字符串或 UUID 标识时，应按该身份体系的契约提供对应方法，不必先转换为 int64。各方法仍遵守第 8.2 节的职责边界。

2. **文件按身份体系组织，方法名称表达要求的身份与返回结果。** 例如，员工身份可以放在 `staff.go` 中，由 `RequireStaffID` 检查员工命名空间并返回约定类型的标识；管理服务身份可以放在 `admin_service.go` 中，由 `RequireAdminServicePrincipal` 返回符合要求的 Principal。这些名称仅演示扩展方式，不表示已提供相应实现。封装优先复用 `RequirePrincipal`，命名空间引用 `app/identity` 中的常量。没有专属转换或共用检查需要时，Controller 直接调用 `RequirePrincipal` 即可，不要求每个命名空间都有封装文件；声明新方法也不需要增加一个新包或注册额外中间件。

3. **新增身份体系时，先同步身份声明和可信认证来源。** 按第 2.4 节在 `app/identity` 声明命名空间，明确所属 Kind、SubjectID 含义及可信来源；再同步签发或身份映射逻辑，以及启动装配中认证器允许的身份组合，最后提供对应的 `authz` 方法。只有新增主体大类时才按第 2.3 节扩展 `PrincipalKind` 及受影响的认证、HTTP 检查逻辑。已有身份体系仅增加一个共用检查方法时，无须重复增加命名空间或放宽认证范围。

4. **新增方法必须明确输入前提、返回结果和失败含义。** 注释说明所要求的身份范围、主体标识的格式和转换规则、返回类型及可能的错误；方法只使用已经认证的 Principal，不从请求字段补建身份，也不通过改写 Kind 或 Namespace 将另一身份冒充为所需身份。跨来源的可信身份映射遵守第 2.2 节，由认证或签发链路确认。可复用既有错误时直接返回；新的独立失败按第 8.3 节在所属文件中声明，并同步 Controller 的错误映射，避免遗漏后落入未知错误分支。

5. **接口通过显式调用选择身份要求，新增方法不会自动授予权限。** Controller 根据接口契约决定是否调用共用方法，处理其错误后，再检查目标访问关系并构造 Service Params。允许匿名的接口不因新增某种身份封装就改为必须登录；需要读取数据库才能判断的业务条件仍交给 Service。仅增加 `authz` 方法，既不能让认证器接受原本不支持的凭证，也不能让某种身份自动访问所有接口。

6. **修改或移除既有封装时，同步核对调用方和既有凭证。** 仅调整内部实现且保持契约时，可以继续复用现有调用方式；若修改命名空间、主体标识含义或返回类型，应同时检查签发与认证逻辑、Controller、Service Params 和相关文档。已签发凭证仍携带原来的身份声明，需要明确其继续兼容还是被拒绝，不能只修改转换方法就假定旧凭证已经采用新语义。接口允许的身份和数据范围变化，记录在所属领域的权限规范中。
