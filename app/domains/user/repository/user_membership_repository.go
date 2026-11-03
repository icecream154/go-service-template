package repository

import (
	"time"

	"go-service-template/app/domains/user/tables"

	"gorm.io/gorm"
)

// userMembershipTable 固定当前会员权益表的完整库表名，不依赖连接的默认数据库。
const userMembershipTable = "user_service.user_membership"

// UserMembershipRepository 封装当前会员权益表的查询、新增和修改。
// 每次调用独立建立表和查询条件，不在仓储对象中保存本次操作状态。
type UserMembershipRepository struct {
	// db 保存注入连接或事务的干净会话，操作中始终复用该连接入口。
	db *gorm.DB
}

// NewUserMembershipRepository 保存已有连接或事务，不执行 SQL。
// NewDB 清除语句条件，保留原事务、context 和 NowFunc，不新建连接池或开启事务。
func NewUserMembershipRepository(db *gorm.DB) *UserMembershipRepository {
	return &UserMembershipRepository{db: db.Session(&gorm.Session{NewDB: true})}
}

// GetMembershipByID 按权益记录 ID 查询，返回完整表结构。
// 未命中或查询失败直接返回 GORM 原始错误，不返回零值记录。
func (r *UserMembershipRepository) GetMembershipByID(id int64) (*tables.UserMembership, error) {
	var membership tables.UserMembership
	if err := r.db.Table(userMembershipTable).Where("id = ?", id).Take(&membership).Error; err != nil {
		return nil, err
	}
	return &membership, nil
}

// GetMembershipByUserID 按用户 ID 查询当前权益，对应表中的 user_id 唯一键。
// 不按到期时间过滤，因此到期后保留的记录也会返回；错误原样返回。
func (r *UserMembershipRepository) GetMembershipByUserID(userID int64) (*tables.UserMembership, error) {
	var membership tables.UserMembership
	if err := r.db.Table(userMembershipTable).Where("user_id = ?", userID).Take(&membership).Error; err != nil {
		return nil, err
	}
	return &membership, nil
}

// CreateMembership 新增一条当前权益，使用独立值副本写入，成功后才回填输入。
// 创建和修改时间为零时使用同一次 NowFunc 结果；所有时间均转为 UTC 并截断到毫秒。
// 生效、到期时间只转换表示和精度，不替零值计算或填写会员期限。
// 写入失败返回原始错误且不修改输入；在外部事务中成功回填不代表事务已经提交。
func (r *UserMembershipRepository) CreateMembership(membership *tables.UserMembership) error {
	// 空指针交由 GORM 返回原生错误，避免先解引用，也不引入自定义参数错误。
	if membership == nil {
		return r.db.Table(userMembershipTable).Create(membership).Error
	}
	row := *membership
	now := r.db.NowFunc().UTC().Truncate(time.Millisecond)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = now
	}
	row.CreatedAt = row.CreatedAt.UTC().Truncate(time.Millisecond)
	row.UpdatedAt = row.UpdatedAt.UTC().Truncate(time.Millisecond)
	row.EffectiveAt = row.EffectiveAt.UTC().Truncate(time.Millisecond)
	row.ExpiredAt = row.ExpiredAt.UTC().Truncate(time.Millisecond)
	if err := r.db.Table(userMembershipTable).Create(&row).Error; err != nil {
		return err
	}
	*membership = row
	return nil
}

// UpdateMembership 按记录 ID 和所属用户 ID 同时定位，只替换等级、生效及到期时间。
// changes 中的 ID、UserID、CreatedAt、UpdatedAt 不用于写入，修改时间使用 NowFunc。
// map 显式写入选定字段，包括零值；时间转为 UTC 毫秒，不自动计算业务期限。
// 原样返回影响行数和错误，零行不转换为错误；本方法不修改传入的 changes。
func (r *UserMembershipRepository) UpdateMembership(id, userID int64, changes tables.UserMembership) (int64, error) {
	result := r.db.Table(userMembershipTable).Model(&tables.UserMembership{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{
			"membership_level": changes.MembershipLevel,
			"effective_at":     changes.EffectiveAt.UTC().Truncate(time.Millisecond),
			"expired_at":       changes.ExpiredAt.UTC().Truncate(time.Millisecond),
			"updated_at":       r.db.NowFunc().UTC().Truncate(time.Millisecond),
		})
	return result.RowsAffected, result.Error
}
