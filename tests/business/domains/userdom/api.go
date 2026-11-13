//go:build business

package userdom

import (
	"strconv"
	"testing"
	"time"

	"go-service-template/tests/testkit/apicall"
	"go-service-template/tests/testkit/flow"
)

const (
	createUserPath                      = "/api/v1/user/userProfile/createUser"
	queryProfileByUserIDPath            = "/api/v1/user/userProfile/queryUserProfileByUserId"
	queryProfilePagePath                = "/api/v1/user/userProfile/queryUserProfilePage"
	generateAvatarPath                  = "/api/v1/user/userProfile/generateUserAvatar"
	updateProfilePath                   = "/api/v1/user/userProfile/updateUserProfile"
	batchQueryProfilesPath              = "/api/v1/user/userProfile/batchQueryUserProfiles"
	queryMembershipByUserIDPath         = "/api/v1/user/userMembership/queryUserMembershipByUserId"
	queryMembershipChangeRecordPagePath = "/api/v1/user/userMembership/queryMembershipChangeRecordPage"
)

// MustParseID 将线上接口返回的十进制字符串 ID 解析为正整数 int64，供后续步骤作为真实标识传递。
func MustParseID(t testing.TB, rawID string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("expected positive decimal int64 wire ID, got %q (err=%v)", rawID, err)
	}
	return id
}

// UserProfileDTO 对应 HTTP 响应中的用户资料 JSON 结构。
type UserProfileDTO struct {
	UserID    string  `json:"user_id"`
	UserName  string  `json:"user_name"`
	AvatarURL *string `json:"avatar_url"`
	IsEnabled bool    `json:"is_enabled"`
	Version   int64   `json:"version"`
}

// CreateUserRequest 对应 POST /api/v1/user/userProfile/createUser 请求体。
type CreateUserRequest struct {
	AccountSource     string  `json:"account_source"`
	ExternalAccountID string  `json:"external_account_id"`
	UserName          string  `json:"user_name"`
	AvatarURL         *string `json:"avatar_url,omitempty"`
}

// CreateUserData 对应 POST /api/v1/user/userProfile/createUser 成功响应 data。
type CreateUserData struct {
	UserProfile UserProfileDTO `json:"user_profile"`
	AccessToken string         `json:"access_token"`
	TokenType   string         `json:"token_type"`
	ExpiresAt   time.Time      `json:"expires_at"`
}

// CallCreateUser 发送 1 次创建用户 HTTP 请求并返回原始响应。
func CallCreateUser(s *flow.Step, req CreateUserRequest, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	allOpts := append([]apicall.Option{apicall.WithJSONBody(req)}, opts...)
	return s.HTTP().PostJSON(s.T(), createUserPath, allOpts...)
}

// MustCreateUser 发送 1 次创建用户请求并断言返回 HTTP 200 成功载荷。
func MustCreateUser(s *flow.Step, req CreateUserRequest, opts ...apicall.Option) CreateUserData {
	s.T().Helper()
	return apicall.RequireSuccess[CreateUserData](s.T(), CallCreateUser(s, req, opts...))
}

// CallQueryProfileByUserID 发送 1 次按用户 ID 查询资料请求并返回原始响应。
func CallQueryProfileByUserID(s *flow.Step, userID int64, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	allOpts := append([]apicall.Option{apicall.WithQuery("user_id", strconv.FormatInt(userID, 10))}, opts...)
	return s.HTTP().Get(s.T(), queryProfileByUserIDPath, allOpts...)
}

// MustQueryProfileByUserID 发送 1 次按用户 ID 查询资料请求并断言返回 HTTP 200 成功载荷。
func MustQueryProfileByUserID(s *flow.Step, userID int64, opts ...apicall.Option) UserProfileDTO {
	s.T().Helper()
	return apicall.RequireSuccess[UserProfileDTO](s.T(), CallQueryProfileByUserID(s, userID, opts...))
}

// QueryProfilePageRequest 对应 GET /api/v1/user/userProfile/queryUserProfilePage 查询参数。
type QueryProfilePageRequest struct {
	UserName   string
	IsEnabled  *bool
	PageNumber int
	PageSize   int
}

// QueryProfilePageData 对应 GET /api/v1/user/userProfile/queryUserProfilePage 成功响应 data。
type QueryProfilePageData struct {
	UserProfileList []UserProfileDTO `json:"user_profile_list"`
	PageNumber      int              `json:"page_number"`
	PageSize        int              `json:"page_size"`
	TotalCount      int64            `json:"total_count"`
}

// CallQueryProfilePage 发送 1 次用户资料分页查询请求并返回原始响应。
func CallQueryProfilePage(s *flow.Step, req QueryProfilePageRequest, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	queryOpts := []apicall.Option{
		apicall.WithQuery("page_number", strconv.Itoa(req.PageNumber)),
		apicall.WithQuery("page_size", strconv.Itoa(req.PageSize)),
	}
	if req.UserName != "" {
		queryOpts = append(queryOpts, apicall.WithQuery("user_name", req.UserName))
	}
	if req.IsEnabled != nil {
		queryOpts = append(queryOpts, apicall.WithQuery("is_enabled", strconv.FormatBool(*req.IsEnabled)))
	}
	queryOpts = append(queryOpts, opts...)
	return s.HTTP().Get(s.T(), queryProfilePagePath, queryOpts...)
}

// MustQueryProfilePage 发送 1 次用户资料分页查询请求并断言返回 HTTP 200 成功载荷。
func MustQueryProfilePage(s *flow.Step, req QueryProfilePageRequest, opts ...apicall.Option) QueryProfilePageData {
	s.T().Helper()
	return apicall.RequireSuccess[QueryProfilePageData](s.T(), CallQueryProfilePage(s, req, opts...))
}

// GenerateAvatarData 对应 POST /api/v1/user/userProfile/generateUserAvatar 成功响应 data。
type GenerateAvatarData struct {
	AvatarURL string `json:"avatar_url"`
}

// CallGenerateAvatar 发送 1 次生成头像请求并返回原始响应。
func CallGenerateAvatar(s *flow.Step, prompt string, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	body := map[string]string{"prompt": prompt}
	allOpts := append([]apicall.Option{apicall.WithJSONBody(body)}, opts...)
	return s.HTTP().PostJSON(s.T(), generateAvatarPath, allOpts...)
}

// MustGenerateAvatar 发送 1 次生成头像请求并断言返回 HTTP 200 成功载荷。
func MustGenerateAvatar(s *flow.Step, prompt string, opts ...apicall.Option) GenerateAvatarData {
	s.T().Helper()
	return apicall.RequireSuccess[GenerateAvatarData](s.T(), CallGenerateAvatar(s, prompt, opts...))
}

// UpdateProfileRequest 对应 POST /api/v1/user/userProfile/updateUserProfile 请求体。
type UpdateProfileRequest struct {
	UserID          int64
	ExpectedVersion int64
	UserName        string
	AvatarURL       *string
}

// CallUpdateProfile 发送 1 次更新本人资料请求并返回原始响应。
func CallUpdateProfile(s *flow.Step, req UpdateProfileRequest, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	wireBody := map[string]any{
		"user_id":          strconv.FormatInt(req.UserID, 10),
		"expected_version": req.ExpectedVersion,
		"user_name":        req.UserName,
		"avatar_url":       req.AvatarURL,
	}
	allOpts := append([]apicall.Option{apicall.WithJSONBody(wireBody)}, opts...)
	return s.HTTP().PostJSON(s.T(), updateProfilePath, allOpts...)
}

// MustUpdateProfile 发送 1 次更新本人资料请求并断言返回 HTTP 200 成功载荷。
func MustUpdateProfile(s *flow.Step, req UpdateProfileRequest, opts ...apicall.Option) UserProfileDTO {
	s.T().Helper()
	return apicall.RequireSuccess[UserProfileDTO](s.T(), CallUpdateProfile(s, req, opts...))
}

// BatchQueryProfilesData 对应 POST /api/v1/user/userProfile/batchQueryUserProfiles 成功响应 data。
type BatchQueryProfilesData struct {
	UserProfileList   []UserProfileDTO `json:"user_profile_list"`
	MissingUserIDList []string         `json:"missing_user_id_list"`
}

// CallBatchQueryProfiles 发送 1 次批量查询用户资料请求并返回原始响应。
func CallBatchQueryProfiles(s *flow.Step, userIDs []int64, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	wireIDs := make([]string, len(userIDs))
	for i, id := range userIDs {
		wireIDs[i] = strconv.FormatInt(id, 10)
	}
	allOpts := append([]apicall.Option{apicall.WithJSONBody(map[string]any{"user_id_list": wireIDs})}, opts...)
	return s.HTTP().PostJSON(s.T(), batchQueryProfilesPath, allOpts...)
}

// MustBatchQueryProfiles 发送 1 次批量查询用户资料请求并断言返回 HTTP 200 成功载荷。
func MustBatchQueryProfiles(s *flow.Step, userIDs []int64, opts ...apicall.Option) BatchQueryProfilesData {
	s.T().Helper()
	return apicall.RequireSuccess[BatchQueryProfilesData](s.T(), CallBatchQueryProfiles(s, userIDs, opts...))
}

// UserMembershipDTO 对应 HTTP 响应中的会员快照对象。
type UserMembershipDTO struct {
	MembershipID    string    `json:"membership_id"`
	MembershipLevel int32     `json:"membership_level"`
	EffectiveAt     time.Time `json:"effective_at"`
	ExpiredAt       time.Time `json:"expired_at"`
}

// QueryMembershipData 对应 GET /api/v1/user/userMembership/queryUserMembershipByUserId 成功响应 data。
type QueryMembershipData struct {
	UserID           string             `json:"user_id"`
	MembershipStatus string             `json:"membership_status"`
	QueriedAt        time.Time          `json:"queried_at"`
	Membership       *UserMembershipDTO `json:"membership"`
}

// CallQueryMembershipByUserID 发送 1 次查询会员状态请求并返回原始响应。
func CallQueryMembershipByUserID(s *flow.Step, userID int64, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	allOpts := append([]apicall.Option{apicall.WithQuery("user_id", strconv.FormatInt(userID, 10))}, opts...)
	return s.HTTP().Get(s.T(), queryMembershipByUserIDPath, allOpts...)
}

// MustQueryMembershipByUserID 发送 1 次查询会员状态请求并断言返回 HTTP 200 成功载荷。
func MustQueryMembershipByUserID(s *flow.Step, userID int64, opts ...apicall.Option) QueryMembershipData {
	s.T().Helper()
	return apicall.RequireSuccess[QueryMembershipData](s.T(), CallQueryMembershipByUserID(s, userID, opts...))
}

// MembershipChangeRecordDTO 对应 HTTP 响应中的单条会员权益变更流水。
type MembershipChangeRecordDTO struct {
	RecordID              string     `json:"record_id"`
	MembershipID          string     `json:"membership_id"`
	OrderID               string     `json:"order_id"`
	ChangeType            int32      `json:"change_type"`
	BeforeMembershipLevel *int32     `json:"before_membership_level"`
	BeforeEffectiveAt     *time.Time `json:"before_effective_at"`
	BeforeExpiredAt       *time.Time `json:"before_expired_at"`
	AfterMembershipLevel  int32      `json:"after_membership_level"`
	AfterEffectiveAt      time.Time  `json:"after_effective_at"`
	AfterExpiredAt        time.Time  `json:"after_expired_at"`
	CreatedAt             time.Time  `json:"created_at"`
}

// QueryMembershipChangeRecordPageRequest 对应 GET /api/v1/user/userMembership/queryMembershipChangeRecordPage 查询参数。
type QueryMembershipChangeRecordPageRequest struct {
	UserID    int64
	PageSize  int
	PageToken string
}

// QueryMembershipChangeRecordPageData 对应 GET /api/v1/user/userMembership/queryMembershipChangeRecordPage 成功响应 data。
type QueryMembershipChangeRecordPageData struct {
	MembershipChangeRecordList []MembershipChangeRecordDTO `json:"membership_change_record_list"`
	PageSize                   int                         `json:"page_size"`
	HasMore                    bool                        `json:"has_more"`
	NextPageToken              *string                     `json:"next_page_token"`
}

// CallQueryMembershipChangeRecordPage 发送 1 次会员变更历史游标分页查询请求并返回原始响应。
func CallQueryMembershipChangeRecordPage(s *flow.Step, req QueryMembershipChangeRecordPageRequest, opts ...apicall.Option) *apicall.Response {
	s.T().Helper()
	queryOpts := []apicall.Option{
		apicall.WithQuery("user_id", strconv.FormatInt(req.UserID, 10)),
	}
	if req.PageSize > 0 {
		queryOpts = append(queryOpts, apicall.WithQuery("page_size", strconv.Itoa(req.PageSize)))
	}
	if req.PageToken != "" {
		queryOpts = append(queryOpts, apicall.WithQuery("page_token", req.PageToken))
	}
	queryOpts = append(queryOpts, opts...)
	return s.HTTP().Get(s.T(), queryMembershipChangeRecordPagePath, queryOpts...)
}

// MustQueryMembershipChangeRecordPage 发送 1 次会员变更历史游标分页查询请求并断言返回 HTTP 200 成功载荷。
func MustQueryMembershipChangeRecordPage(s *flow.Step, req QueryMembershipChangeRecordPageRequest, opts ...apicall.Option) QueryMembershipChangeRecordPageData {
	s.T().Helper()
	return apicall.RequireSuccess[QueryMembershipChangeRecordPageData](s.T(), CallQueryMembershipChangeRecordPage(s, req, opts...))
}
