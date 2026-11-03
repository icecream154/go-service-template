package repository

import (
	"time"

	"go-service-template/app/domains/user/tables"

	"gorm.io/gorm"
)

// membershipChangeRecordTable 固定使用完整库表名，不依赖注入连接的默认数据库。
const membershipChangeRecordTable = "user_service.membership_change_record"

// MembershipChangeRecordRepository 负责会员权益变更历史的查询和追加，不提供修改或删除。
type MembershipChangeRecordRepository struct {
	// db 复用调用方注入的连接或事务，不创建连接池，也不提交或回滚事务。
	db *gorm.DB
}

// NewMembershipChangeRecordRepository 创建历史表仓储，不执行 SQL。
// NewDB 清除已有语句条件，保留原连接或事务、context 和 NowFunc。
func NewMembershipChangeRecordRepository(db *gorm.DB) *MembershipChangeRecordRepository {
	return &MembershipChangeRecordRepository{db: db.Session(&gorm.Session{NewDB: true})}
}

// GetRecordByID 按历史记录 ID 查询，未命中或查询失败时原样返回 GORM 错误。
func (r *MembershipChangeRecordRepository) GetRecordByID(id int64) (*tables.MembershipChangeRecord, error) {
	var record tables.MembershipChangeRecord
	if err := r.db.Table(membershipChangeRecordTable).Where("id = ?", id).Take(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// GetRecordByOrderID 按订单唯一键查询历史，不将查询未命中解释为业务处理状态。
func (r *MembershipChangeRecordRepository) GetRecordByOrderID(orderID int64) (*tables.MembershipChangeRecord, error) {
	var record tables.MembershipChangeRecord
	if err := r.db.Table(membershipChangeRecordTable).Where("order_id = ?", orderID).Take(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// CreateRecord 追加一条历史，ID、订单及权益快照的合法性由调用方保证。
// 使用副本统一 UTC 毫秒时间，失败不修改输入，成功后回填实际写入的值。
// 不处理唯一键冲突，不自动覆盖记录；外部事务的最终提交仍由调用方负责。
func (r *MembershipChangeRecordRepository) CreateRecord(record *tables.MembershipChangeRecord) error {
	// 空指针交由 GORM 处理，不在副本创建前解引用或自定义参数错误。
	if record == nil {
		return r.db.Table(membershipChangeRecordTable).Create(record).Error
	}
	row := *record
	if row.CreatedAt.IsZero() {
		row.CreatedAt = r.db.NowFunc()
	}
	row.CreatedAt = row.CreatedAt.UTC().Truncate(time.Millisecond)
	row.AfterEffectiveAt = row.AfterEffectiveAt.UTC().Truncate(time.Millisecond)
	row.AfterExpiredAt = row.AfterExpiredAt.UTC().Truncate(time.Millisecond)
	// 仅处理有值的变更前时间；Valid=false 由 sql.NullTime 写 SQL NULL。
	// 变更前等级按传入的 sql.NullInt32 原样保存，不修改其值或 Valid 标记。
	if row.BeforeEffectiveAt.Valid {
		row.BeforeEffectiveAt.Time = row.BeforeEffectiveAt.Time.UTC().Truncate(time.Millisecond)
	}
	if row.BeforeExpiredAt.Valid {
		row.BeforeExpiredAt.Time = row.BeforeExpiredAt.Time.UTC().Truncate(time.Millisecond)
	}
	if err := r.db.Table(membershipChangeRecordTable).Create(&row).Error; err != nil {
		return err
	}
	*record = row
	return nil
}
