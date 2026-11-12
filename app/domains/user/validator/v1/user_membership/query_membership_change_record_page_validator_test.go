package usermembership

import (
	"net/http"
	"testing"
	"time"

	usermembershipdto "go-service-template/app/domains/user/dto/v1/user_membership"
	"go-service-template/app/domains/user/models"
)

func TestQueryMembershipChangeRecordPageRejectsBadRequest(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFields []string
	}{
		// page_size 类型错误先触发绑定失败，不返回其他字段的明细。
		{name: "binding failure", query: "page_size=one"},
		// 省略 page_size 时默认数量必须可用，只报告缺失的用户 ID。
		{name: "omitted page size uses default", query: "", wantFields: []string{"user_id"}},
		// 空文本按绑定规则成为零，不能在绑定后重新补默认值。
		{name: "empty page size", query: "user_id=1&page_size=", wantFields: []string{"page_size"}},
		// 显式零值与非法令牌分别汇总，顺序与 DTO 声明一致。
		{name: "zero size and invalid token", query: "user_id=1&page_size=0&page_token=invalid", wantFields: []string{"page_size", "page_token"}},
		// 无效目标不参与令牌用户绑定检查，独立的 page_size 仍须校验。
		{name: "invalid ID skips token dependency", query: "user_id=0&page_size=101&page_token=invalid", wantFields: []string{"user_id", "page_size"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newMembershipValidatorRequest(http.MethodGet, "/?"+test.query)
			NewQueryMembershipChangeRecordPageValidator(nil).QueryMembershipChangeRecordPage(ctx)
			assertMembershipValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

func TestValidateQueryMembershipChangeRecordPageRequest(t *testing.T) {
	token, err := models.EncodeMembershipChangeRecordPageToken(models.MembershipChangeRecordCursor{
		UserID: 42, ID: 91, CreatedAt: time.Date(2026, 11, 12, 2, 0, 0, 123000000, time.UTC),
	})
	if err != nil {
		t.Fatalf("encode valid page token: %v", err)
	}
	tests := []struct {
		name       string
		userID     string
		pageSize   int
		pageToken  string
		wantFields []string
	}{
		// 每页最小值和空令牌表示合法的第一页。
		{name: "first page minimum", userID: "42", pageSize: 1},
		// 每页最大值配合有效令牌可继续翻页，数字等值的 ID 仍匹配绑定。
		{name: "next page maximum", userID: "+00042", pageSize: 100, pageToken: token},
		// 超过每页上界的输入必须报告 page_size。
		{name: "above maximum", userID: "42", pageSize: 101, wantFields: []string{"page_size"}},
		// 令牌只能用于原目标用户，跨用户复用属于 page_token 输入错误。
		{name: "token belongs to another user", userID: "43", pageSize: 20, pageToken: token, wantFields: []string{"page_token"}},
		// 非法令牌通过领域规则映射到其字段，而不误报用户不存在。
		{name: "malformed token", userID: "42", pageSize: 20, pageToken: "invalid", wantFields: []string{"page_token"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := usermembershipdto.QueryMembershipChangeRecordPageRequest{
				UserID: test.userID, PageSize: test.pageSize, PageToken: test.pageToken,
			}
			want := request
			details, err := validateQueryMembershipChangeRecordPageRequest(&request)
			if err != nil {
				t.Fatalf("validateQueryMembershipChangeRecordPageRequest() error = %v", err)
			}
			assertMembershipValidatorFields(t, details, test.wantFields...)
			if request != want {
				t.Errorf("validated request = %#v, want unchanged %#v", request, want)
			}
		})
	}
}
