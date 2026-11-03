package bootstrap

import (
	"context"
	"errors"
	"fmt"

	userrepository "go-service-template/app/domains/user/repository"
	"go-service-template/internal/database/mysql"

	"gorm.io/gorm"
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

// newRuntime 创建连接池，获取三个数据库入口，再创建用户领域的仓储。
// 初始化失败时返回 nil，并关闭本次创建的全部连接池。
func newRuntime(ctx context.Context, config mysql.Config) (_ *Runtime, err error) {
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
	return runtime, nil
}

// Close 关闭本进程创建的数据库连接池；调用前应先停止 HTTP 请求和业务任务。
//
// 此方法不负责关闭 HTTP 服务，也不负责等待业务协程结束；这些先后关系由 main.run 管理。
// nil Runtime 或尚无数据库资源时可安全调用，数据库管理器也允许重复关闭。
// 关闭错误会返回给调用方，不能只依靠 defer 丢弃错误，否则可能掩盖资源释放故障。
func (r *Runtime) Close() error {
	if r == nil || r.DB.manager == nil {
		return nil
	}
	return r.DB.manager.Close()
}
