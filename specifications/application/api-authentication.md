# 公共身份认证契约

## 1. 适用范围与文件职责

1. **`internal/authn` 声明公共认证契约，`app/identity` 声明项目采用的命名空间。** `authn` 表示 authentication，即身份认证。公共契约回答“使用什么凭证认证、认证成功得到什么身份、认证失败如何返回”，不绑定某个业务用户模型或具体凭证协议；项目层集中定义身份体系的名称及含义，具体声明与引用方式见第 2.4 节。

   | 文件 | 声明内容 | 职责 |
   | --- | --- | --- |
   | [principal.go](../../internal/authn/principal.go) | `PrincipalKind`、身份类别常量、`Principal` | 统一表达认证成功后的调用方身份 |
   | [authenticator.go](../../internal/authn/authenticator.go) | `Credential`、`Authenticator` | 统一认证输入和调用方法 |
   | [error.go](../../internal/authn/error.go) | 通用认证错误值 | 为明确的凭证失败提供可识别的错误 |
   | [user_namespace.go](../../app/identity/user_namespace.go) | 项目用户命名空间常量 | 维护用户身份体系的名称、语义与扩展约定 |
   | [service_namespace.go](../../app/identity/service_namespace.go) | 服务命名空间的声明位置与扩展注释 | 按实际接入需要维护服务身份体系，当前不声明常量 |

2. **公共包只提供契约，不自动执行认证。** 创建 `Credential`、填写 `Principal` 或声明一个 `Authenticator` 变量，都不会校验凭证、建立可信身份或访问外部依赖。具体认证实现必须履行本文约定；公共结构体没有构造函数或自动校验方法。

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

1. **完整身份由 `Kind`、`Namespace`、`SubjectID` 共同确定。** 比较身份时必须同时考虑三个字段，不能只比较 `SubjectID`。例如下列结果表示不同的主体；表格用于说明身份语义，项目已声明的命名空间只有 `member`，其他名称不是已登记常量：

   | Kind | Namespace | SubjectID | 含义 |
   | --- | --- | --- | --- |
   | `user` | `member` | `123` | 普通用户体系中的用户 123 |
   | `user` | `staff` | `123` | 员工体系中的员工 123 |
   | `user` | `externalAccounts` | `account-abc` | 已验证的外部账户，不要求已有内部用户 ID |
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
   | `app/identity/service_namespace.go` | `ServiceNamespace<身份体系>` | `authn.PrincipalKindService` | 暂无常量，只有声明位置及扩展约定的注释 |

2. **常量名称表达主体类别和具体身份体系，注释说明身份范围。** 使用 Go 导出标识符的命名方式，不使用全大写下划线。`UserNamespaceMember` 表示普通用户账户体系，`member` 不表示会员等级或角色；新增常量时说明其可信身份来源、覆盖的账户或服务范围，以及 `SubjectID` 在该体系中的含义。服务文件在实际身份体系明确后再增加对应常量，不因公共包存在 `PrincipalKindService` 就登记一个空泛的服务命名空间。

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

## 4. Authenticator 的调用契约

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

## 5. 通用认证错误

### 5.1 错误的声明与含义

1. **通用认证错误使用包级 `var` 和 `errors.New` 声明。** 这些值初始化后不重新赋值，不为它们增加自定义错误类型。错误消息为固定英文说明，不包含凭证原文、签名密钥或具体主体信息。

   | 错误值 | 英文消息 | 使用条件 |
   | --- | --- | --- |
   | `ErrMissingCredential` | `authentication credential is missing` | `Scheme` 和 `Value` 均为空或仅含空白 |
   | `ErrUnsupportedScheme` | `authentication scheme is not supported` | 完整凭证使用了当前认证实现不支持的方案 |
   | `ErrInvalidCredential` | `authentication credential is invalid` | 凭证不完整、格式或验证结果无效，或已确认被撤销 |
   | `ErrExpiredCredential` | `authentication credential has expired` | 已确认凭证真实性，并确定其有效期已经结束 |

2. **凭证过期必须是经过验证后的判断。** 不能只解码出一个过去的时间，就返回 `ErrExpiredCredential`；未通过真实性验证的输入仍属于无效凭证。无法区分具体凭证失败原因时，可以返回 `ErrInvalidCredential`，但技术故障不属于此范围。

### 5.2 错误的识别与传递

1. **调用方使用 `errors.Is` 识别错误，不比较错误文案。** 四个错误是彼此独立的值，没有父子分类关系。例如：

   ```go
   errors.Is(authn.ErrExpiredCredential, authn.ErrExpiredCredential) // true
   errors.Is(authn.ErrExpiredCredential, authn.ErrInvalidCredential) // false
   ```

   需要统一处理多种凭证失败时，显式判断对应错误值；不能假设判断 `ErrInvalidCredential` 就会覆盖缺失、方案不支持或过期。

2. **认证实现只把能够确认的凭证失败映射为通用认证错误。** 具体凭证验证组件明确报告签名不匹配、已撤销或过期时，可以按照其含义返回对应错误值。无法确认是凭证问题时，不替换成 `ErrInvalidCredential`，也不通过重新创建相同文案的 `errors.New` 值模拟已有错误。

3. **技术故障和 Context 错误原样返回。** 网络、数据库、配置或其他执行故障，以及取消和截止时间错误，保留收到的原错误。不得为了统一认证失败而将其替换成四个凭证错误之一，也不得在失败后返回零值身份和 `nil`。公共包不负责重试、记录日志或决定错误的对外表现。
