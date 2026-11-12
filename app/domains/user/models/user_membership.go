package models

import (
	"errors"
	"time"
)

const (
	// MembershipStatusNotOpened 表示用户尚未创建会员权益记录。
	MembershipStatusNotOpened = "NOT_OPENED"
	// MembershipStatusActive 表示查询时刻位于权益的左闭右开有效区间内。
	MembershipStatusActive = "ACTIVE"
	// MembershipStatusExpired 表示查询时刻已达到或超过权益到期时间。
	MembershipStatusExpired = "EXPIRED"
)

// ErrInvalidUserMembershipState 表示已保存权益不符合当前标准会员规则。
// 它是数据异常，不是查询请求参数错误，不能分类为 INVALID_ARGUMENT。
var ErrInvalidUserMembershipState = errors.New("stored user membership state is invalid")

// UserMembership 表示用户当前会员权益，到期后仍保留记录。
// 字段按业务含义声明，数据库映射由 tables.UserMembership 承担。
type UserMembership struct {
	// ID 是当前会员权益记录的内部标识，由应用通过统一雪花组件生成。
	ID int64

	// UserID 是权益所属用户的内部标识。
	UserID int64

	// MembershipLevel 是当前会员等级，本例 10 表示标准会员。
	MembershipLevel int32

	// EffectiveAt 是当前权益的 UTC 生效时间，精度为毫秒。
	EffectiveAt time.Time

	// ExpiredAt 是当前权益的 UTC 到期时间，精度为毫秒。
	ExpiredAt time.Time

	// CreatedAt 是权益记录的 UTC 创建时间，精度为毫秒。
	CreatedAt time.Time

	// UpdatedAt 是权益记录最近修改的 UTC 时间，精度为毫秒。
	UpdatedAt time.Time
}

// StatusAt 使用调用方提供的同一个查询时刻判断权益状态，不修改原记录。
// nil 表示尚未开通；已有记录须为标准会员且具有合法的 UTC 毫秒期限。
// 当前规则不支持未来生效的权益，到期记录仍保留原等级及期限。
func (m *UserMembership) StatusAt(at time.Time) (string, error) {
	if !validMembershipTime(at) {
		return "", ErrInvalidUserMembershipState
	}
	if m == nil {
		return MembershipStatusNotOpened, nil
	}
	if m.ID <= 0 || m.UserID <= 0 || m.MembershipLevel != 10 ||
		!validMembershipTime(m.EffectiveAt) || !validMembershipTime(m.ExpiredAt) ||
		!m.EffectiveAt.Before(m.ExpiredAt) || at.Before(m.EffectiveAt) {
		return "", ErrInvalidUserMembershipState
	}
	if at.Before(m.ExpiredAt) {
		return MembershipStatusActive, nil
	}
	return MembershipStatusExpired, nil
}

// validMembershipTime 保证时间可无损表示为本项目的 MySQL DATETIME(3)。
// 时区只影响表示，范围按 UTC 判断，精度不能依靠查询时截断修复。
func validMembershipTime(value time.Time) bool {
	utc := value.UTC()
	return utc.Year() >= 1000 && utc.Year() <= 9999 && utc.Nanosecond()%int(time.Millisecond) == 0
}
