package mysql

import (
	"errors"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// errorNumberDuplicateEntry 是 MySQL 官方 ER_DUP_ENTRY 错误编号，表示违反主键或唯一约束。
// 来源：https://dev.mysql.com/doc/mysql-errors/8.4/en/server-error-reference.html#error_er_dup_entry
const errorNumberDuplicateEntry uint16 = 1062

// IsDuplicateKey 识别重复键错误，兼容 GORM 转换后的错误及 MySQL 驱动原始错误。
// 使用 errors.Is/As 检查错误链；nil 或空的 MySQLError 指针返回 false。
// 本方法不修改错误，不识别具体冲突约束，也不判断账户已存在等业务含义。
func IsDuplicateKey(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var driverError *driver.MySQLError
	return errors.As(err, &driverError) && driverError != nil && driverError.Number == errorNumberDuplicateEntry
}
