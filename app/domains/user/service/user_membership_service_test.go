package service

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"go-service-template/app/domains/user/models"
	"go-service-template/app/domains/user/tables"
	"go-service-template/internal/check"
)

func TestValidateQueryUserMembershipByUserIDParams(t *testing.T) {
	tests := []struct {
		name    string
		params  QueryUserMembershipByUserIDParams
		wantErr error
	}{
		// 相同正数 ID 表示本人查询，确认用例参数正确接入公共身份校验。
		{name: "self", params: QueryUserMembershipByUserIDParams{OperatorUserID: 12, UserID: 12}},
		// 缺失发起方 ID 应保留输入错误分类，不能绕过身份校验。
		{name: "missing operator", params: QueryUserMembershipByUserIDParams{UserID: 12}, wantErr: check.ErrInvalidValue},
		// 两个合法但不同的 ID 应原样返回本人权限错误。
		{name: "another user", params: QueryUserMembershipByUserIDParams{OperatorUserID: 12, UserID: 13}, wantErr: ErrUserMembershipPermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateQueryUserMembershipByUserIDParams(tt.params); !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateQueryUserMembershipByUserIDParams() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateQueryMembershipChangeRecordPageParams(t *testing.T) {
	createdAt := time.Date(2026, 11, 12, 3, 4, 5, 123000000, time.UTC)
	wantCursor := models.MembershipChangeRecordCursor{UserID: 12, CreatedAt: createdAt, ID: 30}
	token, err := models.EncodeMembershipChangeRecordPageToken(wantCursor)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		params     QueryMembershipChangeRecordPageParams
		wantCursor *models.MembershipChangeRecordCursor
		wantErr    error
	}{
		// 最小数量 1 与空令牌表示合法第一页，不补造游标。
		{name: "minimum first page", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 12, UserID: 12, PageSize: 1}},
		// 最大数量 100 必须被接受，避免将有效上边界错误排除。
		{name: "maximum first page", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 12, UserID: 12, PageSize: 100}},
		// 本人的续页令牌应还原完整位置，供后续查询直接使用。
		{name: "next page", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 12, UserID: 12, PageSize: 20, PageToken: token}, wantCursor: &wantCursor},
		// 显式 0 不能被改成默认数量，应在解析令牌前拒绝。
		{name: "zero page size", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 12, UserID: 12}, wantErr: check.ErrInvalidValue},
		// 101 是上界的相邻非法值，确认 Service 自身限制单次查询数量。
		{name: "oversized page", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 12, UserID: 12, PageSize: 101}, wantErr: check.ErrInvalidValue},
		// 非本人访问同时带非法数量，仍应先返回权限错误而非分页错误。
		{name: "access checked first", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 13, UserID: 12}, wantErr: ErrUserMembershipPermissionDenied},
		// 非法令牌不能当作第一页，保留公共输入错误分类。
		{name: "malformed token", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 12, UserID: 12, PageSize: 20, PageToken: "bad"}, wantErr: check.ErrInvalidValue},
		// 本人查询也不能复用另一用户的令牌，验证令牌绑定目标用户。
		{name: "another users token", params: QueryMembershipChangeRecordPageParams{OperatorUserID: 13, UserID: 13, PageSize: 20, PageToken: token}, wantErr: check.ErrInvalidValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.params
			cursor, err := validateQueryMembershipChangeRecordPageParams(tt.params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateQueryMembershipChangeRecordPageParams() error = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(cursor, tt.wantCursor) {
				t.Errorf("cursor = %#v, want %#v", cursor, tt.wantCursor)
			}
			if tt.params != before {
				t.Fatal("validation modified params")
			}
		})
	}
}

func TestValidateMembershipQueryUserIDs(t *testing.T) {
	tests := []struct {
		name       string
		operatorID int64
		userID     int64
		wantErr    error
	}{
		// 最小正数且身份相同应通过，不额外要求业务数据已存在。
		{name: "self", operatorID: 1, userID: 1},
		// 发起方为 0 时先判定输入无效，不能归类为跨用户访问。
		{name: "zero operator", operatorID: 0, userID: 12, wantErr: check.ErrInvalidValue},
		// 目标为负数时保留输入错误，不能继续比较本人关系。
		{name: "negative user", operatorID: 12, userID: -1, wantErr: check.ErrInvalidValue},
		// 两个正数 ID 不同才属于本人访问限制，返回既定权限错误。
		{name: "different users", operatorID: 12, userID: 13, wantErr: ErrUserMembershipPermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateMembershipQueryUserIDs(tt.operatorID, tt.userID); !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateMembershipQueryUserIDs() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestUserMembershipQueryResult(t *testing.T) {
	effectiveAt := time.Date(2026, 11, 12, 3, 4, 5, 123000000, time.UTC)
	expiredAt := effectiveAt.Add(30 * 24 * time.Hour)
	base := models.UserMembership{ID: 30, UserID: 12, MembershipLevel: 10, EffectiveAt: effectiveAt, ExpiredAt: expiredAt}
	otherUser := base
	otherUser.UserID = 13
	invalidLevel := base
	invalidLevel.MembershipLevel = 20
	tests := []struct {
		name       string
		membership *models.UserMembership
		queriedAt  time.Time
		wantStatus string
		wantErr    error
	}{
		// 无权益记录仍是成功结果，保留目标用户和同一查询时刻，并输出未开通。
		{name: "not opened", queriedAt: effectiveAt, wantStatus: "NOT_OPENED"},
		// 有效权益由领域规则得到 ACTIVE，结果保留原权益对象而不重建或修改。
		{name: "active", membership: &base, queriedAt: effectiveAt, wantStatus: "ACTIVE"},
		// 到期时刻应得到 EXPIRED，同时保留原权益供调用方展示过期信息。
		{name: "expired", membership: &base, queriedAt: expiredAt, wantStatus: "EXPIRED"},
		// 读到其他用户的权益属于内部数据异常，不能组合成当前用户的响应。
		{name: "wrong owner", membership: &otherUser, queriedAt: effectiveAt, wantErr: models.ErrInvalidUserMembershipState},
		// 领域规则拒绝未知等级时，组合函数原样返回该错误且不返回部分结果。
		{name: "invalid stored state", membership: &invalidLevel, queriedAt: effectiveAt, wantErr: models.ErrInvalidUserMembershipState},
		// 即使没有权益，非法查询时刻也不能被当作合法的未开通结果。
		{name: "invalid query time", wantErr: models.ErrInvalidUserMembershipState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before models.UserMembership
			if tt.membership != nil {
				before = *tt.membership
			}
			result, err := userMembershipQueryResult(12, tt.membership, tt.queriedAt)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("userMembershipQueryResult() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if result != nil || errors.Is(err, check.ErrInvalidValue) {
					t.Fatalf("result = %#v, error = %v; want nil result and internal state error", result, err)
				}
			} else if result == nil || result.UserID != 12 || result.MembershipStatus != tt.wantStatus || result.QueriedAt != tt.queriedAt || result.Membership != tt.membership {
				t.Fatalf("result = %#v, want user 12, status %s, unchanged time and membership", result, tt.wantStatus)
			}
			if tt.membership != nil && *tt.membership != before {
				t.Fatal("query result modified membership")
			}
		})
	}
}

func TestMembershipChangeRecordPageResult(t *testing.T) {
	createdAt := time.Date(2026, 11, 12, 3, 4, 5, 123000000, time.UTC)
	first := tables.MembershipChangeRecord{ID: 30, UserID: 12, MembershipID: 3, OrderID: 300, CreatedAt: createdAt}
	second := tables.MembershipChangeRecord{ID: 20, UserID: 12, MembershipID: 3, OrderID: 200, CreatedAt: createdAt}
	probe := tables.MembershipChangeRecord{ID: 10, UserID: 12, MembershipID: 3, OrderID: 100, CreatedAt: createdAt.Add(-time.Millisecond)}
	wrongOwner := first
	wrongOwner.UserID = 13
	invalidID := first
	invalidID.ID = 0
	invalidTime := first
	invalidTime.CreatedAt = time.Time{}
	tests := []struct {
		name        string
		rows        []tables.MembershipChangeRecord
		pageSize    int
		wantIDs     []int64
		wantTokenID int64
		wantErr     error
	}{
		// nil 查询结果应转换为非 nil 空列表，并且没有下一页令牌。
		{name: "nil rows", pageSize: 2, wantIDs: []int64{}},
		// 已分配的空切片也必须维持相同空页语义。
		{name: "empty rows", rows: []tables.MembershipChangeRecord{}, pageSize: 2, wantIDs: []int64{}},
		// 数量少于页大小时完整返回现有记录，不凭空生成续页位置。
		{name: "partial page", rows: []tables.MembershipChangeRecord{first}, pageSize: 2, wantIDs: []int64{30}},
		// 恰好一页没有额外探测行，不能仅因数量达到上限就宣称还有下一页。
		{name: "full final page", rows: []tables.MembershipChangeRecord{first, second}, pageSize: 2, wantIDs: []int64{30, 20}},
		// 多取的一行只用于探测，下一页必须从本页末条 ID 20 而非探测行 ID 10 继续。
		{name: "probe row", rows: []tables.MembershipChangeRecord{first, second, probe}, pageSize: 2, wantIDs: []int64{30, 20}, wantTokenID: 20},
		// 本页末条属于其他用户时不生成令牌，整页以内部数据错误结束。
		{name: "wrong cursor owner", rows: []tables.MembershipChangeRecord{wrongOwner, probe}, pageSize: 1, wantErr: errInvalidMembershipChangeRecordState},
		// 数据库记录 ID 为 0 导致令牌编码失败，应转换成内部数据错误而非请求错误。
		{name: "invalid cursor id", rows: []tables.MembershipChangeRecord{invalidID, probe}, pageSize: 1, wantErr: errInvalidMembershipChangeRecordState},
		// 无法用于数据库游标的日期同样不得输出部分结果或非法令牌。
		{name: "invalid cursor time", rows: []tables.MembershipChangeRecord{invalidTime, probe}, pageSize: 1, wantErr: errInvalidMembershipChangeRecordState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]tables.MembershipChangeRecord(nil), tt.rows...)
			result, err := membershipChangeRecordPageResult(tt.rows, 12, tt.pageSize)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("membershipChangeRecordPageResult() error = %v, want %v", err, tt.wantErr)
			}
			for i := range tt.rows {
				if tt.rows[i] != before[i] {
					t.Fatalf("row %d was modified", i)
				}
			}
			if tt.wantErr != nil {
				if result != nil || errors.Is(err, check.ErrInvalidValue) {
					t.Fatalf("result = %#v, error = %v; want nil result and internal data error", result, err)
				}
				return
			}
			if result == nil || result.Records == nil {
				t.Fatal("successful page must contain a non-nil record slice")
			}
			ids := make([]int64, 0, len(result.Records))
			for _, record := range result.Records {
				ids = append(ids, record.ID)
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Errorf("record IDs = %v, want %v", ids, tt.wantIDs)
			}
			if result.HasMore != (tt.wantTokenID != 0) {
				t.Errorf("HasMore = %v, want %v", result.HasMore, tt.wantTokenID != 0)
			}
			if tt.wantTokenID == 0 {
				if result.NextPageToken != nil {
					t.Fatalf("NextPageToken = %q, want nil", *result.NextPageToken)
				}
				return
			}
			if result.NextPageToken == nil {
				t.Fatal("page with probe row must have a next page token")
			}
			cursor, err := models.ParseMembershipChangeRecordPageToken(*result.NextPageToken, 12)
			if err != nil || cursor == nil || cursor.UserID != 12 || cursor.ID != tt.wantTokenID || cursor.CreatedAt != createdAt {
				t.Fatalf("next cursor = %#v, error = %v; want user 12, ID %d and %v", cursor, err, tt.wantTokenID, createdAt)
			}
		})
	}
}

func TestMembershipModel(t *testing.T) {
	// 非 UTC 的毫秒时间必须保留同一时刻并统一为 UTC，各标识和等级也应原样保留。
	local := time.Date(2026, 11, 12, 11, 4, 5, 123000000, time.FixedZone("UTC+8", 8*60*60))
	utc := time.Date(2026, 11, 12, 3, 4, 5, 123000000, time.UTC)
	row := tables.UserMembership{ID: 30, UserID: 12, MembershipLevel: 10, EffectiveAt: local, ExpiredAt: local.Add(30 * 24 * time.Hour), CreatedAt: local.Add(-time.Hour), UpdatedAt: local.Add(time.Hour)}
	before := row
	want := &models.UserMembership{ID: 30, UserID: 12, MembershipLevel: 10, EffectiveAt: utc, ExpiredAt: utc.Add(30 * 24 * time.Hour), CreatedAt: utc.Add(-time.Hour), UpdatedAt: utc.Add(time.Hour)}
	if got := membershipModel(&row); !reflect.DeepEqual(got, want) {
		t.Fatalf("membershipModel() = %#v, want %#v", got, want)
	}
	if row != before {
		t.Fatal("membershipModel modified the table row")
	}
}

func TestMembershipChangeRecordModel(t *testing.T) {
	local := time.Date(2026, 11, 12, 11, 4, 5, 123000000, time.FixedZone("UTC+8", 8*60*60))
	utc := time.Date(2026, 11, 12, 3, 4, 5, 123000000, time.UTC)
	level := int32(10)
	beforeEffectiveAt, beforeExpiredAt := utc.Add(-30*24*time.Hour), utc
	tests := []struct {
		name            string
		changeType      int32
		beforeLevel     sql.NullInt32
		beforeEffective sql.NullTime
		beforeExpired   sql.NullTime
		wantLevel       *int32
		wantEffectiveAt *time.Time
		wantExpiredAt   *time.Time
	}{
		// 首次开通的 SQL NULL 即使携带旧值也应输出 nil，不泄漏无效的前快照内容。
		{name: "initial opening", changeType: 10, beforeLevel: sql.NullInt32{Int32: 10}, beforeEffective: sql.NullTime{Time: local}, beforeExpired: sql.NullTime{Time: local}},
		// 续费保留完整前快照，将前后时间均转为 UTC，并复制可空值以避免共享可变状态。
		{name: "renewal", changeType: 20, beforeLevel: sql.NullInt32{Int32: 10, Valid: true}, beforeEffective: sql.NullTime{Time: local.Add(-30 * 24 * time.Hour), Valid: true}, beforeExpired: sql.NullTime{Time: local, Valid: true}, wantLevel: &level, wantEffectiveAt: &beforeEffectiveAt, wantExpiredAt: &beforeExpiredAt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := tables.MembershipChangeRecord{
				ID: 30, UserID: 12, MembershipID: 3, OrderID: 300, ChangeType: tt.changeType,
				BeforeMembershipLevel: tt.beforeLevel, BeforeEffectiveAt: tt.beforeEffective, BeforeExpiredAt: tt.beforeExpired,
				AfterMembershipLevel: 10, AfterEffectiveAt: local, AfterExpiredAt: local.Add(30 * 24 * time.Hour), CreatedAt: local.Add(time.Hour),
			}
			before := row
			want := &models.MembershipChangeRecord{
				ID: 30, UserID: 12, MembershipID: 3, OrderID: 300, ChangeType: tt.changeType,
				BeforeMembershipLevel: tt.wantLevel, BeforeEffectiveAt: tt.wantEffectiveAt, BeforeExpiredAt: tt.wantExpiredAt,
				AfterMembershipLevel: 10, AfterEffectiveAt: utc, AfterExpiredAt: utc.Add(30 * 24 * time.Hour), CreatedAt: utc.Add(time.Hour),
			}
			got := membershipChangeRecordModel(&row)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("membershipChangeRecordModel() = %#v, want %#v", got, want)
			}
			if got.BeforeMembershipLevel != nil {
				*got.BeforeMembershipLevel = 20
			}
			if got.BeforeEffectiveAt != nil {
				*got.BeforeEffectiveAt = time.Time{}
			}
			if got.BeforeExpiredAt != nil {
				*got.BeforeExpiredAt = time.Time{}
			}
			if row != before {
				t.Fatal("conversion or mutation of returned snapshot modified the table row")
			}
		})
	}
}
