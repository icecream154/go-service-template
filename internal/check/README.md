# 公共数据检查

`check` 按能力组划分子包。根包只定义共享错误，各能力包依赖根包，根包不反向依赖能力包。

```text
check/
  doc.go, error.go     # 包契约与共享错误
  scalar/             # 整数、浮点、精确十进制、布尔
  text/               # 文本、长度、Unicode、模式
  collection/         # 切片、map、唯一性、枚举集合
  relation/           # 存在性与条件组合
  datetime/           # 时间、时间戳、时长
  format/             # URL、网络、邮箱、标识符、编码、JSON、路径
```

## 1. 能力入口

| 子包 | 常用入口 | 契约要点 |
| --- | --- | --- |
| `scalar` | `ParsePositiveInt64`、`Int64InRange`、`ParseFiniteFloat64`、`ApproximatelyEqualFloat64`、`ParseDecimal`、`DecimalFits`、`ParseBoolText` | 区间包含两端，浮点拒绝非有限值，精确十进制不经过浮点转换，布尔文本仅接受 `true` / `false` |
| `text` | `ValidUTF8`、`NotBlank`、`RuneCountInRange`、`GraphemeCount`、`WeightedLength`、`NewRuneSet`、`Normalized`、`CompileFullPattern` | 字节、码点、字素和加权长度分开；检查不执行 trim、规范化或大小写转换 |
| `collection` | `SliceLengthInRange`、`Unique`、`UniqueBy`、`Each`、`RequireKeys`、`AllowedKeys`、`EachMapEntry`、`OneOf` | 不修改集合、顺序或重复项；map 检查稳定定位失败键 |
| `relation` | `AtLeastOne`、`AtMostOne`、`ExactlyOne`、`AllOrNone`、`RequiredIf`、`NotNil` | 调用方明确判断是否提供，不反射 DTO、不把零值推断为缺失 |
| `datetime` | `ParseTime`、`ParseCanonicalTime`、`UnixTime`、`TimeInRange`、`WithinPast`、`TimePrecision`、`ParseDuration` | 时区、时间戳单位和参考时刻显式传入，不读取系统当前时间 |
| `format` | `ParseHTTPURL`、`Hostname`、`ParseIPAddress`、`ParseMailbox`、`ParseUUID`、`ParseULID`、`ParseBase64`、`ParseJSON`、`LexicallyLocalPath` | 本地形式检查，不验证资源存在、身份或访问权限；解析方法返回独立结果 |

业务字段的上下限、允许值、来源和必填性由调用方组合，不在公共包内声明用户或接口规则。当前包没有接入任何 HTTP Validator、models 或 Service。

## 2. 错误分类

```go
errors.Is(err, check.ErrInvalidValue) // 数据不满足规则。
errors.Is(err, check.ErrInvalidRule)  // 边界、模式、策略等规则定义错误。
```

`Violation` 的 `Rule`、`Reason` 表达基础约束，默认不保存原始输入。`RuleError` 表达无效的规则定义，两类错误不能一律映射为客户端参数错误。

`IndexError`、`KeyError` 保留集合位置及原始错误链。通过 `errors.As` 读取位置，通过 `errors.Is` 继续判断原因；键不会自动拼入错误文案。它们不包含 HTTP 字段路径或业务错误码。

检查回调、字符权重等调用方函数也必须是只读纯函数。集合会保留回调返回的原始错误，不将未知错误伪装成字段校验失败。

## 3. 数值与集合

整数文本按十进制解析；有符号解析允许前导正号和零，无符号解析不接受符号。空白、小数、指数及溢出均被拒绝。浮点解析采用 Go 浮点文本语法，再拒绝 NaN 和无穷值。

`Decimal` 接受 `[+-]?[0-9]+(\.[0-9]+)?`，保留原文和小数尾零，零值表示数值 0。`Scale`、`DecimalScaleInRange` 检查输入的小数位数；`DecimalFits` 判断数值是否能精确放入指定精度和小数位，因此允许去掉不影响数值的小数尾零。输入体积限制应在解析前用字节数检查明确控制。

集合的 nil 与空集合长度均为 0；需要区别时调用 `SliceNotNil` 或 `MapNotNil`。`Each` 对空集合通过，至少一项需另外检查。`OneOf` 的空允许集合不允许任何值，`NoneOf` 的空禁止集合不禁止任何值。

唯一性和枚举采用 Go 相等语义，不将字符串转小写或解析别名。NaN 及动态不可比较的输入返回输入错误；枚举规则集合包含这类值则返回规则定义错误。字符串键 map 按键排序副本确定问题顺序，原始 map 不改写。

## 4. 文本与 Unicode

`ByteCount` 仅计算原始字节数。码点、字素与 Unicode 规则拒绝非法 UTF-8，不用替换符掩盖原始编码问题。

字素分段使用固定的 `uniseg v0.4.7`，对应 Unicode 15.0.0。`GraphemeUnicodeVersion`、`CharacterUnicodeVersion`、`NormalizationUnicodeVersion` 分别公开所用数据版本；升级时应审查字符规则的行为变化。

`WeightedLength` 按码点调用显式权重函数，不预设“中文算两个”。负权重属于规则定义错误，累计溢出返回输入错误。

`CompileFullPattern` 要求整个输入匹配，`CompileFragmentPattern` 仅要求存在匹配片段。两类对象应在装配时编译后复用；零值或 nil 规则对象不可用。字符集合、正则对象构造后只读，检查期间不增加缓存或修改规则。

## 5. 时间与格式

`ParseTime` 使用 Go 的时间解析语义。`ParseCanonicalTime` 另外要求同一布局格式化后与原文相同，用于需要规范文本的场景。时间点比较使用实际时刻，`WithinPast` / `WithinFuture` 接收参考时间，时间精度采用 Go 的绝对时间截断对齐语义，不代表本地日历边界。

URL 的通用解析允许相对引用；HTTP URL 解析另外要求绝对、层次型 HTTP(S) 和主机名。端口、userinfo、query、fragment 和主机允许集合通过独立方法组合，来源与远程信任由上层决定。

`HostnameOptions` 显式控制尾部根点、通配标签、下划线、IDNA 和标签数量。IP zone、IPv4-mapped IPv6、非规范 CIDR 可以分别检查，不执行 DNS 查询。

`ParseEmailAddress` 允许显示名，`ParseMailbox` 仅接受裸邮箱。UUID 的结构、规范大小写、版本和变体分开检查；ULID 拒绝超出 128 位范围的表示。

Hex 接受空串和大小写字母；`CanonicalHex` 可以要求具体大小写。Base64 明确选择标准或 URL 字符表及是否填充，拒绝空白和非规范填充位，解码前检查结果大小上限。

独立 JSON 检查不替代 HTTP 绑定。`JSONOptions` 必须给出正字节上限和明确深度：标量深度为 0、对象或数组从 1 计数，技术上限为 1000。可拒绝重复键或限制顶层类型；始终拒绝尾部第二个值、非法 UTF-8 和未配对的 Unicode 代理项。解析后的数字为 `json.Number`，保留精度。

路径使用显式 POSIX / Windows 词法规则，不依据执行机器隐式选择平台。`LexicallyLocalPath` 仅检查相对路径词法不越界，不解析符号链接或证明真实文件访问安全。
