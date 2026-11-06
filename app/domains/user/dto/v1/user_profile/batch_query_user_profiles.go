package userprofile

// BatchQueryUserProfilesRequest 表示按一组明确用户 ID 批量读取资料。
type BatchQueryUserProfilesRequest struct {
	// UserIDList 必填，包含 1～100 个正 int64 的十进制字符串，重复 ID 只处理首次出现。
	UserIDList []string `json:"user_id_list"`
}

// BatchQueryUserProfilesResponse 区分已找到的资料和未找到的用户 ID。
type BatchQueryUserProfilesResponse struct {
	// UserProfileList 按请求中 ID 首次出现的顺序返回资料，无结果时输出 []。
	UserProfileList []UserProfile `json:"user_profile_list"`
	// MissingUserIDList 按请求顺序返回未找到的 ID，无缺失时输出 []。
	MissingUserIDList []string `json:"missing_user_id_list"`
}
