package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"go-service-template/internal/database/mysql"

	"gorm.io/gorm"
)

// Runtime 持有应用进程使用的资源，由启动入口统一管理生命周期。
// Init 成功后，调用方即成为这些资源的持有者，必须在停止业务访问后调用 Close。
// 将资源放在 Runtime 中便于明确资源归属，避免各业务模块自行创建或关闭连接池。
type Runtime struct {
	// UserDB 是用户库的连接池入口，默认数据库为 user_service。
	UserDB *gorm.DB
	// PostDB 是内容库的连接池入口，默认数据库为 post_service。
	PostDB *gorm.DB
	// OrderDB 是订单库的连接池入口，默认数据库为 order_service。
	OrderDB *gorm.DB

	// databases 持有上述入口所属的管理器，统一关闭本次创建的全部连接池。
	databases *mysql.Manager
}

// newRuntime 创建连接池，并获取用户、内容和订单三个数据库入口。
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
	runtime := &Runtime{databases: databases}
	runtime.UserDB, err = databases.Get("user")
	if err != nil {
		return nil, fmt.Errorf("get user database: %w", err)
	}
	runtime.PostDB, err = databases.Get("post")
	if err != nil {
		return nil, fmt.Errorf("get post database: %w", err)
	}
	runtime.OrderDB, err = databases.Get("order")
	if err != nil {
		return nil, fmt.Errorf("get order database: %w", err)
	}
	return runtime, nil
}

// Close 关闭本进程创建的数据库连接池；调用前应先停止 HTTP 请求和业务任务。
//
// 此方法不负责关闭 HTTP 服务，也不负责等待业务协程结束；这些先后关系由 main.run 管理。
// nil Runtime 或尚无数据库资源时可安全调用，数据库管理器也允许重复关闭。
// 关闭错误会返回给调用方，不能只依靠 defer 丢弃错误，否则可能掩盖资源释放故障。
func (r *Runtime) Close() error {
	if r == nil || r.databases == nil {
		return nil
	}
	return r.databases.Close()
}
