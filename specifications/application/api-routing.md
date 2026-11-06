# 接口路由与命名规范

## 1. 适用范围与统一风格

本项目自行设计的所有 HTTP 接口统一采用按业务用例命名的路由，包括业务接口、内部接口、管理接口和系统接口。接口按业务领域、Controller 两级分组，以明确的动作名称表达业务能力。

统一格式：

```text
/{api前缀}/{主版本}/{业务领域}/{Controller分组}/{业务用例}
```

例如：

```http
GET  /api/v1/user/userProfile/queryUserProfileByUserId?user_id=123
GET  /api/v1/user/userProfile/queryUserPage?page_number=1&page_size=20
POST /api/v1/user/userProfile/updateUserProfile
POST /api/v1/order/order/cancelOrder
```

普通增删改查也使用上述格式，不与 `/user/{user_id}`、`POST /user` 等资源式路由混用。本规范中的示例表示应采用的命名，不表示对应接口已经实现。

本文规定路由格式、HTTP 方法、参数命名、常用动作和业务概念用词，以及路由与 Controller、DTO 的名称对应关系。响应外层结构、通用错误码和输出方式遵守[接口返回规范](api-response.md)，具体业务字段由各用例的 DTO 定义。

## 2. 路径格式

| 组成部分 | 规则 | 示例 |
| --- | --- | --- |
| API 前缀 | 固定为 `/api` | `/api` |
| 主版本 | 使用小写 `v` 加主版本号，不使用 `V1`、`version1` 等其他写法 | `/v1`、`/v2` |
| 业务领域 | 严格使用 `app/domains/<domain>` 中的 domain 目录名，保留其单复数、大小写及分隔格式，不另行转换 | `/user`、`/order`、`/post` |
| Controller 分组 | 严格依据处理该接口的 Controller 类型名，去除固定 `Controller` 后缀后转为 lowerCamelCase | `UserProfileController` 对应 `/userProfile` |
| 业务用例 | 使用英文 lowerCamelCase，按“动作 + 业务对象或结果 + 必要限定”命名 | `queryUserProfileByUserId`、`cancelOrder` |

具体约束：

1. 业务用例必须显式包含动作，即使 HTTP 方法已经表达读取或提交，仍保留 `query`、`create` 等动作词。
2. 业务用例直接位于所属 Controller 分组下。每个接口必须具有领域、Controller 两级分组，不增加 `/query`、`/commands`、Service 名等其他中间层级。
3. 不将业务标识放入路径。用户 ID、订单 ID 等通过 GET 的 Query 参数或 POST 的请求体传递。
4. 不使用下划线、连字符或全小写拼接来替代用例的 lowerCamelCase，例如不使用 `query_user_profile`、`query-user-profile`、`queryuserprofile`。
5. 不添加尾部 `/`。不同大小写、不同尾部斜线不能作为两个业务接口的区分方式。
6. 领域分组、Controller 分组和业务用例共同表达完整含义。即使已位于 `/user/userProfile` 分组，仍使用 `queryUserProfileByUserId`，不缩写成 `queryProfile` 或 `queryByUserId`。
7. 领域段、Controller 段按本规范对应代码结构；业务用例描述对外业务能力，不引入表名、SQL、GORM、缓存实现或其他内部实现类型名称。

领域路径以代码中的 domain 目录名为准，目录名为单数就使用单数，为复数就使用复数；多词目录名也原样使用，不单独转换成 lowerCamelCase。同一 domain 不得另设复数名、缩写或其他别名。当前代码的对应关系如下：

| 领域 | 代码中的 domain 目录 | 路径分组 | 用例中的业务对象词 |
| --- | --- | --- | --- |
| 用户 | `app/domains/user` | `/user` | `User` |
| 订单 | `app/domains/order` | `/order` | `Order` |
| 帖子 | `app/domains/post` | `/post` | `Post` |

新增或重命名 domain 时，须同步维护领域路径及相关文档。系统接口同样按实际 domain 目录和 Controller 类型确定路由，遵守统一的路径格式。

会员权益等归属用户领域的能力，可以由 `UserMembershipController` 处理，对应 `/user/userMembership/renewUserMembership`；不因增加一个 Controller 就必须新增一级领域分组。

Controller 分组按以下规则确定：

1. 以对应版本下实际处理请求的 Controller 类型名为准，不按文件名、Service 名或自行指定的简称命名。
2. 仅去除结尾的固定 `Controller` 后缀，保留业务名称中的其他词和单复数，如 `UserProfileController` 对应 `userProfile`，不能另写为 `profile`、`profiles`。
3. 剩余业务名称按单词转换为 lowerCamelCase，缩写统一按普通单词处理，如 `UserIDController` 对应 `userId`、`HTTPProxyController` 对应 `httpProxy`。
4. 同一版本、同一 domain 下，一个 Controller 对应一个固定分组。不同 Controller 转换后的分组名称不得冲突，也不为同一个 Controller 注册多个分组别名。
5. 即使某个 domain 当前只有一个 Controller，也保留 Controller 层级。Controller 的所有对外方法注册到其所属分组下，不将部分方法直接挂在 domain 下。
6. Controller 名称和接口归属构成对外路由的一部分。重命名 Controller 或将接口迁移到其他 Controller 时，必须同步处理路由、接口文档和调用方的迁移，不得将其视为不影响接口的内部重构。

## 3. HTTP 方法与参数位置

| 场景 | HTTP 方法 | 业务参数位置 |
| --- | --- | --- |
| 简单读取、条件查询、统计、只读检查 | `GET` | Query 参数 |
| 输入包含复杂条件组、嵌套对象或批量目标集合的读取 | `POST` | 请求体 |
| 创建、修改、删除、关系变更和其他业务写操作 | `POST` | 请求体 |

本项目业务路由统一使用上述 GET、POST 约定，不额外使用 PUT、PATCH、DELETE 表达同类业务用例。

具体约束：

1. GET 不得执行业务写入、状态迁移、验证码发送、支付发起等操作，也不通过请求体承载业务参数。
2. POST 读取接口仍按读取语义命名，例如需要复杂条件组的订单分页查询使用 `POST /api/v1/order/order/queryOrderPage`，不因使用 POST 就改成 `create`，也不因参数复杂就改成 `search`。
3. 一个版本内，同一领域的对外用例名称保持唯一，并对应明确的 Controller、HTTP 方法及请求、响应契约。不得通过不同 Controller 分组或 GET、POST 承载同名但业务含义不同的接口。
4. 同一业务参数只约定一个位置，不同时从 Query 和请求体读取后决定优先级。鉴权信息等公共请求头按其专门约定处理。
5. POST 默认使用 JSON 请求体；文件上传等需要其他媒体类型的接口，单独明确请求格式，路由名称仍遵守本规范。
6. 批量原子性、部分成功规则以及写操作重试、幂等要求，必须由具体接口契约说明，不能只根据 POST 或 `batch` 名称推断。

## 4. 用例边界与名称组成

### 4.1 按业务契约划分接口

一个接口对应一个明确的业务用例，并定义稳定的请求、响应契约。

| 情况 | 组织规则 |
| --- | --- |
| 业务目的、权限和响应结构一致，仅筛选条件、排序或分页输入不同 | 在同一个用例中扩展请求参数，如用户分页查询增加状态筛选 |
| 业务目的、权限、校验流程或响应语义存在独立差异 | 拆分为独立用例，如用户公开资料、运营用户概览、可领取权益查询 |
| 合并后需要大量互斥参数，或根据参数返回完全不同的响应结构 | 拆分用例，分别定义请求和响应 DTO |
| 多个用例内部查询同一张表，或复用同一个 Service 方法 | 不据此合并对外接口 |
| 仅为现有用例换一种同义名称 | 不新增别名接口，继续使用已有名称 |

每个业务用例必须声明一对专属命名的请求、响应 DTO，与完整用例名严格对应，具体规则见第 8 节；语义一致的子结构可以复用。先确定业务用例边界，再定义对应 DTO，不按数据库表的数量划分接口，也不使用一个通用入口配合 `action` 或 `type` 参数分发多个独立业务用例。

### 4.2 动作、对象与限定

普通用例按以下顺序命名：

```text
动作 + 业务对象或结果 + 必要限定
```

例如：

```text
query + UserProfile + ByUserId = queryUserProfileByUserId
query + User + Page           = queryUserPage
query + User + Overview       = queryUserOverview
cancel + Order                = cancelOrder
```

`By` 只用于表达需要区分的定位依据或业务查询维度，例如 `queryUserProfileByUserId` 和 `queryUserProfileByAccount`。普通可选筛选项放入请求参数，不为每种组合创建 `queryUserPageByStatusAndMembershipAndCreatedAt`。

同一用例确定名称后，不再同时提供省略对象、增加 `By` 或替换同义词的多个别名。已有用户 ID 查询统一命名为 `queryUserProfileByUserId`。

### 4.3 缩写与单复数

1. Controller 段和业务用例段中的缩写按普通单词参与 lowerCamelCase：`Id`、`Url`、`Http`，如 `userId`、`queryUserProfileByUserId`、`queryUserAvatarUrl`。领域段仍原样使用 domain 目录名。
2. Go 导出标识符沿用 Go 的缩写写法：`ID`、`IDs`、`URL`、`HTTP`，如 `QueryUserProfileByUserID`。
3. 业务用例中的对象名称通常使用单数，如 `createUser`、`updateUserProfile`。集合查询使用第 5 节约定的 `List`、`Page`，不再用对象复数创造另一套普通列表查询格式；此规则不改变领域段的 domain 原名。
4. 对多个独立目标执行同一种操作时，使用 `batch` 前缀及复数目标，如 `batchQueryUserProfiles`、`batchDeleteOrders`。
5. 不使用含义不明的缩写或编号，如 `qryUser`、`updProfile`、`queryUserInfo2`、`doOrder`。

## 5. 查询、检索与批量操作用词

| 语义 | 统一格式 | 示例 | 不混用的表达 |
| --- | --- | --- | --- |
| 查询一个对象或一份业务结果 | `query<ObjectOrResult>` | `queryUserProfileByUserId`、`queryUserOverview` | 不用 `get`、`fetch`、`find` 作为同义查询动作 |
| 查询不分页集合 | `query<Object>List` | `queryUserList`、`queryUserRoleList` | 不另用 `listUsers`、`getUsers`、`queryUsers` 表达相同含义 |
| 分页查询集合 | `query<Object>Page` | `queryUserPage`、`queryOrderPage` | 不另用 `pageUsers`、`queryUserListPage`、`queryPagedUsers` |
| 关键词、全文或相关性检索 | `search<Object>List` 或 `search<Object>Page` | `searchPostPage` | 普通字段筛选仍用 `query`；不将 `search` 与 `query` 随意互换 |
| 只获取数量 | `count<Objects>` | `countUsers`、`countOrders` | 不用 `queryUserCount` 表达同一类计数用例 |
| 查询统计指标 | `query<Object>Statistics` | `queryOrderStatistics` | 不用 `stats`、`statistic`、`report` 替代同一统计概念 |
| 只读判断存在性、资格或业务条件 | `check<Condition>` | `checkUserExists`、`checkOrderCancelable` | 不使用含义模糊的 `validateUser`；凭证核验见第 6 节 |
| 对明确的一组目标执行同一操作 | `batch<Action><Objects>` | `batchQueryUserProfiles`、`batchUpdateUserProfiles` | 不用 `bulk`、`multi` 作为同义批量前缀 |

补充要求：

- `List` 表示不分页，不表示可以无限量返回；具体接口必须明确适用范围或数量限制。
- `Page` 表示具有分页契约。页码与游标两种分页方式都使用 `Page`，具体采用哪一种必须在接口中明确。
- `search` 表达检索业务语义，不由 HTTP 方法、数据库查询复杂度或返回条数决定。
- `batch` 表达对多个明确目标执行同一种操作。普通分页查询不因返回多条记录就使用 `batch`。
- 只读检查的结果不保证后续写操作一定成功；写操作仍须校验执行时的业务条件。

## 6. 写操作与业务动作用词

| 语义 | 统一动作 | 示例 | 使用边界 |
| --- | --- | --- | --- |
| 创建业务对象 | `create` | `createUser`、`createOrder` | 不用 `add`、`insert`、`save` 表达创建实体 |
| 修改已有对象的一般信息 | `update` | `updateUserProfile` | 不用 `edit`、`modify`、`change` 作为一般更新的同义词；修改范围由请求契约明确 |
| 删除业务对象 | `delete` | `deletePost` | 不用 `remove`、`drop` 替代；是否软删除由具体契约定义 |
| 恢复已删除或已归档对象 | `restore` | `restorePost` | 不用 `recover` 与其混用；不表示解冻或重新启用 |
| 向集合增加成员或条目 | `add` | `addUserToGroup` | 表达集合成员变化，不等于创建用户 |
| 从集合移除成员或条目 | `remove` | `removeUserFromGroup` | 不等于删除成员对象 |
| 建立、解除明确的绑定关系 | `bind`、`unbind` | `bindUserAccount`、`unbindUserAccount` | 不与集合成员操作或领域已有动作随意互换 |
| 启用、停用对象或能力 | `enable`、`disable` | `enableUser`、`disableUser` | 不等于冻结、取消、关闭或删除 |
| 业务冻结、解除冻结 | `freeze`、`unfreeze` | `freezeUser`、`unfreezeUser` | 与一般启停区分；适用状态和限制由领域规则定义 |
| 加锁、解锁 | `lock`、`unlock` | `lockOrder`、`unlockOrder` | 仅用于对外存在的业务锁定能力，不将内部数据库锁暴露为业务接口 |
| 取消可撤回的请求或流程 | `cancel` | `cancelOrder` | 不表示删除记录或任意结束流程 |
| 按业务规则结束对象或流程 | `close` | `closeOrder` | 与取消区分；取消和关闭不得作为同义接口同时存在 |
| 提交待处理或待审批的业务申请 | `submit` | `submitRefundApplication` | 不与审批决定混用 |
| 审批通过、驳回 | `approve`、`reject` | `approveRefundApplication`、`rejectRefundApplication` | 表达审批决定，不用通用 `updateStatus` 代替 |
| 完成业务流程 | `complete` | `completeOrder` | 表达业务完成，不用 `finish`、`done` 作为同义动作 |
| 确认明确的业务事实 | `confirm` | `confirmOrderReceipt` | 必须写出确认对象或事实，不使用 `confirmOrder` 表达所有确认操作 |
| 发布、撤下发布内容 | `publish`、`unpublish` | `publishPost`、`unpublishPost` | 不等于创建、删除帖子 |
| 发送消息、通知或验证码 | `send` | `sendVerificationCode` | 是业务操作，使用 POST，不使用 GET |
| 核验凭证、验证码等证明材料 | `verify` | `verifyVerificationCode` | 核验可能消耗凭证或次数，统一使用 POST；不同于只读资格检查 `check` |
| 续期、续费 | `renew` | `renewUserMembership` | 不用 `extend`、`recharge` 与同一会员续期语义混用 |
| 关注、取消关注 | `follow`、`unfollow` | `followUser`、`unfollowUser` | 采用明确领域动作，不使用 `createFollow`、`cancelFollow` 作为同义入口 |
| 点赞、取消点赞 | `like`、`unlike` | `likePost`、`unlikePost` | 与关注分别命名，不以通用 `addRelation` 代替 |
| 导入、导出 | `import`、`export` | `importUsers`、`exportOrders` | 说明实际执行、任务创建及下载方式，不根据名称推断同步或异步 |

以上动词的不同含义必须保留。例如，删除订单、取消订单和关闭订单是不同的业务能力；业务明确区分时分别使用 `deleteOrder`、`cancelOrder`、`closeOrder`。

有明确业务含义的操作优先使用具体动作。不得通过 `updateOrderStatus` 加任意目标状态，绕过取消、审批、完成等各自不同的校验和处理流程。

状态变更接口应明确表达目标状态。例如，`enableUser` 将用户设为启用，`disableUser` 将用户设为停用，这两个接口可以同时提供。避免使用 `toggleUserEnabled` 等翻转当前状态的接口，以免重复请求时再次翻转状态，产生与调用方预期不一致的结果。

新增动作前先检查本表。确实属于新的业务语义时，补充其固定英文词、适用边界和示例，再在各接口中统一使用；不因某个开发者习惯不同而新增同义词。

## 7. 通用概念与参数命名

### 7.1 通用概念用词

本节统一跨业务通用概念的英文词根和语义边界。具体领域对象、业务身份、字段含义及对象关系由各业务定义，并在接口中使用一致的术语。

下表中的词根按使用位置转换命名格式：Controller 分组、业务用例使用 lowerCamelCase，Go 导出标识符按第 4.3 节处理，请求参数使用 snake_case；领域段仍严格沿用 domain 目录原名。

| 通用概念 | 统一英文词根 | 语义边界 |
| --- | --- | --- |
| 对象标识 | `id` | 标识对象身份，与业务编号、编码区分；Go 标识符中写为 `ID` |
| 业务编号 | `no` | 表达业务使用的编号，同一含义不混用 `num`、`number`；页码等数值位置不属于业务编号 |
| 编码 | `code` | 表达约定编码体系中的代码，与对象标识 `id` 区分 |
| 状态 | `status` | 表达对象的业务状态，不用 `state` 替代同一概念；独立状态机概念另行定义 |
| 类型 | `type` | 表达对象或操作的类型，不与分类 `category` 混用 |
| 分类 | `category` | 表达分类归属，不作为 `type` 的同义词 |
| 名称 | `name` | 表达对象名称，不与内容标题 `title` 混用 |
| 标题 | `title` | 表达内容或展示单元的标题，不作为 `name` 的随意替换 |
| 详情 | `detail` | 单个对象的详细业务视图，与摘要、概览区分 |
| 摘要 | `summary` | 对明确对象或内容的精简表达，不与概览随意互换 |
| 概览 | `overview` | 对一个业务范围内的对象、状态或指标进行综合呈现 |
| 列表 | `list` | 保留元素顺序的集合；作为查询用例后缀时，按第 5 节表示不分页结果 |
| 集合 | `set` | 元素唯一，业务语义不依赖顺序 |
| 键值映射 | `map` | 通过明确的键定位值，须说明键和值的含义 |
| 分页结果 | `page` | 具有明确分页契约的集合，作为查询用例的结果后缀 |
| 统计结果 | `statistics` | 一份统计结果，可包含多个指标；单一对象个数按第 5 节使用 `count` 动作 |
| 对象个数 | `count` | 表达统计范围内的对象个数，不与业务计量数量混用 |
| 业务计量数量 | `quantity` | 表达带业务单位的数量，须明确单位，不作为 `count` 的同义词 |
| 金额 | `amount` | 表达金额，须明确币种和计价单位，不与对象个数或单价混用 |
| 单价 | `price` | 表达每个计量单位的价格，与总金额区分 |

具体接口按实际业务语义选用词根；视图的划分依据业务目的和契约，字段数量不直接决定其名称。

### 7.2 参数名称与格式

业务请求参数统一使用英文 snake_case。相同含义的参数在不同接口、Query 参数和 JSON 请求体中使用相同名称，不因所属 Controller 或传递位置不同而另起简称。

下表中的 `<object>` 表示具体业务对象的统一英文名称，按 snake_case 填写，实际参数中不保留尖括号。对象名称由业务术语确定，本规范不预设具体业务实体。

| 参数概念 | 统一名称或格式 | 说明 |
| --- | --- | --- |
| 对象标识 | `<object>_id` | 说明标识的对象、范围和取值约束，不与业务编号混用 |
| 业务编号 | `<object>_no` | 与对象标识 `<object>_id` 区分 |
| 业务编码 | `<object>_code` | 与业务编号、对象标识分别定义 |
| 对象标识列表 | `<object>_id_list` | 使用数组，说明顺序和重复值规则，不另用复数形式作为同义名称 |
| 对象标识集合 | `<object>_id_set` | 使用数组，元素唯一，业务不依赖顺序；明确重复值的校验或去重规则 |
| 页码 | `page_number` | 页码分页统一使用此名称，不混用 `page`、`page_no`、`page_index`；从 1 开始 |
| 每页数量 | `page_size` | 页码分页、游标分页均使用此名称 |
| 翻页令牌 | `page_token` | 用于游标分页，作为不透明令牌传递，不依赖内部编码；不与 `page_number` 在同一接口中混用 |
| 检索关键词 | `keyword` | 不混用 `q`、`key` 等别名 |

批量目标按第 3 节通过 POST 请求体中的数组传递，不使用逗号拼接字符串。参数包含多个相同类型但角色不同的对象时，须在名称中写明角色，避免使用含义不明的编号后缀区分。

分页接口必须明确默认值、数量上限和翻页规则。具体接口按业务需要选择参数，单条查询不增加分页参数，页码分页与游标分页分别采用各自约定的输入。

### 7.3 业务术语的维护

1. 同一业务概念使用相同英文词根和命名格式；含义不同的概念分别定义，不能仅因中文称呼相近就共用一个词。
2. 新增领域概念时，在对应业务的接口说明中记录中文含义、统一英文词、适用边界及命名示例，供该业务的各接口共同遵守。
3. 跨业务共用的通用概念统一维护其词汇约定，不在单个接口中重新定义。本节不依据示例代码或数据库表推导通用于所有业务的实体含义和字段语义。

## 8. 业务用例与 DTO 的严格对应关系

路由最后一段的完整业务用例名是命名基准。每个业务用例必须且只能对应一对专属命名的请求、响应 DTO；请求 DTO 和响应 DTO 分别与该业务用例一一对应。Controller 方法、DTO 类型和 DTO 文件均由该用例名按固定规则确定，不允许各自起名。

一一对应及名称唯一性以同一领域、同一主版本为范围；不同领域或不同主版本可以在各自的包中使用相同的 DTO 名，不额外拼接版本号。

用例专属的 Response DTO 描述统一响应中的 `data`，公共外层 `Response[T]` 由响应组件提供；该外层不替代用例 DTO，也不改变以下命名对应规则，详见[接口返回规范](api-response.md)。

```text
Go 用例名       = 完整业务用例名转为 Go 导出标识符形式
Controller 方法 = Go 用例名
请求 DTO 名     = Go 用例名 + Request
响应 DTO 名     = Go 用例名 + Response
DTO 文件名      = 完整业务用例名转为 snake_case + .go
```

转换时只改变命名格式和第 4.3 节约定的缩写写法，如路由中的 `Id` 对应 Go 标识符中的 `ID`；业务词、单复数及限定部分必须全部保留。

| 项目 | 示例 |
| --- | --- |
| domain 目录与领域分组 | `app/domains/user` 对应 `/user` |
| Controller 类型与 Controller 分组 | `UserProfileController` 对应 `/userProfile` |
| 路由用例 | `queryUserProfileByUserId` |
| 完整路由 | `/api/v1/user/userProfile/queryUserProfileByUserId` |
| Controller 方法 | `QueryUserProfileByUserID` |
| 请求 DTO | `QueryUserProfileByUserIDRequest` |
| 响应 DTO | `QueryUserProfileByUserIDResponse` |
| DTO 文件 | `query_user_profile_by_user_id.go` |

必须遵守以下约束：

1. 动作、业务对象、`List`、`Page`、`batch` 前缀、`By...` 限定等都是完整用例名的一部分。不得在 DTO 名中省略、替换同义词或增添另一套业务限定。
2. 例如 `queryUserProfileByUserId` 必须使用 `QueryUserProfileByUserIDRequest` 和 `QueryUserProfileByUserIDResponse`，不能改用 `GetUserRequest`、`UserProfileRequest` 或 `QueryUserProfileResponse`。
3. 不同业务用例必须分别声明自己的请求、响应 DTO 名称，即使当前字段完全相同，也不能共用某个用例的顶层 DTO 名，或共用 `CommonRequest`、`CommonResponse` 代替各自的 DTO。
4. 共享字段可以通过同包结构、组合或指向共享结构的类型别名复用，但必须保留各用例专属的 DTO 声明和名称。“专属”约束顶层声明名和用途，不要求复制相同字段。不能直接以 `UserProfile` 等共享结构名代替用例对应的 Response 名。
5. 没有业务入参或没有业务返回字段时，也声明对应命名的空 Request 或 Response DTO。空 DTO 不改变 GET 参数位置，也不要求增加无意义的请求体或响应字段；GET 的空 Request 不表示客户端需要发送 `{}`。
6. 业务用例改名时，必须同步修改 Controller 方法、Request、Response、DTO 文件名及相关引用、文档，不能只修改路由末段或其中一个 DTO 名。

Validator 和 DTO 的目录及包组织遵守以下约定：

1. **先按接口主版本划分，再按 Controller 分组。** Validator 使用 `validator/v<version>/<controller>/`，DTO 使用 `dto/v<version>/<controller>/`。`<controller>` 取 Controller 类型名去掉 `Controller` 后缀，再转换为 snake_case；例如 `UserProfileController` 对应 `user_profile`。Controller 自身仍采用 `controller/v<version>/<controller>_controller.go`，不增加同名目录。

2. **每个用例在所属分组中独立组织文件。** Validator 文件使用 `<use_case>_validator.go`，DTO 文件使用 `<use_case>.go` 并同时组织该用例的请求、响应定义。用例及类型继续使用完整名称，不因已有 Controller 目录而省略业务对象，也不额外拼接 `Controller`。不同 Controller 的独立业务契约仍使用可区分的用例名称。

   ```text
   app/domains/<domain>/
     controller/v<version>/
       <controller>_controller.go
     validator/v<version>/<controller>/
       <use_case>_validator.go
     dto/v<version>/<controller>/
       <use_case>.go
       <shared_concept>.go
   ```

3. **仅在同一 Controller 内复用的 DTO 放在该分组中。** 共享结构按具体业务概念命名文件，例如用户资料分组中的 `dto/v1/user_profile/user_profile.go`。其他用例在同包直接引用 `UserProfile`；不为这类局部复用再建立 `shared` 子包。只有真正跨 Controller 共享且语义一致的结构，才按需放入该版本的 `dto/v<version>/shared/`；共享包不反向导入各 Controller 的 DTO 包，各分组也不互相导入来复用结构。

4. **Controller 分组目录分别构成独立 Go 包。** 例如两个 `user_profile` 目录都声明 `package userprofile`，使用时以 `userprofilevalidator`、`userprofiledto` 等导入别名区分职责。目录分组不是同一个 `v1` 包内的纯文件夹分类；包内标识符只在对应分组包中共享。Validator 和 Controller 可以依赖 DTO，DTO 不反向依赖它们。

5. **不同接口主版本保留独立目录和契约。** V2 使用 `validator/v2/<controller>/`、`dto/v2/<controller>/`；跨 Controller 的共享结构同样按版本组织。不能在 V1 目录中承载 V2 的新契约，也不要求没有跨 Controller 复用需求的版本预先建立 `shared` 包。

领域 Router 在已有版本分组下，依次注册 domain、Controller 和业务用例，不重复拼接 `/api/v1`：

```go
func (r *UserRouter) RegisterV1Routes(v1 *gin.RouterGroup) {
	user := v1.Group("/user")
	userProfile := user.Group("/userProfile")
	userProfile.GET("/queryUserProfileByUserId", r.queryUserProfileByUserIDValidator.QueryUserProfileByUserID)
}
```

路由进入对应用例的 Validator，绑定 Query 参数到请求 DTO 并校验后，再交给 Controller。路由、方法、DTO 的名称对齐要求不扩展为必须将 Repository 的数据库操作名称也改成对外接口名。

### 8.1 Request DTO 的标准绑定与输入语义

Request DTO 通过 Go 字段类型及 `form`、`json` 等映射、序列化标签描述请求参数，允许使用 `json:",string"` 等标准表示方式。接口设计应先明确输入的业务含义，使通用绑定结果足以表达请求意图。

1. **所有 Request DTO 均禁止自定义 `UnmarshalJSON` 方法。** HTTP 参数统一通过公共 `Bind` 映射，GET 使用 Gin 的 Query 映射能力，POST 使用标准库 JSON 解码能力；不在 Request DTO 中手工解码 JSON、记录字段出现情况或执行参数校验。
2. **Request DTO 禁止使用 `binding` 等自动校验标签，Bind 不执行自动校验。** 必填、非空、长度、范围、格式及字段关系等输入规则，由对应 Validator 的私有校验函数显式执行；可以调用 `check` 和 models 规则，不要求重复实现底层算法。JSON 语法错误或输入无法转换为 DTO 字段类型，仍属于绑定失败，由 Validator 入口立即返回，不继续检查部分 DTO。
3. 每个输入字段必须明确是否必填、是否允许 `null`、空字符串的含义，以及业务是否支持清空原值。不能仅因技术上能够区分这些状态，就为接口增加相应业务语义。
4. 标准绑定无法区分“未提供”和“显式 `null`”时，应通过接口语义让两者含义一致，或调整请求结构及业务用例，明确表达不同操作。不应通过 `XxxProvided`、`XxxPresent` 等仅用于记录解码状态的辅助字段维持含混的输入契约。
5. 例如，对于业务不支持清空的必填文本字段，可以将未提供、`null` 和空字符串统一作为参数错误处理，不赋予 `null` 隐式清空的含义。具体规则须在对应接口契约中说明。
6. 零值具有合法业务含义时，可以采用标准绑定支持的指针字段。例如 `*int64` 可区分“未提供或 `null`”与显式的 `0`；指针本身不能区分未提供和 `null`，不得据此承诺不同操作。
7. 绑定后的字段校验在对应 Validator 中完成，依赖业务状态的判断由 Service 完成，不通过 Request DTO 的自定义反序列化承载这些职责。
