// Package repository 按表封装数据库操作。
// 每张表的仓储结构体、构造函数及操作放在对应的 *_repository.go 文件中。
// 仓储直接使用 tables 中的表结构，不依赖 DDD models、HTTP 或请求上下文。
package repository

import (
	"time"

	"go-service-template/app/domains/user/tables"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// userProfileTable 统一维护用户资料表的完整库表名，不让业务层或事务入口拼接表名。
// 普通连接与跨库事务都访问这个表，因此不依赖注入连接是否选择了默认数据库。
// 库或表重命名时同步维护这里、tables 模型及受控 SQL；连接账号仍须具备目标表权限。
const userProfileTable = "user_service.user_profile"

// UserProfileRepository 只负责 user_profile 表的数据操作。
// 连接由构造函数注入，可用于服务、脚本或后台任务，不要求存在 Web 应用。
// 查询条件在每次调用时建立，不在仓储对象中保存用户或当前查询状态。
type UserProfileRepository struct {
	// db 保存注入连接或事务上的干净会话；每次操作自行指定完整库表名和查询条件。
	// 不另取全局连接，也不改变连接的默认库，因此可直接接收没有默认库的跨库 tx。
	// 连接池的创建、关闭以及事务的提交、回滚由持有连接的调用方负责。
	db *gorm.DB
}

// NewUserProfileRepository 使用已初始化的数据库连接创建单表仓储，不执行 SQL。
// 同库或跨库事务中直接传入 tx，全部方法都复用该事务，不切换到其他全局连接。
// 调用方负责传入有效连接；仓储不重复检查装配或领域参数的合法性。
func NewUserProfileRepository(db *gorm.DB) *UserProfileRepository {
	// NewDB 只清除 GORM 语句状态，保留原连接或事务、context 和 NowFunc 等配置；
	// 它不会新建连接池或开启事务。后续每次 Table 调用生成独立语句，避免条件串入下一次操作。
	return &UserProfileRepository{db: db.Session(&gorm.Session{NewDB: true})}
}

// GetProfileByID 按内部 ID 读取表结构，参数合法性由调用方保证。
// 查询未命中或失败时，直接返回 GORM 的原始错误，不转换为仓储错误。
// 不默认过滤停用用户；数据库中的 sql.NullString 等字段直接返回给调用方。
func (r *UserProfileRepository) GetProfileByID(id int64) (*tables.UserProfile, error) {
	var profile tables.UserProfile
	if err := r.db.Table(userProfileTable).Where("id = ?", id).Take(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetProfileByIDForUpdate 在调用方提供的事务内读取并锁定用户资料。
// 行锁由外层事务释放，供业务层在同一事务中判断状态、版本并完成更新。
func (r *UserProfileRepository) GetProfileByIDForUpdate(id int64) (*tables.UserProfile, error) {
	var profile tables.UserProfile
	if err := r.db.Table(userProfileTable).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).Take(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetProfilesByIDs 查询指定 ID 集合，不承诺数据库返回顺序，也不排除停用用户。
// 数量、去重和缺失结果的解释由业务层负责；空结果返回空切片。
func (r *UserProfileRepository) GetProfilesByIDs(ids []int64) ([]tables.UserProfile, error) {
	profiles := make([]tables.UserProfile, 0)
	if len(ids) == 0 {
		return profiles, nil
	}
	err := r.db.Table(userProfileTable).Where("id IN ?", ids).Find(&profiles).Error
	return profiles, err
}

// QueryProfilePage 按昵称精确匹配及启用状态筛选，并按 ID 降序返回指定页。
// 空昵称或 nil 状态表示不筛选；页码、每页数量和偏移量由调用方预先校验。
// 总数与页面分别读取，不承诺并发写入期间的快照一致性。
func (r *UserProfileRepository) QueryProfilePage(userName string, isEnabled *bool, pageNumber, pageSize int) ([]tables.UserProfile, int64, error) {
	query := func() *gorm.DB {
		db := r.db.Table(userProfileTable)
		if userName != "" {
			db = db.Where("user_name = ?", userName)
		}
		if isEnabled != nil {
			db = db.Where("is_enabled = ?", *isEnabled)
		}
		return db
	}
	var total int64
	if err := query().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	profiles := make([]tables.UserProfile, 0)
	err := query().Order("id DESC").Offset((pageNumber - 1) * pageSize).Limit(pageSize).Find(&profiles).Error
	return profiles, total, err
}

// GetProfileByAccount 按账户来源和外部标识的联合唯一键查询。
// 两个参数保留原文，不去空格、改大小写或转换为数字，并始终通过 SQL 参数绑定。
// 账户是否可信、非空和符合长度要求由领域模型或调用方校验。
func (r *UserProfileRepository) GetProfileByAccount(source, externalID string) (*tables.UserProfile, error) {
	var profile tables.UserProfile
	if err := r.db.Table(userProfileTable).Where("account_source = ? AND external_account_id = ?", source, externalID).Take(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// CreateProfile 直接新增表结构；ID、初始版本、字段内容等合法性由外层领域模型保证。
// IsEnabled 按传入值保存，false 不被数据库默认值替换；注册业务自行确定初始启用状态。
// AvatarURL.Valid=false 写 SQL NULL，Valid=true 则保存 String，包括空字符串。
//
// 使用独立值副本写入，失败不修改输入；成功后回填实际写入的字段和时间。
// 时间统一为 UTC 毫秒，零值使用连接的 NowFunc 填写，显式时间保留对应时刻。
// 外部事务中的成功回填不代表已经提交；事务回滚也不会自动恢复内存对象。
// 数据库或 GORM 错误原样返回，不做错误分类、包装或业务解释。
func (r *UserProfileRepository) CreateProfile(profile *tables.UserProfile) error {
	// 让 GORM 自身处理空指针，避免在构造写入副本时先解引用，也不自定义参数错误。
	if profile == nil {
		return r.db.Table(userProfileTable).Create(profile).Error
	}
	row := *profile
	now := r.db.NowFunc().UTC().Truncate(time.Millisecond)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = now
	}
	row.CreatedAt = row.CreatedAt.UTC().Truncate(time.Millisecond)
	row.UpdatedAt = row.UpdatedAt.UTC().Truncate(time.Millisecond)
	if err := r.db.Table(userProfileTable).Create(&row).Error; err != nil {
		return err
	}
	*profile = row
	return nil
}

// UpdateProfile 取 changes 的 UserName、AvatarURL 完整替换对应列，不作 PATCH 合并。
// changes 的 ID、账户身份、启用状态、Version 和时间全部忽略；定位与旧版本使用独立参数。
// 写入字段明确列出，避免传入完整表结构时误修改其他列；空昵称和 SQL NULL 同样会写入。
//
// 更新要求 ID、旧版本和 is_enabled=1 同时匹配，并递增版本、维护 UTC 毫秒修改时间。
// 这些 SQL 条件用于保证原子更新，不代替外层领域模型的参数合法性校验。
// 原样返回影响行数和 GORM 错误，调用方先检查错误，再解释行数。
// 零行且无错误可能是记录缺失、停用或版本过期，仓储不分类、不额外查询，也不自动重试。
// 外部事务中一行且无错误只表示该语句成功，最终提交仍由事务发起方负责。
// 本方法不改动传入的 changes，调用方需要最新资料时重新查询。
func (r *UserProfileRepository) UpdateProfile(id, expectedVersion int64, changes tables.UserProfile) (int64, error) {
	result := r.db.Table(userProfileTable).Model(&tables.UserProfile{}).
		Where("id = ? AND version = ? AND is_enabled = ?", id, expectedVersion, true).
		Updates(map[string]any{
			"user_name":  changes.UserName,
			"avatar_url": changes.AvatarURL,
			"version":    gorm.Expr("version + ?", 1),
			"updated_at": r.db.NowFunc().UTC().Truncate(time.Millisecond),
		})
	return result.RowsAffected, result.Error
}
