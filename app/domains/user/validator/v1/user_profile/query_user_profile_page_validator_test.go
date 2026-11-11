package userprofile

import (
	"fmt"
	"math"
	"net/http"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
)

type queryUserProfilePageRejectionCase struct {
	name       string
	query      string
	wantFields []string
}

func TestQueryUserProfilePageRejectsBadRequest(t *testing.T) {
	// 每页默认 20 条时，此页码的偏移仍可用 int 表示，下一页会超出范围。
	lastDefaultPage := math.MaxInt/20 + 1
	tests := []queryUserProfilePageRejectionCase{
		// 分页字段无法绑定为整数时，应在字段校验前返回没有明细的 400。
		{name: "binding failure", query: "page_number=one&is_enabled=1"},
		// 省略两个分页参数并传入空白昵称，验证默认分页参数可用，只有昵称产生明细。
		{name: "omitted pagination uses defaults", query: "user_name=%20", wantFields: []string{"user_name"}},
		// 省略 page_size 并使用默认大小的最后一个可用页码，验证只拒绝非法筛选值。
		{
			name:       "last page with default size",
			query:      fmt.Sprintf("page_number=%d&is_enabled=1", lastDefaultPage),
			wantFields: []string{"is_enabled"},
		},
		// 页码增加一位而仍省略 page_size，验证默认大小参与偏移检查。
		{
			name:       "page offset exceeds range with default size",
			query:      fmt.Sprintf("page_number=%d", lastDefaultPage+1),
			wantFields: []string{"page_number"},
		},
		// 显式零值覆盖默认值，验证四个失败字段按请求字段顺序汇总。
		{
			name:       "explicit zero pagination and invalid filters",
			query:      "page_number=0&page_size=0&user_name=%20&is_enabled=TRUE",
			wantFields: []string{"page_number", "page_size", "user_name", "is_enabled"},
		},
		// Query 显式给空的 is_enabled，验证该输入仍参与校验，区别于省略筛选条件。
		{name: "empty enabled filter", query: "is_enabled=", wantFields: []string{"is_enabled"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newProfileValidatorRequest(http.MethodGet, "/?"+test.query, "")
			validator := NewQueryUserProfilePageValidator(nil)
			validator.QueryUserProfilePage(ctx)
			assertProfileValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

type queryUserProfilePageValidationCase struct {
	name       string
	pageNumber int
	pageSize   int
	userName   string
	isEnabled  *string
	wantFields []string
}

func TestValidateQueryUserProfilePageRequest(t *testing.T) {
	lastPageWithMaxSize := math.MaxInt/100 + 1
	tests := []queryUserProfilePageValidationCase{
		// 页码和每页数量均为下界，且两个筛选项均省略，验证最小分页可用。
		{name: "minimum pagination without filters", pageNumber: 1, pageSize: 1},
		// 每页数量等于上界，验证合法范围包含 100。
		{name: "maximum page size", pageNumber: 1, pageSize: 100},
		// 页码比下界少一，验证零页码定位为 page_number。
		{name: "zero page number", pageNumber: 0, pageSize: 20, wantFields: []string{"page_number"}},
		// 以最大 int 页码搭配零页大小，验证只报告 page_size，偏移检查避开非法除数。
		{name: "zero page size skips offset calculation", pageNumber: math.MaxInt, pageSize: 0, wantFields: []string{"page_size"}},
		// 每页数量刚超过 100，验证越界定位为 page_size。
		{name: "page size above maximum", pageNumber: 1, pageSize: 101, wantFields: []string{"page_size"}},
		// 取每页 100 条时的最后一个可表示页码，验证偏移上界内的请求通过。
		{name: "last representable offset", pageNumber: lastPageWithMaxSize, pageSize: 100},
		// 页码比最后一个可表示页码大一，验证偏移越界定位为 page_number。
		{name: "offset overflow", pageNumber: lastPageWithMaxSize + 1, pageSize: 100, wantFields: []string{"page_number"}},
		// 显式 true 搭配带空格的非空昵称，验证可选筛选值合法且保留原文。
		{name: "true filter with a nickname", pageNumber: 2, pageSize: 20, userName: " 小明 ", isEnabled: new("true")},
		// 显式 false 表示停用筛选，验证它是有效输入。
		{name: "false filter", pageNumber: 1, pageSize: 20, isEnabled: new("false")},
		// 昵称筛选已提供但只有空白，验证领域规则的失败对应 user_name。
		{name: "blank nickname filter", pageNumber: 1, pageSize: 20, userName: "　", wantFields: []string{"user_name"}},
		// 以数字文本代替布尔文本，验证明细定位到 is_enabled。
		{name: "numeric enabled filter", pageNumber: 1, pageSize: 20, isEnabled: new("1"), wantFields: []string{"is_enabled"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := userprofiledto.QueryUserProfilePageRequest{
				PageNumber: test.pageNumber,
				PageSize:   test.pageSize,
				UserName:   test.userName,
				IsEnabled:  test.isEnabled,
			}
			var wantEnabled string
			if test.isEnabled != nil {
				wantEnabled = *test.isEnabled
			}

			details, err := validateQueryUserProfilePageRequest(&request)
			if err != nil {
				t.Fatalf("validateQueryUserProfilePageRequest() error = %v, want nil", err)
			}
			assertProfileValidatorFields(t, details, test.wantFields...)
			if request.PageNumber != test.pageNumber || request.PageSize != test.pageSize ||
				request.UserName != test.userName || request.IsEnabled != test.isEnabled {
				t.Error("validateQueryUserProfilePageRequest() modified the request")
			}
			if request.IsEnabled != nil && *request.IsEnabled != wantEnabled {
				t.Errorf("is_enabled = %q after validation, want %q", *request.IsEnabled, wantEnabled)
			}
		})
	}
}
