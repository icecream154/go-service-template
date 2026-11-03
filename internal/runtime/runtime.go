// Package runtime 创建并保存进程共享的数据库连接和仓储实例。
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	userrepository "go-service-template/app/domains/user/repository"
	"go-service-template/internal/database/mysql"

	"gorm.io/gorm"
)

var (
	// initMu 串行化初始化，防止重复创建连接池和覆盖已发布的实例。
	initMu sync.Mutex
	// current 只在完整初始化成功后发布，原子读取不会看到构建中的 Runtime。
	current atomic.Pointer[Runtime]
)

// Runtime 按连接和仓储分组持有应用资源。
// 使用结束后调用 Close，统一关闭其持有的连接池。
type Runtime struct {
	// DB 保存三个业务库的连接池入口。
	DB DatabaseConnections
	// Repositories 保存使用普通连接的仓储实例。
	Repositories Repositories
}

// DatabaseConnections 按业务库保存连接池入口及其管理器。
type DatabaseConnections struct {
	// User 默认访问 user_service。
	User *gorm.DB
	// Post 默认访问 post_service。
	Post *gorm.DB
	// Order 默认访问 order_service。
	Order *gorm.DB

	// manager 统一管理本次创建的全部连接池，包含配置中其他启用的命名池。
	manager *mysql.Manager
}

// Repositories 按表保存已初始化的仓储，复用对应的普通业务连接。
type Repositories struct {
	// UserProfile 读写用户资料。
	UserProfile *userrepository.UserProfileRepository
	// UserFollow 查询、建立和删除关注关系。
	UserFollow *userrepository.UserFollowRepository
	// UserMembership 查询、创建和更新当前会员权益。
	UserMembership *userrepository.UserMembershipRepository
	// MembershipChangeRecord 查询和追加会员权益变更记录。
	MembershipChangeRecord *userrepository.MembershipChangeRecordRepository
}

// Init 创建连接池、获取三个数据库入口并创建仓储，全部成功后发布默认实例。
// 每个进程只允许成功初始化一次，重复调用返回错误，不覆盖或关闭已有实例。
// 初始化失败时返回 nil，释放本次创建的连接池；默认实例仍为空，可重新尝试。
func Init(ctx context.Context, config mysql.Config) (_ *Runtime, err error) {
	initMu.Lock()
	defer initMu.Unlock()
	if current.Load() != nil {
		return nil, errors.New("runtime is already initialized")
	}

	databases, err := mysql.Open(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("initialize MySQL connections: %w", err)
	}
	// Open 成功后，后续任一步失败都在这里释放资源，并保留原始错误和关闭错误。
	// 成功返回时 err 为 nil，连接池交由返回的 Runtime 持有。
	defer func() {
		if err != nil {
			err = errors.Join(err, databases.Close())
		}
	}()

	// Get 取得现有连接池的入口，不重新创建连接池。
	runtime := &Runtime{DB: DatabaseConnections{manager: databases}}
	runtime.DB.User, err = databases.Get("user")
	if err != nil {
		return nil, fmt.Errorf("get user database: %w", err)
	}
	runtime.DB.Post, err = databases.Get("post")
	if err != nil {
		return nil, fmt.Errorf("get post database: %w", err)
	}
	runtime.DB.Order, err = databases.Get("order")
	if err != nil {
		return nil, fmt.Errorf("get order database: %w", err)
	}
	// 用户领域的四个仓储使用同一个用户库连接入口，各自建立独立的查询语句。
	runtime.Repositories = Repositories{
		UserProfile:            userrepository.NewUserProfileRepository(runtime.DB.User),
		UserFollow:             userrepository.NewUserFollowRepository(runtime.DB.User),
		UserMembership:         userrepository.NewUserMembershipRepository(runtime.DB.User),
		MembershipChangeRecord: userrepository.NewMembershipChangeRecordRepository(runtime.DB.User),
	}
	// 最后一步才公开默认实例，确保 Current 取得的连接和仓储均已就绪。
	current.Store(runtime)
	return runtime, nil
}

// Current 返回已发布的默认实例；Init 尚未成功时 panic，提示初始化顺序错误。
// 返回的是共享指针，调用方只读取 DB 和 Repositories，不替换其中的字段。
// 此函数只读取指针，不创建连接池或仓储，也不执行 SQL。
func Current() *Runtime {
	runtime := current.Load()
	if runtime == nil {
		panic("runtime is not initialized; call Init before Current")
	}
	return runtime
}

// Close 关闭本实例创建的全部连接池，并返回关闭错误。
// nil 实例或未持有管理器时直接返回；重复调用由管理器返回首次关闭结果。
// 调用方须先停止业务访问。关闭不会清空默认实例，也不允许重新初始化。
func (r *Runtime) Close() error {
	if r == nil || r.DB.manager == nil {
		return nil
	}
	return r.DB.manager.Close()
}
