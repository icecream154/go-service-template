// Package composition 创建并保存应用共享的业务服务实例。
package composition

import (
	"errors"

	userservice "go-service-template/app/domains/user/service"
)

// current 保存构造完成的默认服务集合，nil 表示尚未初始化。
// 仅在启动阶段由单个协程写入，初始化完成后才允许并发读取，此后不再修改。
var current *Services

// Services 保存默认业务服务；发布后复用其中的实例，不替换字段。
type Services struct {
	// UserProfile 提供用户资料领域操作，不保存单次请求或事务状态。
	UserProfile *userservice.UserProfileService
}

// Init 构造并发布默认服务集合，不执行 SQL，也不创建数据库连接。
// 只允许成功发布一次；重复调用返回错误，不覆盖已有实例。
// 本函数仅供启动阶段串行调用，不能与其他 Init 或 Current 调用并发执行。
func Init() error {
	if current != nil {
		return errors.New("application services are already initialized")
	}
	current = &Services{
		UserProfile: userservice.NewUserProfileService(),
	}
	return nil
}

// Current 返回已发布的共享服务集合，Init 尚未成功时 panic。
// 此处只读取指针，不重新构造服务或改变已有服务。
// 初始化完成且默认集合不再修改后，允许多个协程同时调用。
func Current() *Services {
	if current == nil {
		panic("application services are not initialized; call Init before Current")
	}
	return current
}
