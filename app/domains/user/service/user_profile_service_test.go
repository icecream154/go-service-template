package service

import (
	"database/sql"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"go-service-template/app/domains/user/models"
	"go-service-template/app/domains/user/tables"
	"go-service-template/internal/check"
)

func TestValidateQueryUserProfileByUserIDParams(t *testing.T) {
	tests := []struct {
		name     string
		params   QueryUserProfileByUserIDParams
		wantRule string
	}{
		// 最小正整数作为双方标识，验证纯输入检查允许有效 ID。
		{name: "positive IDs", params: QueryUserProfileByUserIDParams{OperatorUserID: 1, UserID: 1}},
		// 两个合法 ID 不相同，验证参数检查不提前承担公开用例中的本人权限判断。
		{name: "different positive IDs", params: QueryUserProfileByUserIDParams{OperatorUserID: 1, UserID: 2}},
		// 发起方为零时即拒绝，验证可信入口也必须提供正数身份。
		{name: "missing operator", params: QueryUserProfileByUserIDParams{UserID: 1}, wantRule: "integer.minimum"},
		// 发起方有效而目标为负数，验证目标标识自身的约束仍会执行。
		{name: "negative target", params: QueryUserProfileByUserIDParams{OperatorUserID: 1, UserID: -1}, wantRule: "integer.minimum"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertProfileParamsRule(t, validateQueryUserProfileByUserIDParams(test.params), test.wantRule)
		})
	}
}

func TestValidateQueryUserProfilePageParams(t *testing.T) {
	disabled := false
	tests := []struct {
		name     string
		params   QueryUserProfilePageParams
		wantRule string
	}{
		// 第一页且每页一条验证两个下限；空昵称和 nil 状态均表示未设置筛选。
		{name: "minimum page", params: QueryUserProfilePageParams{PageNumber: 1, PageSize: 1}},
		// 每页 100 条验证包含上限，带空格的有效昵称及显式 false 应保持原值。
		{name: "maximum size with filters", params: QueryUserProfilePageParams{PageNumber: 2, PageSize: 100, UserName: " 小明 ", IsEnabled: &disabled}},
		// 显式零条数应拒绝而非补默认值，也避免随后发生除零。
		{name: "zero page size", params: QueryUserProfilePageParams{PageNumber: 1}, wantRule: "integer.range"},
		// 101 比协议上限多一条，验证不能越过单页数量限制。
		{name: "page size above maximum", params: QueryUserProfilePageParams{PageNumber: 1, PageSize: 101}, wantRule: "integer.range"},
		// 有效条数配零页码，验证页码不会被自动改成第一页。
		{name: "zero page number", params: QueryUserProfilePageParams{PageSize: 20}, wantRule: "integer.minimum"},
		// 最小平台整数页码必须先被拒绝，不能在减一时溢出后继续计算。
		{name: "minimum integer page number", params: QueryUserProfilePageParams{PageNumber: math.MinInt, PageSize: 20}, wantRule: "integer.minimum"},
		// 偏移为 int 可容纳的最大偶数时合法，验证乘法溢出检查的有效边界。
		{name: "largest safe offset", params: QueryUserProfilePageParams{PageNumber: math.MaxInt/2 + 1, PageSize: 2}},
		// 再增加一页会使偏移超过 int，验证在实际查询前拒绝溢出风险。
		{name: "offset overflow", params: QueryUserProfilePageParams{PageNumber: math.MaxInt/2 + 2, PageSize: 2}, wantRule: "integer.maximum"},
		// 非空但全为空白的筛选昵称不能被当作省略，验证领域规则确实接入。
		{name: "blank name filter", params: QueryUserProfilePageParams{PageNumber: 1, PageSize: 20, UserName: " \u3000"}, wantRule: "not_blank"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.params
			assertProfileParamsRule(t, validateQueryUserProfilePageParams(test.params), test.wantRule)
			if !reflect.DeepEqual(test.params, before) || disabled {
				t.Errorf("parameters changed: got %+v, want %+v; disabled = %v", test.params, before, disabled)
			}
		})
	}
}

func TestValidateCreateUserParams(t *testing.T) {
	tests := []struct {
		name     string
		params   CreateUserParams
		wantRule string
	}{
		// 省略头像允许首次注册，并保留来源、账户和昵称的原始空格及前导零。
		{name: "omitted avatar", params: CreateUserParams{AccountSource: " Provider ", ExternalAccountID: "001", UserName: " 小明 "}},
		// 显式空字符串也是创建时的合法头像输入，不能改成 nil。
		{name: "empty avatar", params: CreateUserParams{AccountSource: "provider", ExternalAccountID: "001", UserName: "小明", AvatarURL: profileTestString("")}},
		// 非空 HTTPS 头像应通过领域规则，验证可选字段提供值后的成功路径。
		{name: "provided avatar", params: CreateUserParams{AccountSource: "provider", ExternalAccountID: "001", UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}},
		// 空来源违反必填规则，验证来源校验在注册输入中生效。
		{name: "missing source", params: CreateUserParams{ExternalAccountID: "001", UserName: "小明"}, wantRule: "not_blank"},
		// 有效来源搭配空外部账户，验证不是只检查来源字段。
		{name: "missing account", params: CreateUserParams{AccountSource: "provider", UserName: "小明"}, wantRule: "not_blank"},
		// 来源和账户有效但昵称纯空白，验证展示昵称的领域校验会阻止创建。
		{name: "blank user name", params: CreateUserParams{AccountSource: "provider", ExternalAccountID: "001", UserName: "\t"}, wantRule: "not_blank"},
		// 空白头像不同于空字符串，验证提供的非空值必须满足地址规则。
		{name: "blank avatar", params: CreateUserParams{AccountSource: "provider", ExternalAccountID: "001", UserName: "小明", AvatarURL: profileTestString(" ")}, wantRule: "not_blank"},
		// 相对路径不能作为头像地址，验证 URL 领域检查的错误类型原样保留。
		{name: "relative avatar", params: CreateUserParams{AccountSource: "provider", ExternalAccountID: "001", UserName: "小明", AvatarURL: profileTestString("/avatar.png")}, wantRule: "absolute_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.params
			if before.AvatarURL != nil {
				before.AvatarURL = profileTestString(*before.AvatarURL)
			}
			assertProfileParamsRule(t, validateCreateUserParams(test.params), test.wantRule)
			if !reflect.DeepEqual(test.params, before) {
				t.Errorf("parameters changed: got %+v, want %+v", test.params, before)
			}
		})
	}
}

func TestValidateGenerateUserAvatarParams(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		wantRule string
	}{
		// 文本两侧可保留空格，验证这里只检查是否有内容，不改写提示词。
		{name: "nonblank prompt", prompt: " 一只猫的头像 "},
		// 空提示词应在发起任何生成之前被参数检查拒绝。
		{name: "empty prompt", wantRule: "not_blank"},
		// 全角空格和换行也是空白，验证非空字符串不一定有效。
		{name: "Unicode whitespace prompt", prompt: "\u3000\n", wantRule: "not_blank"},
		// 有内容但含非法 UTF-8 字节，验证编码错误保留自己的规则类别。
		{name: "invalid UTF-8 prompt", prompt: "猫\xff", wantRule: "valid_utf8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := GenerateUserAvatarParams{Prompt: test.prompt}
			assertProfileParamsRule(t, validateGenerateUserAvatarParams(params), test.wantRule)
			if params.Prompt != test.prompt {
				t.Errorf("prompt = %q, want unchanged %q", params.Prompt, test.prompt)
			}
		})
	}
}

func TestValidateUpdateUserProfileParams(t *testing.T) {
	tests := []struct {
		name     string
		params   UpdateUserProfileParams
		wantRule string
	}{
		// 初始版本 0 可更新，验证版本下限及完整替换字段都有效。
		{name: "initial version", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, ExpectedVersion: 0, UserName: " 小明 ", AvatarURL: profileTestString("https://images.example.test/avatar.png")}},
		// MaxInt64-1 仍为成功后的版本递增留有空间，验证上限包含等号。
		{name: "largest incrementable version", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, ExpectedVersion: math.MaxInt64 - 1, UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}},
		// 双方 ID 不同但均合法，验证纯输入检查不重复公开用例的权限判断。
		{name: "different positive IDs", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 2, UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}},
		// 发起方为零必须拒绝，不能把缺省身份交给后续业务处理。
		{name: "missing operator", params: UpdateUserProfileParams{UserID: 1, UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}, wantRule: "integer.minimum"},
		// 发起方有效而目标为零，验证目标 ID 的检查不会遗漏。
		{name: "missing target", params: UpdateUserProfileParams{OperatorUserID: 1, UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}, wantRule: "integer.minimum"},
		// -1 低于初始版本，验证负版本不能进入并发控制流程。
		{name: "negative version", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, ExpectedVersion: -1, UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}, wantRule: "integer.range"},
		// MaxInt64 无法再递增，验证在更新之前拒绝该版本。
		{name: "unincrementable version", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, ExpectedVersion: math.MaxInt64, UserName: "小明", AvatarURL: profileTestString("https://images.example.test/avatar.png")}, wantRule: "integer.range"},
		// 昵称是完整替换字段，验证不能通过省略昵称保留原值。
		{name: "missing user name", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, AvatarURL: profileTestString("https://images.example.test/avatar.png")}, wantRule: "not_blank"},
		// nil 头像表示未提供，更新用例必须以必填错误拒绝且不解引用空指针。
		{name: "missing avatar", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, UserName: "小明"}, wantRule: "relation.not_nil"},
		// 更新不支持清空头像，验证创建时允许的空字符串在这里被拒绝。
		{name: "empty avatar", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, UserName: "小明", AvatarURL: profileTestString("")}, wantRule: "not_blank"},
		// 非空相对路径仍无效，验证更新路径接入头像 URL 领域规则。
		{name: "relative avatar", params: UpdateUserProfileParams{OperatorUserID: 1, UserID: 1, UserName: "小明", AvatarURL: profileTestString("/avatar.png")}, wantRule: "absolute_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.params
			if before.AvatarURL != nil {
				before.AvatarURL = profileTestString(*before.AvatarURL)
			}
			assertProfileParamsRule(t, validateUpdateUserProfileParams(test.params), test.wantRule)
			if !reflect.DeepEqual(test.params, before) {
				t.Errorf("parameters changed: got %+v, want %+v", test.params, before)
			}
		})
	}
}

func TestValidateBatchQueryUserProfilesParams(t *testing.T) {
	tests := []struct {
		name        string
		ids         []int64
		wantRule    string
		wantIndexed bool
		wantIndex   int
	}{
		// 一条记录达到最小数量，验证合法批量请求的下限。
		{name: "single ID", ids: []int64{1}},
		// 重复且乱序的合法 ID 应通过，验证校验阶段不去重、不排序。
		{name: "duplicate IDs preserve order", ids: []int64{3, 1, 3}},
		// 100 个重复 ID 达到数量上限，验证数量按提交条数计算且允许重复。
		{name: "maximum submitted count", ids: slices.Repeat([]int64{7}, 100)},
		// nil 没有任何目标，验证缺省集合触发数量错误。
		{name: "nil IDs", wantRule: "collection.length"},
		// 非 nil 空集合也没有目标，验证不依赖指针是否为空来判断数量。
		{name: "empty IDs", ids: []int64{}, wantRule: "collection.length"},
		// 101 个重复 ID 去重后虽只有一个，仍必须按原始提交数量拒绝。
		{name: "count before deduplication", ids: slices.Repeat([]int64{7}, 101), wantRule: "collection.length"},
		// 第二个元素为零，验证逐项检查保留原始位置及内部错误类别。
		{name: "invalid second ID", ids: []int64{3, 0, 1}, wantRule: "integer.minimum", wantIndexed: true, wantIndex: 1},
		// 第一项已为负数，后面还有错误时应报告首次失败的原始下标。
		{name: "first invalid ID wins", ids: []int64{-1, 0}, wantRule: "integer.minimum", wantIndexed: true, wantIndex: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.ids)
			err := validateBatchQueryUserProfilesParams(BatchQueryUserProfilesParams{UserIDs: test.ids})
			assertProfileParamsRule(t, err, test.wantRule)
			var indexed *check.IndexError
			if gotIndexed := errors.As(err, &indexed); gotIndexed != test.wantIndexed {
				t.Fatalf("indexed error = %v, want %v: %v", gotIndexed, test.wantIndexed, err)
			}
			if test.wantIndexed && indexed.Index != test.wantIndex {
				t.Errorf("error index = %d, want %d", indexed.Index, test.wantIndex)
			}
			if !reflect.DeepEqual(test.ids, before) {
				t.Errorf("IDs changed: got %v, want %v", test.ids, before)
			}
		})
	}
}

func TestProfileModel(t *testing.T) {
	tests := []struct {
		name       string
		avatar     sql.NullString
		wantAvatar *string
	}{
		// SQL NULL 即使含有残留字符串也表示未提供头像，模型中必须是 nil。
		{name: "SQL NULL avatar", avatar: sql.NullString{String: "ignored"}},
		// 有效的空字符串与 SQL NULL 不同，模型必须保留非 nil 的空值。
		{name: "empty avatar", avatar: sql.NullString{Valid: true}, wantAvatar: profileTestString("")},
		// 有效头像需要复制到独立指针，改变模型或原始行都不能相互影响。
		{name: "copied avatar", avatar: sql.NullString{String: "https://images.example.test/avatar.png", Valid: true}, wantAvatar: profileTestString("https://images.example.test/avatar.png")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			createdAt := time.Date(2026, 11, 12, 8, 0, 0, 123000000, time.FixedZone("UTC+8", 8*60*60))
			updatedAt := createdAt.Add(time.Hour)
			row := tables.UserProfile{
				ID: 42, AccountSource: " Provider ", ExternalAccountID: "001", UserName: " 小明 ",
				AvatarURL: test.avatar, IsEnabled: false, Version: 7, CreatedAt: createdAt, UpdatedAt: updatedAt,
			}
			before := row
			want := &models.UserProfile{
				ID: 42, AccountSource: " Provider ", ExternalAccountID: "001", UserName: " 小明 ",
				AvatarURL: test.wantAvatar, IsEnabled: false, Version: 7, CreatedAt: createdAt, UpdatedAt: updatedAt,
			}
			got := profileModel(&row)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("profile = %+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(row, before) {
				t.Errorf("source row changed: got %+v, want %+v", row, before)
			}
			if got.AvatarURL != nil {
				if got.AvatarURL == &row.AvatarURL.String {
					t.Fatal("avatar pointer aliases the source row")
				}
				*got.AvatarURL = "model-only change"
				if row.AvatarURL != test.avatar {
					t.Errorf("source avatar changed with model: got %+v, want %+v", row.AvatarURL, test.avatar)
				}
				row.AvatarURL.String = "row-only change"
				if *got.AvatarURL != "model-only change" {
					t.Errorf("model avatar changed with row: got %q", *got.AvatarURL)
				}
			}
		})
	}
}

func assertProfileParamsRule(t *testing.T, err error, wantRule string) {
	t.Helper()
	if wantRule == "" {
		if err != nil {
			t.Fatalf("validation error = %v, want nil", err)
		}
		return
	}
	if !errors.Is(err, check.ErrInvalidValue) {
		t.Fatalf("validation error = %v, want ErrInvalidValue", err)
	}
	var violation *check.Violation
	if !errors.As(err, &violation) || violation.Rule != wantRule {
		t.Fatalf("validation error = %v, want violation rule %q", err, wantRule)
	}
}

func profileTestString(value string) *string {
	return &value
}
