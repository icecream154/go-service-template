package usermembership

// QueryMembershipChangeRecordPageRequest 表示本人权益变更历史的游标分页 Query 参数。
type QueryMembershipChangeRecordPageRequest struct {
	// UserID 必填，必须为正 int64 的十进制文本。
	UserID string `form:"user_id" json:"user_id"`
	// PageSize 省略时为 20，范围为 1～100；显式零值不采用默认值。
	PageSize int `form:"page_size" json:"page_size"`
	// PageToken 省略或空字符串表示第一页；后续原样传入上一页的令牌。
	PageToken string `form:"page_token" json:"page_token"`
}

// QueryMembershipChangeRecordPageResponse 返回按创建时间、记录 ID 降序排列的历史页。
// 跨页不承诺固定快照，不返回总数；翻页仍须验证当前请求身份。
type QueryMembershipChangeRecordPageResponse struct {
	// MembershipChangeRecordList 是本页历史记录，无记录时输出 []。
	MembershipChangeRecordList []MembershipChangeRecord `json:"membership_change_record_list"`
	// PageSize 是本次采用的每页数量。
	PageSize int `json:"page_size"`
	// HasMore 表示还有下一页，不由客户端猜测本页条数。
	HasMore bool `json:"has_more"`
	// NextPageToken 是下一页令牌，没有下一页时为 null。
	NextPageToken *string `json:"next_page_token"`
}
