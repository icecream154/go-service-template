package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	httpauthz "go-service-template/app/http/authz"
	"go-service-template/internal/check"
	"go-service-template/internal/response"

	"github.com/gin-gonic/gin"
)

func TestUserMembershipDTO(t *testing.T) {
	// 从未开通没有权益对象，转换必须保留 nil，响应中才会输出 null。
	t.Run("not opened", func(t *testing.T) {
		if got := userMembershipDTO(nil); got != nil {
			t.Fatalf("DTO = %#v, want nil", got)
		}
	})
	// 传入明确过期的权益，验证转换仍保留等级与历史时间，不清空或重新判断会员状态。
	t.Run("retain expired membership", func(t *testing.T) {
		effectiveAt := time.Date(2025, 1, 1, 0, 0, 0, 123000000, time.UTC)
		expiredAt := effectiveAt.Add(30 * 24 * time.Hour)
		membership := &models.UserMembership{ID: 90, UserID: 7, MembershipLevel: 10, EffectiveAt: effectiveAt, ExpiredAt: expiredAt, CreatedAt: effectiveAt, UpdatedAt: expiredAt}
		before := *membership
		got := userMembershipDTO(membership)
		want := &usermembershipdto.UserMembership{MembershipID: 90, MembershipLevel: 10, EffectiveAt: effectiveAt, ExpiredAt: expiredAt}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("DTO = %#v, want %#v", got, want)
		}
		if !reflect.DeepEqual(*membership, before) {
			t.Error("DTO conversion changed the source membership")
		}
	})
}

func TestMembershipChangeRecordDTOList(t *testing.T) {
	level := int32(10)
	beforeEffectiveAt := time.Date(2026, 1, 1, 0, 0, 0, 123000000, time.UTC)
	beforeExpiredAt := beforeEffectiveAt.Add(30 * 24 * time.Hour)
	afterExpiredAt := beforeExpiredAt.Add(30 * 24 * time.Hour)
	tests := []struct {
		name    string
		records []*models.MembershipChangeRecord
		want    []usermembershipdto.MembershipChangeRecord
	}{
		// nil 历史必须转换为非 nil 空列表，保持空页的 [] 协议。
		{name: "nil list", want: []usermembershipdto.MembershipChangeRecord{}},
		// 已分配但没有记录的列表同样编码为 []，不能退回 null。
		{name: "empty list", records: []*models.MembershipChangeRecord{}, want: []usermembershipdto.MembershipChangeRecord{}},
		// 续费排在首次开通之前，验证原顺序及前后快照全部保留，首次开通的前值仍为 nil。
		{name: "renewal and opening snapshots", records: []*models.MembershipChangeRecord{
			{ID: 9, UserID: 7, MembershipID: 90, OrderID: 102, ChangeType: 20,
				BeforeMembershipLevel: &level, BeforeEffectiveAt: &beforeEffectiveAt, BeforeExpiredAt: &beforeExpiredAt,
				AfterMembershipLevel: 10, AfterEffectiveAt: beforeEffectiveAt, AfterExpiredAt: afterExpiredAt, CreatedAt: beforeExpiredAt},
			{ID: 3, UserID: 7, MembershipID: 90, OrderID: 101, ChangeType: 10,
				AfterMembershipLevel: 10, AfterEffectiveAt: beforeEffectiveAt, AfterExpiredAt: beforeExpiredAt, CreatedAt: beforeEffectiveAt},
		}, want: []usermembershipdto.MembershipChangeRecord{
			{RecordID: 9, MembershipID: 90, OrderID: 102, ChangeType: 20,
				BeforeMembershipLevel: &level, BeforeEffectiveAt: &beforeEffectiveAt, BeforeExpiredAt: &beforeExpiredAt,
				AfterMembershipLevel: 10, AfterEffectiveAt: beforeEffectiveAt, AfterExpiredAt: afterExpiredAt, CreatedAt: beforeExpiredAt},
			{RecordID: 3, MembershipID: 90, OrderID: 101, ChangeType: 10,
				AfterMembershipLevel: 10, AfterEffectiveAt: beforeEffectiveAt, AfterExpiredAt: beforeExpiredAt, CreatedAt: beforeEffectiveAt},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := membershipChangeRecordDTOList(test.records)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DTO list = %#v, want %#v", got, test.want)
			}
			if len(test.want) == 0 {
				body, err := json.Marshal(got)
				if err != nil || string(body) != "[]" {
					t.Errorf("empty list JSON = %s, error = %v; want []", body, err)
				}
			}
		})
	}
}

func TestMapUserMembershipErrorCodes(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantCode     response.ErrorCode
		wantBusiness response.BusinessErrorCode
	}{
		// 缺少身份归入认证失败，不提供会员业务码。
		{name: "authentication required", err: httpauthz.ErrAuthenticationRequired, wantCode: response.CodeUnauthenticated},
		// 无效会员身份是认证问题，不能当作会员尚未开通或普通权限失败。
		{name: "invalid member identity", err: httpauthz.ErrInvalidMemberIdentity, wantCode: response.CodeUnauthenticated},
		// 检查规则本身非法属于内部故障，不应提示调用者修复输入。
		{name: "invalid rule", err: check.NewRuleError("range", "invalid bounds"), wantCode: response.CodeInternalError},
		// 资料不存在经过错误包装后仍要输出用户未找到的协议码。
		{name: "wrapped user not found", err: fmt.Errorf("lookup: %w", userservice.ErrUserProfileNotFound), wantCode: response.CodeNotFound, wantBusiness: "USER_PROFILE_NOT_FOUND"},
		// 已确认身份但范围不符时输出会员权限业务码。
		{name: "principal not allowed", err: httpauthz.ErrPrincipalNotAllowed, wantCode: response.CodePermissionDenied, wantBusiness: "USER_MEMBERSHIP_PERMISSION_DENIED"},
		// Controller 中访问他人的拒绝统一映射为会员权限失败。
		{name: "controller access denied", err: errUserMembershipAccessDenied, wantCode: response.CodePermissionDenied, wantBusiness: "USER_MEMBERSHIP_PERMISSION_DENIED"},
		// Service 复核权限失败即使被包装，也要保持与 Controller 相同的业务分类。
		{name: "wrapped service permission", err: fmt.Errorf("query: %w", userservice.ErrUserMembershipPermissionDenied), wantCode: response.CodePermissionDenied, wantBusiness: "USER_MEMBERSHIP_PERMISSION_DENIED"},
		// 结构化输入错误映射为参数错误，不附加错误的会员业务码。
		{name: "invalid input", err: check.NewViolation("page_size", "outside range"), wantCode: response.CodeInvalidArgument},
		// 同时包含规则与输入错误时，规则故障优先，避免把程序错误归咎于用户。
		{name: "rule error takes precedence", err: errors.Join(check.ErrInvalidValue, check.ErrInvalidRule), wantCode: response.CodeInternalError},
		// 存储权益状态异常属于内部数据故障，不能当作非法请求参数。
		{name: "invalid persisted membership", err: models.ErrInvalidUserMembershipState, wantCode: response.CodeInternalError},
		// 未识别的技术错误采用内部错误兜底，不伪造已知业务原因。
		{name: "unknown failure", err: errors.New("storage unavailable"), wantCode: response.CodeInternalError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, business := mapUserMembershipErrorCodes(test.err)
			if code != test.wantCode || business != test.wantBusiness {
				t.Errorf("codes = (%s, %s), want (%s, %s)", code, business, test.wantCode, test.wantBusiness)
			}
		})
	}
}

func TestFailUserMembershipOperation(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantCode      response.ErrorCode
		wantBusiness  response.BusinessErrorCode
		wantChallenge string
	}{
		// 缺少身份时只输出基础 Bearer 挑战头，不添加 invalid_token。
		{name: "missing identity", err: httpauthz.ErrAuthenticationRequired, wantStatus: 401, wantCode: response.CodeUnauthenticated, wantChallenge: `Bearer realm="api"`},
		// 包装后的无效身份仍应输出 invalid_token，并原样保留错误消息。
		{name: "invalid identity", err: fmt.Errorf("identity: %w", httpauthz.ErrInvalidMemberIdentity), wantStatus: 401, wantCode: response.CodeUnauthenticated, wantChallenge: `Bearer realm="api", error="invalid_token"`},
		// 访问他人会员数据返回 403 和会员权限业务码，不输出认证挑战头。
		{name: "permission denied", err: errUserMembershipAccessDenied, wantStatus: 403, wantCode: response.CodePermissionDenied, wantBusiness: "USER_MEMBERSHIP_PERMISSION_DENIED"},
		// 用户不存在返回 404，业务码与资料不存在保持一致。
		{name: "user not found", err: userservice.ErrUserProfileNotFound, wantStatus: 404, wantCode: response.CodeNotFound, wantBusiness: "USER_PROFILE_NOT_FOUND"},
		// 无效分页输入返回 400，但 Controller 不自行补造字段明细。
		{name: "invalid argument", err: check.NewViolation("page_size", "outside range"), wantStatus: 400, wantCode: response.CodeInvalidArgument},
		// 内部数据故障返回 500，并保留实际错误消息及 data:null。
		{name: "invalid membership state", err: models.ErrInvalidUserMembershipState, wantStatus: 500, wantCode: response.CodeInternalError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			failUserMembershipOperation(ctx, test.err)
			assertUserControllerFailure(t, ctx, recorder, test.err, test.wantStatus, test.wantCode, test.wantBusiness, test.wantChallenge)
		})
	}
}
