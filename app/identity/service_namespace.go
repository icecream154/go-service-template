package identity

// 本文件集中维护服务调用方的命名空间，已登记管理服务身份体系 admin。
// 公共契约中的 authn.PrincipalKindService 声明服务主体大类，具体服务身份仍须经过认证。
//
// 服务命名空间的扩展约定：
//  1. 确定需要接入的服务身份体系后，在本文件声明 ServiceNamespace 前缀的常量，
//     并说明可信身份来源和主体标识范围；内部服务体系与合作方服务体系可分别定义。
//  2. 服务命名空间只与 authn.PrincipalKindService 配合使用，不复用用户命名空间冒充用户。
//     同一体系新增调用服务时使用新的 SubjectID，不为每个服务单独创建命名空间。
//  3. 认证实现和身份使用方共同引用常量，并同步受影响的可信身份来源映射和类别、命名空间判断。
//     声明常量不会自动完成服务认证，也不会授予该服务代表某个用户调用的权限。

// ServiceNamespaceAdmin 表示管理服务身份体系，只与 authn.PrincipalKindService 配合使用。
// SubjectID 是该体系中经认证的具体管理服务标识；admin 不表示用户的管理员角色。
// 命名空间由可信认证结果确定，不能通过请求自行声明 admin 获得管理服务身份。
const ServiceNamespaceAdmin = "admin"
