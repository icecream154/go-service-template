# 用户资料接口

本文描述 `UserProfileController` 的 V1 接口契约。路由命名遵守[接口路由与命名规范](api-routing.md)，成功与失败的公共外层遵守[接口返回规范](api-response.md)。

## 1. 接口与公共约定

统一路径前缀为 `/api/v1/user/userProfile`：

| HTTP 方法 | 业务用例 | 请求 DTO | 响应 DTO |
| --- | --- | --- | --- |
| GET | `queryUserProfileByUserId` | `QueryUserProfileByUserIDRequest` | `QueryUserProfileByUserIDResponse` |
| POST | `createUser` | `CreateUserRequest` | `CreateUserResponse` |
| POST | `updateUserProfile` | `UpdateUserProfileRequest` | `UpdateUserProfileResponse` |
| POST | `batchQueryUserProfiles` | `BatchQueryUserProfilesRequest` | `BatchQueryUserProfilesResponse` |
| GET | `queryUserProfilePage` | `QueryUserProfilePageRequest` | `QueryUserProfilePageResponse` |

GET 参数放在 Query，POST 使用 `application/json` 请求体，所有成功响应使用 HTTP 200。`createUser` 使用第 3 节所述的 Gin 默认绑定；更新和批量查询仍使用严格 JSON 解析，请求体最多 1 MiB，必须是单个对象，不接受未知字段或多个连续的 JSON 值。

接口中的用户 ID 为正 int64，范围为 `1`～`9223372036854775807`。JSON 中使用十进制字符串，批量 ID 数组的每个元素也使用字符串；Query 中使用对应十进制文本。版本号、页码、每页数量和总数使用 JSON 数字。

创建按首次自注册处理：注册身份由 `account_source` 与 `external_account_id` 共同表达，此时尚无内部用户 ID，因此创建请求不提供 `operator_user_id`。当前未接入认证，注册身份由请求声明，尚未核验外部身份凭证或可信来源。更新请求中的 `operator_user_id` 同样只是调用方提供的身份声明，不代表已经完成认证；当前业务规则只允许其与目标 `user_id` 相同。

单个用户资料的响应字段如下：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `user_id` | string | 内部用户 ID |
| `user_name` | string | 展示昵称，允许重名 |
| `avatar_url` | string 或 null | 头像地址；`null` 与空字符串分别保留 |
| `is_enabled` | boolean | 当前启用状态 |
| `version` | number | 当前资料版本，初始为 0，成功更新时递增 |

上述字段始终存在。账户来源、外部账户标识和持久化时间不包含在普通资料响应中。批量及分页查询复用同一资料结构，空集合输出 `[]`。

## 2. 按用户 ID 查询资料

```http
GET /api/v1/user/userProfile/queryUserProfileByUserId?user_id=1234567890123456789
```

| Query 参数 | 必填 | 说明 |
| --- | --- | --- |
| `user_id` | 是 | 待查询的内部用户 ID |

停用用户仍可查询。成功响应：

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

用户资料不存在时返回 HTTP 404、`NOT_FOUND` 和 `USER_PROFILE_NOT_FOUND`。

## 3. 首次创建用户

```http
POST /api/v1/user/userProfile/createUser
Content-Type: application/json
```

本接口先进入 `UserProfileValidator.CreateUser`，由通用 `Bind` 调用 Gin 的 `ShouldBindJSON` 填充 `CreateUserRequest`，再通过同文件内的私有函数 `validateCreateUserRequest` 校验字段，全部通过后才将 DTO 交给 Controller。绑定错误返回 HTTP 400 和 `INVALID_ARGUMENT`；字段校验失败时按 `account_source`、`external_account_id`、`user_name`、`avatar_url` 的顺序汇总到 `details`，每个字段最多一条原因，不附带业务错误码。现有 Service 保留入参防御校验，并继续判断账户唯一性等业务规则。

本次仍采用 Gin 默认 JSON 解码：未知字段会被忽略，字段名允许大小写匹配，只读取第一个 JSON 值，非法 UTF-8 使用解码器的替换行为；绑定器不额外施加 1 MiB 限制或检查尾部内容。字段校验针对绑定后的 DTO，不改变原始解码行为。缺失或显式 `null` 的必填文本字段会保持零值，由请求 Validator 按不能为空白处理。

| 请求字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `account_source` | string | 是 | 外部账户来源命名空间，合法 UTF-8、非空白，最多 32 个 Unicode 码点 |
| `external_account_id` | string | 是 | 来源内的外部账户标识，合法 UTF-8、非空白，最多 128 个 Unicode 码点 |
| `user_name` | string | 是 | 展示昵称，合法 UTF-8、非空白，最多 64 个 Unicode 码点 |
| `avatar_url` | string 或 null | 否 | 最多 512 个 Unicode 码点；非空字符串必须是绝对 HTTP/HTTPS URL |

Service 对绑定后的账户身份不去空格、不转换大小写，也不把外部标识转换为数字。缺失头像或显式传 `null` 均表示尚未提供头像；传空字符串则保留空字符串。

请求示例：

```json
{
  "account_source": "example_identity",
  "external_account_id": "external-user-001",
  "user_name": "Example User",
  "avatar_url": null
}
```

服务端通过雪花生成器分配内部用户 ID，初始 `is_enabled` 为 `true`，`version` 为 `0`。请求不能自行指定内部 ID、启用状态或版本。成功响应：

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

同一 `account_source` 与 `external_account_id` 组合只能创建一个用户。数据库唯一约束保证并发创建时不产生重复账户；重复创建返回 HTTP 409、`CONFLICT` 和 `USER_ACCOUNT_ALREADY_EXISTS`，不将已有用户作为本次创建成功的结果。

## 4. 更新用户资料

```http
POST /api/v1/user/userProfile/updateUserProfile
Content-Type: application/json
```

| 请求字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `operator_user_id` | string | 是 | 发起方内部用户 ID，当前必须与 `user_id` 相同 |
| `user_id` | string | 是 | 目标内部用户 ID |
| `expected_version` | number | 是 | 调用方读取到的资料版本，整数，范围为 `0`～`9223372036854775806` |
| `user_name` | string | 是 | 新昵称，校验规则与创建一致 |
| `avatar_url` | string 或 null | 是 | 新头像，校验规则与创建一致；必须显式提供该字段 |

这是对昵称、头像的完整替换。头像传 `null` 表示清空为“尚未提供”，传空字符串则保存空字符串；遗漏字段不代表保留旧值。账户来源、外部账户标识和启用状态不在更新范围内。

请求示例：

```json
{
  "operator_user_id": "1234567890123456789",
  "user_id": "1234567890123456789",
  "expected_version": 0,
  "user_name": "Updated User",
  "avatar_url": "https://example.com/avatar.png"
}
```

停用用户不能更新资料。更新时检查资料版本，成功后将版本递增一次，返回本次更新后的资料：

```json
{
  "data": {
    "user_id": "1234567890123456789",
    "user_name": "Updated User",
    "avatar_url": "https://example.com/avatar.png",
    "is_enabled": true,
    "version": 1
  },
  "error": null
}
```

版本过期返回 HTTP 409、`CONFLICT` 和 `USER_PROFILE_VERSION_CONFLICT`，不自动重试覆盖新值。调用方需要重新查询后决定是否再次提交。同一旧版本请求再次提交可能返回版本冲突，接口不承诺重试返回相同响应。

## 5. 批量查询资料

```http
POST /api/v1/user/userProfile/batchQueryUserProfiles
Content-Type: application/json
```

| 请求字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `user_id_list` | string[] | 是 | 1～100 个内部用户 ID，每个元素必须是正 int64 的十进制字符串 |

请求示例：

```json
{
  "user_id_list": [
    "1234567890123456789",
    "1234567890123456790",
    "1234567890123456789"
  ]
}
```

重复 ID 去重，并保留首次出现的位置。已存在的资料按请求中的 ID 顺序返回，停用用户也包含在结果中；未找到的 ID 单独放入 `missing_user_id_list`。输入数量限制按去重前的数组长度计算。

部分或全部 ID 不存在仍返回 HTTP 200，不将整批查询视为失败。例如第二个 ID 不存在：

```json
{
  "data": {
    "user_profile_list": [
      {
        "user_id": "1234567890123456789",
        "user_name": "Example User",
        "avatar_url": null,
        "is_enabled": true,
        "version": 0
      }
    ],
    "missing_user_id_list": ["1234567890123456790"]
  },
  "error": null
}
```

没有已存在资料时，`user_profile_list` 为 `[]`；没有缺失 ID 时，`missing_user_id_list` 为 `[]`。

## 6. 分页查询资料

```http
GET /api/v1/user/userProfile/queryUserProfilePage?page_number=1&page_size=20&is_enabled=true
```

| Query 参数 | 必填 | 说明 |
| --- | --- | --- |
| `page_number` | 否 | 从 1 开始，默认 1；与每页数量组合后不得产生分页偏移溢出 |
| `page_size` | 否 | 1～100，默认 20 |
| `user_name` | 否 | 非空时按完整昵称精确匹配，最多 64 个 Unicode 码点；省略或空字符串时不过滤昵称 |
| `is_enabled` | 否 | 仅接受 `true`、`false`，不提供则不过滤启用状态 |

按内部用户 ID 降序返回。昵称筛选是普通字段匹配，不提供关键词或全文检索。成功响应：

```json
{
  "data": {
    "user_profile_list": [
      {
        "user_id": "1234567890123456789",
        "user_name": "Example User",
        "avatar_url": null,
        "is_enabled": true,
        "version": 0
      }
    ],
    "page_number": 1,
    "page_size": 20,
    "total_count": 1
  },
  "error": null
}
```

空页返回 `user_profile_list: []`，仍返回页码、每页数量和匹配总数。总数与页面数据分次读取，不承诺并发写入期间来自同一个一致性快照。

## 7. 错误约定

| 失败场景 | HTTP 状态 | `code` | `business_code` |
| --- | --- | --- | --- |
| 参数缺失、格式错误、字段约束不满足或请求体不合法 | 400 | `INVALID_ARGUMENT` | 省略 |
| 更新发起方与目标用户不同 | 403 | `PERMISSION_DENIED` | `USER_PROFILE_PERMISSION_DENIED` |
| 单个目标用户资料不存在 | 404 | `NOT_FOUND` | `USER_PROFILE_NOT_FOUND` |
| 创建时账户身份已存在 | 409 | `CONFLICT` | `USER_ACCOUNT_ALREADY_EXISTS` |
| 停用用户更新资料 | 409 | `CONFLICT` | `USER_DISABLED` |
| 更新版本过期 | 409 | `CONFLICT` | `USER_PROFILE_VERSION_CONFLICT` |
| 未预期的数据库、发号器等内部异常 | 500 | `INTERNAL_ERROR` | 省略 |

所有失败响应的 `data` 为 `null`。参数错误可以带 `details`，用于说明字段及约束；内部异常固定返回 `internal server error`，原始错误仅记录在服务端。

例如版本冲突：

```json
{
  "data": null,
  "error": {
    "code": "CONFLICT",
    "business_code": "USER_PROFILE_VERSION_CONFLICT",
    "message": "user profile version conflict"
  }
}
```
