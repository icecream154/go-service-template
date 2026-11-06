# user 领域接口权限规范

## 1. 适用范围与职责

1. **本文规定 `UserProfileController` 五个接口的调用资格和数据访问范围。** 创建用户允许匿名调用；单个资料的查询、修改仅限用户本人；批量及分页查询仅限管理服务。本规范独立维护 user 领域规则，不将这些接口规则写入通用认证契约。

2. **通用身份结构与项目命名空间沿用既有声明。** `Principal`、签发器、认证器及通用认证错误见[公共身份认证与 JWT 实现](../../application/api-authentication.md)；用户与服务命名空间分别声明在 [user_namespace.go](../../../app/identity/user_namespace.go) 和 [service_namespace.go](../../../app/identity/service_namespace.go)。本文规定接口使用哪一种身份以及创建后的凭证响应，签名算法和有效期配置沿用公共 JWT 组件。

3. **身份认证与业务授权分别成立。** 有效凭证证明调用方身份；本规范决定该身份能够调用哪个接口、访问哪些资料。公共类型或命名空间常量的声明本身不执行认证，也不会自动赋予全部接口权限。

4. **本次实现只接入创建成功后的凭证签发。** `createUser` 仍允许匿名调用；其余四个接口尚未接入 HTTP 认证及本规范要求的身份授权。下述权限矩阵规定这些接口应当遵守的规则，不能据此认为现有路由已经受到保护。

## 2. 身份与主体标识

1. **普通用户使用 `user/member` 身份。** `Kind` 使用 `authn.PrincipalKindUser`，`Namespace` 使用 `identity.UserNamespaceMember`。`SubjectID` 是本项目内部用户 ID 的十进制字符串，来自服务端创建结果或可信认证结果，不采用请求方自行指定的身份。

2. **管理服务使用 `service/admin` 身份。** `Kind` 使用 `authn.PrincipalKindService`，`Namespace` 使用 `identity.ServiceNamespaceAdmin`，其值为 `"admin"`。`SubjectID` 标识具体的管理服务，由可信配置与凭证验证确认；它不是待查询的用户 ID，也不是客户端填入 `admin` 字符串就能取得的身份。

3. **授权同时检查主体类别和命名空间。** 仅检查 `Namespace == "admin"` 不足以确认管理服务；必须同时确认 `Kind == service` 且凭证对应已认可的管理服务主体。用户身份不能通过修改命名空间变成管理服务，其他服务命名空间也不自动获得本规范的管理服务权限。

## 3. 五个接口的权限矩阵

| 方法 | 完整路径 | 允许的调用方 | 数据范围或成功结果 |
| --- | --- | --- | --- |
| POST | `/api/v1/user/userProfile/createUser` | 无需身份认证，允许匿名调用 | 创建新用户，成功响应同时返回用户资料和该新用户的身份凭证 |
| GET | `/api/v1/user/userProfile/queryUserProfileByUserId` | 已认证的 `user/member` | 仅能查询本人资料，目标用户 ID 必须等于认证主体的内部用户 ID |
| POST | `/api/v1/user/userProfile/updateUserProfile` | 已认证的 `user/member` | 仅能修改本人资料，目标用户 ID 必须等于认证主体的内部用户 ID |
| POST | `/api/v1/user/userProfile/batchQueryUserProfiles` | 已认证的 `service/admin` | 按请求 ID 集合批量查询资料，继续遵守批量数量和查询结果契约 |
| GET | `/api/v1/user/userProfile/queryUserProfilePage` | 已认证的 `service/admin` | 按请求筛选及分页条件查询资料，继续遵守分页输入和结果契约 |

## 4. 创建用户与首次凭证签发

1. **`createUser` 是公开的创建入口，不要求调用方先拥有用户或服务凭证。** 注册前没有内部用户 ID 是正常情况，也不以验证外部账户身份作为本接口的调用前提。输入合法性、账户唯一性等业务条件仍按创建用例执行。

2. **Controller 在创建成功后，为新用户签发身份凭证。** 先调用 `Service.CreateUser`，只有成功取得新建资料后，才调用 `authnjwt.Current().Issue(ctx.Request.Context(), principal)`。`Kind` 固定为 `authn.PrincipalKindUser`，`Namespace` 固定为 `identity.UserNamespaceMember`，`SubjectID` 取新建资料 ID 的十进制字符串。身份类别、命名空间和主体 ID 均由服务端确定，不能由请求参数指定；本接口不能签发管理服务身份。Service 继续只负责创建用户，不接收 HTTP Context，也不签发凭证。

3. **成功响应使用嵌套资料对象，同时交付凭证与失效时间。** HTTP 状态为 200，统一外层为 `data/error`，`error` 为 `null`。`CreateUserResponse` 独立声明以下字段，资料字段位于 `data.user_profile` 内，不再平铺到 `data`。Controller 只在创建、签发均成功后调用一次 `response.Success`，响应设置 `Cache-Control: no-store`。

   | `data` 字段 | Go 类型 | 含义与来源 |
   | --- | --- | --- |
   | `user_profile` | `UserProfile` | 本次创建的资料，复用本 Controller 分组的共享 DTO；`user_id` 仍为十进制字符串 |
   | `access_token` | `string` | 签发结果的 `Credential.Value`，只包含令牌正文，不带 `Bearer` 前缀 |
   | `token_type` | `string` | 签发结果的 `Credential.Scheme`，当前为 `Bearer` |
   | `expires_at` | `time.Time` | 直接使用签发结果的 `ExpiresAt`，JSON 为 UTC RFC3339 字符串；不由 Controller 重新计算有效期 |

4. **创建失败或账户重复时不调用签发器。** 创建失败沿用已有错误映射；重复账户返回 HTTP 409、`CONFLICT` 和 `USER_ACCOUNT_ALREADY_EXISTS`。不能查询出已有用户后为匿名调用方签发其凭证，也不能通过重复匿名注册补发上次未取得的令牌。

5. **创建已完成但签发失败时，返回错误并保留已创建的用户。** Controller 记录包含新建 `user_id` 和原始错误的英文日志，不记录令牌；响应返回 HTTP 500、`INTERNAL_ERROR` 和原始 `err.Error()`，不附业务码。此时不返回缺少凭证的成功结果，也不回滚或删除已经创建的记录；凭证恢复机制不在本次实现范围内。

6. **响应等待超时不取消创建和签发，晚到凭证不再发送。** 本接口等待 10 秒；超时先返回 HTTP 503 和 `SERVICE_UNAVAILABLE`，表示业务结果尚未确认。Controller 传入签发器的是等待中间件已隔离取消的内部 Request Context，已经开始的处理继续执行；晚到的资料和凭证响应均被丢弃。超时、断开连接或写出失败都不证明创建回滚，不能据此无条件重试注册或认定能够取回凭证。

7. **未经认证的外部账户字段只表达调用方声明。** 请求中的 `account_source`、`external_account_id` 不证明调用方拥有对应外部账户，不能作为已经验证的外部身份或账户绑定依据。签发的身份只指向本次创建的本地用户，不能根据这些字段认领已有用户。

## 5. 本人查询与本人修改

1. **两个接口先要求可信的普通用户身份，再确认目标就是本人。** 调用方必须通过认证并满足 `Kind=user`、`Namespace=member`；目标 `user_id` 必须与可信 `SubjectID` 对应的内部用户 ID 一致。其他用户即使提供合法、存在的目标 ID，也不能查询或修改该资料。

2. **操作发起方来自认证结果，不来自请求自报。** Controller 根据可信用户身份构造 Service 的发起方入参；目标用户 ID 仍表达本次操作对象。不能将请求中的 `operator_user_id` 当作身份依据，也不能通过让两个请求字段相等来完成本人判断。Service 根据可信发起方与目标执行本人约束，Validator 继续负责字段合法性。

3. **管理服务不享有这两个接口的额外调用资格。** `service/admin` 不能使用本人查询或本人修改接口绕过身份类别限制；管理服务的读取需求通过批量或分页查询实现。本规范不授予管理服务代替用户修改资料的能力。

4. **权限通过后，原有业务条件继续生效。** 本人查询仍处理目标不存在等结果；本人修改继续检查用户启用状态、期望版本及资料字段规则，并保持既有事务约束。身份正确不代表业务操作一定成功。

## 6. 管理服务批量与分页查询

1. **批量和分页接口只接纳可信的管理服务身份。** 普通 `user/member`、匿名调用方及其他服务身份均不能调用。认证过程必须确认具体服务主体属于 `service/admin`，不能依据请求中声明的类别、命名空间或服务名称放行。

2. **管理服务资格适用于本规范明确列出的两个查询用例。** 授予批量与分页查询资格，不表示管理服务自动拥有其他 user 领域接口或其他领域的权限，也不表示该服务能够代表某个用户执行本人操作。

3. **查询输入和返回语义继续按各自用例执行。** 批量查询保持数量、ID 格式、去重及缺失 ID 的约定；分页查询保持筛选条件、分页边界、结果列表和总数的约定。身份认证成功不免除请求字段校验，也不改变资料查询本身的业务语义。

## 7. 拒绝与错误处理

1. **受保护接口缺少有效身份凭证时，按认证失败处理。** 返回通用类别 `UNAUTHENTICATED`；本规则不用于要求公开的 `createUser` 接口先登录。

2. **身份有效但不满足本规范时，按权限不足处理。** 身份类别或命名空间不符、普通用户访问他人资料等情况返回 `PERMISSION_DENIED`。权限失败后立即结束对应处理，不继续执行资料查询或修改。

3. **技术故障不转换成认证失败或权限不足。** 认证依赖、数据库或签发过程发生执行故障时，按实际错误及[错误声明、传递与响应规范](../../application/api-error.md)处理，不能因为无法完成检查而默认放行。
