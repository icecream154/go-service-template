// Package dto 定义用户相关接口的请求和响应。
package dto

import "go-service-template/app/domains/user/dto/shared"

// QueryUserProfileByUserIDRequest 表示按内部用户 ID 查询用户资料的请求参数。
type QueryUserProfileByUserIDRequest struct {
	// UserID 是待查询的内部用户 ID，必须大于 0；JSON 使用十进制字符串。
	UserID int64 `json:"user_id,string"`
}

// QueryUserProfileByUserIDResponse 表示查询成功后返回的用户基本资料。
// 通过类型别名复用 shared.UserProfile，JSON 直接包含其字段。
type QueryUserProfileByUserIDResponse = shared.UserProfile
