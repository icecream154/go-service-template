package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/app/domains/user/models"
	userservice "go-service-template/app/domains/user/service"
	httpauthz "go-service-template/app/http/authz"
	httpmiddleware "go-service-template/app/http/middleware"
	"go-service-template/app/identity"
	"go-service-template/env"
	"go-service-template/internal/authn"
	"go-service-template/internal/check"
	"go-service-template/internal/logger"
	"go-service-template/internal/response"
	"go-service-template/internal/tracing"

	"github.com/gin-gonic/gin"
)

// TestMain 按进程生命周期装配共享依赖，允许 -count 多次运行同一测试。
func TestMain(m *testing.M) {
	os.Exit(runMembershipControllerTests(m))
}

type membershipAccessCase struct {
	name          string
	principal     authn.Principal
	anonymous     bool
	wantStatus    int
	wantCode      response.ErrorCode
	wantChallenge string
}

func TestUserMembershipControllerRejectsUnauthorizedQueries(t *testing.T) {
	tests := []membershipAccessCase{
		// 匿名请求不能读取权益，必须返回标准的认证挑战。
		{name: "anonymous", anonymous: true, wantStatus: 401, wantCode: response.CodeUnauthenticated, wantChallenge: `Bearer realm="api"`},
		// 管理服务不能借用本次仅面向本人开放的两个查询接口。
		{name: "admin service", principal: authn.Principal{Kind: authn.PrincipalKindService, Namespace: identity.ServiceNamespaceAdmin, SubjectID: "admin-service"}, wantStatus: 403, wantCode: response.CodePermissionDenied},
		// 同为用户身份但命名空间不同，不能解释为本项目的普通用户。
		{name: "other user namespace", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: "staff", SubjectID: "123"}, wantStatus: 403, wantCode: response.CodePermissionDenied},
		// 用户身份已认证但主体不是正 int64，返回凭证错误而不是请求参数错误。
		{name: "invalid member ID", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: identity.UserNamespaceMember, SubjectID: "not-a-number"}, wantStatus: 401, wantCode: response.CodeUnauthenticated, wantChallenge: `Bearer realm="api", error="invalid_token"`},
		// 当前用户为 124，请求目标为 123，两个接口都必须在访问 Service 前拒绝。
		{name: "another member", principal: authn.Principal{Kind: authn.PrincipalKindUser, Namespace: identity.UserNamespaceMember, SubjectID: "124"}, wantStatus: 403, wantCode: response.CodePermissionDenied},
	}
	controller := NewUserMembershipController()
	operations := []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{name: "current membership", handler: func(ctx *gin.Context) {
			controller.QueryUserMembershipByUserID(ctx, &usermembershipdto.QueryUserMembershipByUserIDRequest{UserID: "123"})
		}},
		{name: "change history", handler: func(ctx *gin.Context) {
			controller.QueryMembershipChangeRecordPage(ctx, &usermembershipdto.QueryMembershipChangeRecordPageRequest{UserID: "123", PageSize: 20})
		}},
	}
	for _, operation := range operations {
		for _, test := range tests {
			t.Run(operation.name+"/"+test.name, func(t *testing.T) {
				engine := gin.New()
				engine.Use(httpmiddleware.AuthenticateIfPresent(membershipTestAuthenticator{principal: test.principal}))
				engine.GET("/query", operation.handler)
				request := httptest.NewRequest(http.MethodGet, "/query", nil)
				if !test.anonymous {
					request.Header.Set("Authorization", "Bearer fixture")
				}
				recorder := httptest.NewRecorder()
				// 不初始化 composition 或数据库；若拒绝路径误调用 Service，测试会直接失败。
				engine.ServeHTTP(recorder, request)
				var payload response.Response[any]
				if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if recorder.Code != test.wantStatus || payload.Data != nil || payload.Error == nil || payload.Error.Code != test.wantCode {
					t.Fatalf("response = %d %s, want %d %s", recorder.Code, recorder.Body, test.wantStatus, test.wantCode)
				}
				if got := recorder.Header().Get("WWW-Authenticate"); got != test.wantChallenge {
					t.Errorf("WWW-Authenticate = %q, want %q", got, test.wantChallenge)
				}
				if test.wantStatus == 403 && payload.Error.BusinessCode != "USER_MEMBERSHIP_PERMISSION_DENIED" {
					t.Errorf("business code = %q", payload.Error.BusinessCode)
				}
			})
		}
	}
}

func TestUserMembershipDTO(t *testing.T) {
	// 未开通没有权益对象，不能返回填有零值的会员结构。
	if got := userMembershipDTO(nil); got != nil {
		t.Fatalf("userMembershipDTO(nil) = %+v, want nil", got)
	}
	// 已过期记录的原始等级和期限继续输出，大整数 ID 不损失精度。
	effective := time.Date(2026, 10, 1, 0, 0, 0, 123000000, time.UTC)
	expired := effective.Add(30 * 24 * time.Hour)
	got := userMembershipDTO(&models.UserMembership{ID: 9007199254740993, UserID: 123, MembershipLevel: 10, EffectiveAt: effective, ExpiredAt: expired})
	if got.MembershipID != 9007199254740993 || got.MembershipLevel != 10 || !got.EffectiveAt.Equal(effective) || !got.ExpiredAt.Equal(expired) {
		t.Fatalf("membership DTO = %+v", got)
	}
}

func TestMembershipChangeRecordDTOList(t *testing.T) {
	// 空历史必须编码为 []，不能编码为 null。
	empty, err := json.Marshal(membershipChangeRecordDTOList(nil))
	if err != nil || string(empty) != "[]" {
		t.Fatalf("empty history = %s, %v", empty, err)
	}
	// 首购保留 null 前快照，续费保留真实前后快照，而不以当前权益覆盖历史。
	before := time.Date(2026, 11, 1, 0, 0, 0, 123000000, time.UTC)
	after := before.Add(30 * 24 * time.Hour)
	level := int32(10)
	got := membershipChangeRecordDTOList([]*models.MembershipChangeRecord{
		{ID: 3, MembershipID: 2, OrderID: 1, ChangeType: 10, AfterMembershipLevel: 10, AfterEffectiveAt: before, AfterExpiredAt: after, CreatedAt: before},
		{ID: 6, MembershipID: 2, OrderID: 5, ChangeType: 20, BeforeMembershipLevel: &level, BeforeEffectiveAt: &before, BeforeExpiredAt: &after, AfterMembershipLevel: 10, AfterEffectiveAt: before, AfterExpiredAt: after.Add(30 * 24 * time.Hour), CreatedAt: after},
	})
	if len(got) != 2 || got[0].BeforeMembershipLevel != nil || got[0].BeforeEffectiveAt != nil || got[0].BeforeExpiredAt != nil {
		t.Fatalf("first activation snapshot = %+v", got)
	}
	if got[1].RecordID != 6 || got[1].MembershipID != 2 || got[1].OrderID != 5 || got[1].ChangeType != 20 ||
		got[1].BeforeMembershipLevel == nil || *got[1].BeforeMembershipLevel != 10 ||
		!got[1].BeforeEffectiveAt.Equal(before) || !got[1].BeforeExpiredAt.Equal(after) ||
		got[1].AfterMembershipLevel != 10 || !got[1].AfterEffectiveAt.Equal(before) ||
		!got[1].AfterExpiredAt.Equal(after.Add(30*24*time.Hour)) || !got[1].CreatedAt.Equal(after) {
		t.Fatalf("renewal snapshot = %+v", got[1])
	}
}

func TestMapUserMembershipErrorCodes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     response.ErrorCode
		business response.BusinessErrorCode
	}{
		// 身份缺失属于认证失败，不附业务码。
		{"authentication", httpauthz.ErrAuthenticationRequired, response.CodeUnauthenticated, ""},
		// Service 保留的本人约束映射为稳定的会员权限错误。
		{"permission", userservice.ErrUserMembershipPermissionDenied, response.CodePermissionDenied, "USER_MEMBERSHIP_PERMISSION_DENIED"},
		// 用户不存在与从未开通会员不同，前者返回 404。
		{"missing user", userservice.ErrUserProfileNotFound, response.CodeNotFound, "USER_PROFILE_NOT_FOUND"},
		// 调用参数违反公共规则时，保持 400 分类。
		{"invalid value", &check.Violation{Rule: "range", Reason: "outside range"}, response.CodeInvalidArgument, ""},
		// 无效规则是程序问题，不能要求客户端修改参数。
		{"invalid rule", &check.RuleError{Rule: "range", Reason: "invalid bounds"}, response.CodeInternalError, ""},
		// 已保存权益异常必须返回 500，不能误报为查询参数非法。
		{"stored state", models.ErrInvalidUserMembershipState, response.CodeInternalError, ""},
		// 未知技术故障不伪装成用户不存在或未开通。
		{"unknown failure", errors.New("query failed"), response.CodeInternalError, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, business := mapUserMembershipErrorCodes(test.err)
			if code != test.code || business != test.business {
				t.Errorf("codes = %s/%s, want %s/%s", code, business, test.code, test.business)
			}
		})
	}
}

// membershipTestAuthenticator 通过正式认证中间件提供内存身份，不访问私有 Context 键。
type membershipTestAuthenticator struct {
	principal authn.Principal
}

func (a membershipTestAuthenticator) Authenticate(context.Context, authn.Credential) (authn.Principal, error) {
	return a.principal, nil
}

// runMembershipControllerTests 仅装配日志和追踪，不初始化数据库或 Service 集合。
func runMembershipControllerTests(m *testing.M) int {
	endpoint, hadEndpoint := os.LookupEnv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err := os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if hadEndpoint {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpoint)
		} else {
			_ = os.Unsetenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
		}
	}()
	dir, err := os.MkdirTemp("", "membership-controller-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "test.yml"), []byte("service_name: membership-test\nmin_level: ERROR\n"), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	deployment := env.Config{Environment: env.TestEnv}
	if err := logger.Init(dir, deployment); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	config := logger.Current().Config()
	if err := tracing.Init(context.Background(), config.ServiceName, config.InstanceID, deployment); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = logger.Shutdown()
		return 1
	}
	result := m.Run()
	if err := tracing.Shutdown(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		result = 1
	}
	if err := logger.Shutdown(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		result = 1
	}
	return result
}
