package v1

import "go-service-template/app/domains/user/dto/v1/shared"

// QueryUserProfilePageRequest 表示使用页码分页读取用户资料的 Query 参数。
type QueryUserProfilePageRequest struct {
	// PageNumber 从 1 开始，省略时为 1。
	PageNumber int `json:"page_number"`
	// PageSize 是每页数量，范围为 1～100，省略时为 20。
	PageSize int `json:"page_size"`
	// UserName 可选，非空时按展示昵称精确匹配，不作为关键词检索。
	UserName string `json:"user_name"`
	// IsEnabled 可选，nil 表示包含所有启用状态，Query 值只接受 true、false。
	IsEnabled *bool `json:"is_enabled"`
}

// QueryUserProfilePageResponse 表示按用户 ID 降序排列的一页资料及分页信息。
type QueryUserProfilePageResponse struct {
	// UserProfileList 是本页资料，空页输出 []。
	UserProfileList []shared.UserProfile `json:"user_profile_list"`
	// PageNumber 是本次查询的页码，从 1 开始。
	PageNumber int `json:"page_number"`
	// PageSize 是本次查询的每页数量。
	PageSize int `json:"page_size"`
	// TotalCount 是符合筛选条件的用户总数。
	TotalCount int64 `json:"total_count"`
}
