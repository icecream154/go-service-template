// Package identity 集中声明本项目采用的身份命名空间，供认证实现和身份使用方共同引用。
// 本包只维护项目身份约定，不依赖认证实现、Service 或 Repository，也不执行凭证校验。
package identity

// 用户命名空间的扩展约定：
//  1. 新增独立的用户账户体系时，在本文件声明 UserNamespace 前缀的常量，
//     并注释其账户范围和主体标识含义；例如独立员工账户体系可使用 staff。
//  2. 用户命名空间与 authn.PrincipalKindUser 配合使用，不因此新增 PrincipalKind。
//     同一体系新增用户只新增 SubjectID；角色、权限或会员等级变化不改变命名空间。
//  3. 认证实现生成身份、使用方判断身份时，都引用同一个常量，不各自填写字符串。
//     新增常量时同步受影响的可信身份来源映射和类别、命名空间判断；常量本身不提供认证能力。

// UserNamespaceMember 表示普通用户账户体系，只与 authn.PrincipalKindUser 配合使用。
// SubjectID 表示该体系中的用户标识；member 是身份命名空间，不表示会员等级或权限。
const UserNamespaceMember = "member"
