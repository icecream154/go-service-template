package models

import (
	"encoding/base64"
	"encoding/binary"
	"time"

	checkformat "go-service-template/internal/check/format"
	"go-service-template/internal/check/scalar"
)

const (
	// membershipChangeRecordPageTokenVersion 固定本领域历史列表的令牌版本。
	membershipChangeRecordPageTokenVersion = 1
	// membershipChangeRecordPageTokenBytes 包含版本字节及三个 64 位整数。
	membershipChangeRecordPageTokenBytes = 25
)

// MembershipChangeRecordCursor 表示某个用户历史列表中最后返回的位置。
// 令牌不承载权限；每次查询仍须验证调用方与 UserID 的本人关系。
type MembershipChangeRecordCursor struct {
	// UserID 是令牌绑定的目标用户，必须为正数。
	UserID int64
	// CreatedAt 是最后返回记录的创建时刻，保留 UTC 毫秒精度。
	CreatedAt time.Time
	// ID 是最后返回记录的正数内部标识，同毫秒记录以此继续排序。
	ID int64
}

// ParseMembershipChangeRecordPageToken 解析本领域的不透明翻页令牌并校验所属用户。
// 空令牌表示第一页；格式、版本、范围或用户不匹配均保留公共输入错误分类。
// 解码长度有固定上限，不将令牌原文或内部边界拼入错误文案。
func ParseMembershipChangeRecordPageToken(token string, userID int64) (*MembershipChangeRecordCursor, error) {
	if err := scalar.PositiveInt64(userID); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, nil
	}
	data, err := checkformat.ParseBase64(token, checkformat.Base64URLRaw, membershipChangeRecordPageTokenBytes)
	if err != nil {
		return nil, err
	}
	if err := scalar.IntInRange(len(data), membershipChangeRecordPageTokenBytes, membershipChangeRecordPageTokenBytes); err != nil {
		return nil, err
	}
	if err := scalar.IntInRange(int(data[0]), membershipChangeRecordPageTokenVersion, membershipChangeRecordPageTokenVersion); err != nil {
		return nil, err
	}
	cursor := MembershipChangeRecordCursor{
		UserID:    int64(binary.BigEndian.Uint64(data[1:9])),
		CreatedAt: time.UnixMilli(int64(binary.BigEndian.Uint64(data[9:17]))).UTC(),
		ID:        int64(binary.BigEndian.Uint64(data[17:25])),
	}
	if err := validateMembershipChangeRecordCursor(cursor); err != nil {
		return nil, err
	}
	if err := scalar.Int64InRange(cursor.UserID, userID, userID); err != nil {
		return nil, err
	}
	return &cursor, nil
}

// EncodeMembershipChangeRecordPageToken 从实际返回的末条历史位置生成规范令牌。
// 所有 ID 使用整数二进制编码，不经过浮点数；时间保留 UTC 毫秒精度。
func EncodeMembershipChangeRecordPageToken(cursor MembershipChangeRecordCursor) (string, error) {
	if err := validateMembershipChangeRecordCursor(cursor); err != nil {
		return "", err
	}
	data := make([]byte, membershipChangeRecordPageTokenBytes)
	data[0] = membershipChangeRecordPageTokenVersion
	binary.BigEndian.PutUint64(data[1:9], uint64(cursor.UserID))
	binary.BigEndian.PutUint64(data[9:17], uint64(cursor.CreatedAt.UnixMilli()))
	binary.BigEndian.PutUint64(data[17:25], uint64(cursor.ID))
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// validateMembershipChangeRecordCursor 约束内部边界值，供编码和解析共同使用。
func validateMembershipChangeRecordCursor(cursor MembershipChangeRecordCursor) error {
	if err := scalar.PositiveInt64(cursor.UserID); err != nil {
		return err
	}
	if err := scalar.PositiveInt64(cursor.ID); err != nil {
		return err
	}
	if err := scalar.IntInRange(cursor.CreatedAt.UTC().Year(), 1000, 9999); err != nil {
		return err
	}
	return scalar.IntInRange(cursor.CreatedAt.Nanosecond()%int(time.Millisecond), 0, 0)
}
