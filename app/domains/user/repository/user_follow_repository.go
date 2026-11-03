package repository

import (
	"time"

	"go-service-template/app/domains/user/tables"

	"gorm.io/gorm"
)

// userFollowTable 固定关注关系的完整库表名，普通连接和跨库事务均访问同一张表。
const userFollowTable = "user_service.user_follow"

// UserFollowRepository 读写当前关注关系，不判断用户身份、自关注或参数合法性。
type UserFollowRepository struct {
	// db 保留注入的连接或事务及其 context，每次操作单独创建查询语句。
	// 连接池和事务由调用方管理，本仓储不创建、提交或关闭它们。
	db *gorm.DB
}

// NewUserFollowRepository 保存干净会话，不执行 SQL，也不改变注入事务。
// NewDB 清除已存在的语句条件，但保留连接、context 和 NowFunc 等配置。
func NewUserFollowRepository(db *gorm.DB) *UserFollowRepository {
	return &UserFollowRepository{db: db.Session(&gorm.Session{NewDB: true})}
}

// GetFollowByID 按关注记录 ID 查询，直接返回表结构。
// 未找到或查询失败时返回 nil 和 GORM 原始错误，不分类或包装错误。
func (r *UserFollowRepository) GetFollowByID(id int64) (*tables.UserFollow, error) {
	var follow tables.UserFollow
	if err := r.db.Table(userFollowTable).Where("id = ?", id).Take(&follow).Error; err != nil {
		return nil, err
	}
	return &follow, nil
}

// GetFollowByUsers 按关注方、被关注方的联合唯一键查询。
// 两个角色有方向，参数顺序不会交换，也不把反向关注视为同一条记录。
func (r *UserFollowRepository) GetFollowByUsers(followerUserID, followedUserID int64) (*tables.UserFollow, error) {
	var follow tables.UserFollow
	if err := r.db.Table(userFollowTable).
		Where("follower_user_id = ? AND followed_user_id = ?", followerUserID, followedUserID).
		Take(&follow).Error; err != nil {
		return nil, err
	}
	return &follow, nil
}

// CreateFollow 新增关注记录，ID 和双方用户标识由调用方提供并校验。
// 不做 upsert，也不吞掉联合唯一键冲突；数据库错误原样返回。
// 通过独立副本写入，成功后才回填 CreatedAt，失败不修改原对象。
// 外部事务内语句成功不代表已提交，后续回滚也不会自动还原内存中的回填结果。
func (r *UserFollowRepository) CreateFollow(follow *tables.UserFollow) error {
	if follow == nil {
		// 空指针交给 GORM 按原生规则处理，避免先解引用或引入自定义参数错误。
		return r.db.Table(userFollowTable).Create(follow).Error
	}
	row := *follow
	if row.CreatedAt.IsZero() {
		row.CreatedAt = r.db.NowFunc()
	}
	// 显式时间保留原时刻，零值时间来自注入时钟；两者统一保存为 UTC 毫秒。
	row.CreatedAt = row.CreatedAt.UTC().Truncate(time.Millisecond)
	if err := r.db.Table(userFollowTable).Create(&row).Error; err != nil {
		return err
	}
	*follow = row
	return nil
}

// DeleteFollow 按记录 ID 和关注双方三个条件物理删除，不只按双方用户标识删除。
// 旧请求的记录 ID 无法匹配取消后重新建立的新关注，避免误删后续关系。
// 返回实际影响行数和原始错误；0 行不是 SQL 错误，由调用方解释是否已取消或记录不匹配。
func (r *UserFollowRepository) DeleteFollow(id, followerUserID, followedUserID int64) (int64, error) {
	result := r.db.Table(userFollowTable).
		Where("id = ? AND follower_user_id = ? AND followed_user_id = ?", id, followerUserID, followedUserID).
		Delete(&tables.UserFollow{})
	return result.RowsAffected, result.Error
}
