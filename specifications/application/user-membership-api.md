# 用户会员 V1 查询接口

本页记录 `UserMembershipController` 已实现的两个本人查询接口。路由、响应和校验分层分别遵守[路由规范](api-routing.md)、[响应规范](api-response.md)和[校验规范](api-service-validator.md)。开通及续费继续遵守[会员订单履约规则](../databases/mysql/schema-design.md#435-创建会员订单与支付回调)，不由这两个接口写入。

## 1. 公共约定

- 路由前缀：`/api/v1/user/userMembership`。
- 身份：已认证的 `user/member`，且 `user_id` 必须等于可信身份中的用户 ID。服务身份及其他用户均无权访问。
- `member` 是普通用户身份命名空间，不表示已付费。未开通、已过期、停用的用户均可查询本人数据。
- GET 参数只从 Query 绑定；ID 为正 int64 的十进制字符串，响应 ID 也使用字符串。
- 时间使用 UTC RFC3339 表示，精度为毫秒；整秒值允许省略小数部分。
- 成功返回 HTTP 200，顶层固定为 `data` 和 `error: null`；空集合为 `[]`，可空字段保留 `null`。

## 2. 查询当前会员权益

```http
GET /api/v1/user/userMembership/queryUserMembershipByUserId?user_id=123
Authorization: Bearer <access_token>
```

`user_id` 必填。成功示例：

```json
{
  "data": {
    "user_id": "123",
    "membership_status": "ACTIVE",
    "queried_at": "2026-11-12T02:00:00Z",
    "membership": {
      "membership_id": "456",
      "membership_level": 10,
      "effective_at": "2026-11-02T02:00:00Z",
      "expired_at": "2026-12-02T02:00:00Z"
    }
  },
  "error": null
}
```

| membership_status | 含义 | membership |
| --- | --- | --- |
| `NOT_OPENED` | 用户存在但没有会员记录 | `null` |
| `ACTIVE` | `effective_at <= queried_at < expired_at` | 当前权益对象 |
| `EXPIRED` | `queried_at >= expired_at` | 保留原等级及期限的权益对象 |

`queried_at` 在读取权益后取一次 UTC 毫秒时间，并用于本次状态判断。状态是派生结果，不写回数据库。未来生效、未知等级或不合法期限属于存量数据异常，返回 500；用户本身不存在返回 404。账户停用不改变会员期限，也不使这里返回的会员状态等同于其他业务操作的授权结果。

## 3. 分页查询权益变更历史

```http
GET /api/v1/user/userMembership/queryMembershipChangeRecordPage?user_id=123&page_size=20
Authorization: Bearer <access_token>
```

| Query 参数 | 约定 |
| --- | --- |
| `user_id` | 必填，本人用户 ID |
| `page_size` | 省略时为 20，范围 1～100；显式 0 或空值不采用默认值 |
| `page_token` | 省略或空字符串表示第一页；后续原样提交上一页的 `next_page_token` |

响应 `data` 包含 `membership_change_record_list`、`page_size`、`has_more`、`next_page_token`。没有历史时：

```json
{
  "data": {
    "membership_change_record_list": [],
    "page_size": 20,
    "has_more": false,
    "next_page_token": null
  },
  "error": null
}
```

每条历史包含以下字段：

| 字段 | 类型及语义 |
| --- | --- |
| `record_id`、`membership_id`、`order_id` | 正 int64 的十进制字符串 |
| `change_type` | `10` 首次开通，`20` 续费；过期后购买仍为续费 |
| `before_membership_level` | 整数或 `null` |
| `before_effective_at`、`before_expired_at` | UTC 时间或 `null` |
| `after_membership_level` | 整数，当前为 `10` 标准会员 |
| `after_effective_at`、`after_expired_at` | 本次履约后的 UTC 期限快照 |
| `created_at` | 本条历史创建的 UTC 时间 |

首次开通的三个 `before_*` 字段全部为 `null`。历史按 `created_at DESC, id DESC` 排序，游标来自实际返回的末条记录；服务端额外读取一条以判断 `has_more`。没有下一页时 `next_page_token` 为 `null`，不返回总数，也不承诺跨页固定快照。

令牌具有版本和用户绑定，但不是身份凭证；每次翻页仍需认证和本人权限校验。客户端将其作为不透明值，不自行解析或构造。非法格式、版本、边界或不匹配的用户返回参数错误。改变 `page_size` 不改变令牌表示的位置。

只返回订单履约产生的历史，不包含待支付订单，也不为自然到期生成历史。`after_*` 是历史快照，不保证等于当前权益。用户查询不跨库关联订单信息。

## 4. 失败响应

| HTTP | code | business_code / 场景 |
| --- | --- | --- |
| 400 | `INVALID_ARGUMENT` | 非法 ID、分页数量或令牌；Validator 提供对应字段明细 |
| 401 | `UNAUTHENTICATED` | 缺少或无效身份；返回必要的 Bearer 认证挑战 |
| 403 | `PERMISSION_DENIED` | `USER_MEMBERSHIP_PERMISSION_DENIED`，身份范围不允许或查询他人 |
| 404 | `NOT_FOUND` | `USER_PROFILE_NOT_FOUND`，目标用户不存在 |
| 500 | `INTERNAL_ERROR` | 存量数据异常、查询故障或其他内部失败 |

公共认证中间件、响应等待层仍使用各自的错误契约。未开通、会员过期和历史为空均为正常成功结果。
