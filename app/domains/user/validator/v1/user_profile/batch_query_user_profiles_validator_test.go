package userprofile

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	userprofiledto "go-service-template/app/domains/user/dto/v1/user_profile"
)

type batchQueryUserProfilesHTTPCase struct {
	name       string
	body       string
	wantFields []string
}

func TestBatchQueryUserProfilesBadRequest(t *testing.T) {
	tests := []batchQueryUserProfilesHTTPCase{
		// 截断数组破坏 JSON 语法，验证绑定失败返回 400 且省略字段明细。
		{name: "malformed JSON", body: `{"user_id_list":[`},
		// JSON 数字无法绑定到字符串列表，验证元素类型错误同样在绑定阶段返回 400。
		{name: "number in string list", body: `{"user_id_list":[1]}`},
		// 请求缺少列表，验证可以绑定但校验失败，错误定位到 user_id_list。
		{name: "missing list", body: `{}`, wantFields: []string{"user_id_list"}},
		// 显式 null 绑定后的列表长度为零，验证它同样违反数量下限。
		{name: "null list", body: `{"user_id_list":null}`, wantFields: []string{"user_id_list"}},
		// 首项合法而次项文本无效，验证元素校验失败仍只返回一条列表字段明细。
		{name: "invalid second item", body: `{"user_id_list":["1","invalid"]}`, wantFields: []string{"user_id_list"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newProfileValidatorRequest(http.MethodPost, "/batchQueryUserProfiles", test.body)
			validator := NewBatchQueryUserProfilesValidator(nil)

			validator.BatchQueryUserProfiles(ctx)

			assertProfileValidatorBadRequest(t, ctx, recorder, test.wantFields...)
		})
	}
}

type batchQueryUserProfilesRequestCase struct {
	name       string
	userIDs    []string
	wantReason string
}

func TestValidateBatchQueryUserProfilesRequest(t *testing.T) {
	const countReason = "must contain between 1 and 100 user IDs"
	const itemReason = "each item must be a positive 64-bit integer string"
	tests := []batchQueryUserProfilesRequestCase{
		// 单个 ID 恰好达到数量下限，验证允许只查询一个用户。
		{name: "minimum size", userIDs: []string{"1"}},
		// 使用 100 个重复项达到上限，验证包含重复项的有效列表原样保留。
		{name: "maximum size with duplicates", userIDs: slices.Repeat([]string{"2"}, 100)},
		// 混合顺序、重复项、前导零和 int64 上界，验证校验保持输入的顺序和原始文本。
		{name: "preserves order and ID text", userIDs: []string{"2", "0001", "9223372036854775807", "2"}},
		// 显式空切片的长度为零，验证按数量规则拒绝。
		{name: "empty list", userIDs: []string{}, wantReason: countReason},
		// 101 项中同时有重复和非法文本，验证按去重前数量判断，并优先返回数量错误。
		{name: "count checked before elements", userIDs: append([]string{"invalid"}, slices.Repeat([]string{"1"}, 100)...), wantReason: countReason},
		// 数量合法但后两项分别为零和非法文本，验证最多生成一条元素校验明细。
		{name: "multiple invalid items", userIDs: []string{"1", "0", "invalid"}, wantReason: itemReason},
		// 比 int64 上界多一的十进制文本无法表示用户 ID，验证仍归到列表字段。
		{name: "item exceeds int64", userIDs: []string{"9223372036854775808"}, wantReason: itemReason},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := userprofiledto.BatchQueryUserProfilesRequest{UserIDList: slices.Clone(test.userIDs)}

			details, err := validateBatchQueryUserProfilesRequest(&request)

			if err != nil {
				t.Fatalf("validateBatchQueryUserProfilesRequest() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(request.UserIDList, test.userIDs) {
				t.Errorf("UserIDList = %v, want original %v", request.UserIDList, test.userIDs)
			}
			if test.wantReason == "" {
				assertProfileValidatorFields(t, details)
				return
			}
			assertProfileValidatorFields(t, details, "user_id_list")
			if len(details) == 1 && details[0].Reason != test.wantReason {
				t.Errorf("reason = %q, want %q", details[0].Reason, test.wantReason)
			}
		})
	}
}
