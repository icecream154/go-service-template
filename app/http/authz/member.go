package authz

import (
	"errors"
	"strconv"

	"go-service-template/app/identity"
	"go-service-template/internal/authn"

	"github.com/gin-gonic/gin"
)

// ErrInvalidMemberIdentity 表示已认证的 member 主体标识无法表示本项目的内部用户 ID。
// 凭证验证成功只保证身份来源可信，不保证 SubjectID 满足 member 的正 int64 约定。
// Controller 将其映射为 401，并设置 invalid_token 认证挑战；它不是请求字段错误。
// 错误文案不包含主体原文、Token 或 strconv 的解析细节，调用方用 errors.Is 识别。
var ErrInvalidMemberIdentity = errors.New("authenticated member identity must contain a positive 64-bit user ID")

// RequireMemberUserID 将当前请求的 user/member 身份转换为可信的内部用户 ID。
//
// 本项目约定 member 的 SubjectID 是内部用户 ID 的十进制文本。其他用户命名空间
// 可能使用外部账户或 UUID，服务身份也可能使用服务名称，因此这一转换属于项目的
// HTTP 身份适配，放在 app/http/authz；internal/authn 仍以字符串表达通用主体标识。
//
// 执行分为两步：先通过 RequirePrincipal 要求已认证的 user/member 身份，再按
// 十进制、有符号 64 位整数解析 SubjectID，并确认结果大于 0。没有身份或身份范围
// 不符时，直接返回 RequirePrincipal 的错误；解析失败、溢出、零值或负数统一返回
// ErrInvalidMemberIdentity。任意失败都返回用户 ID 0，调用方必须先检查 error。
//
// SubjectID 来自认证中间件保存的 Principal，没有经过 Request DTO 的 Validator。
// 即使目标 user_id 已完成字段校验，这里的解析错误也不能忽略。本方法不裁剪或修复
// 主体文本，不从请求参数补取发起方 ID，也不重新读取 Header 或验证 JWT。
//
// 成功只表示取得可信的 member 用户 ID，不代表该用户存在、处于启用状态，或有权
// 操作某个对象。Controller 决定是否要求目标用户等于发起方；Service 继续保障业务
// 状态和本人关系等用例约束。本方法不接收目标 ID，不隐含所有 member 接口只能访问本人。
//
// 本方法只返回 ID 或错误，不查数据库、不写 HTTP 响应、不设置挑战头，也不调用
// Abort。Controller 负责错误映射、响应及 return，并在允许继续时用结果构造 Service Params。
func RequireMemberUserID(ctx *gin.Context) (int64, error) {
	// 身份范围必须同时匹配类别和命名空间，不能把其他用户或服务的主体解释为 member ID。
	principal, err := RequirePrincipal(ctx, authn.PrincipalKindUser, identity.UserNamespaceMember)
	if err != nil {
		return 0, err
	}

	// 使用认证结果中的原始主体文本解析，不采用调用方在 DTO 中自行声明的身份。
	userID, err := strconv.ParseInt(principal.SubjectID, 10, 64)
	if err != nil || userID <= 0 {
		return 0, ErrInvalidMemberIdentity
	}
	return userID, nil
}
