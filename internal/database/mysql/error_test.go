package mysql_test

import (
	"errors"
	"fmt"
	"testing"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"go-service-template/internal/database/mysql"
)

type mysqlErrorCase struct {
	name string
	err  error
	want bool
}

type mysqlErrorAdapter struct {
	driverError *driver.MySQLError
}

func TestIsDuplicateKey(t *testing.T) {
	var nilDriverError *driver.MySQLError
	tests := []mysqlErrorCase{
		// 直接传入 GORM 转换后的错误，验证通用重复键哨兵能被识别。
		{name: "GORM duplicate key", err: gorm.ErrDuplicatedKey, want: true},
		// 给 GORM 错误加上调用层上下文，验证沿错误链识别重复键。
		{name: "wrapped GORM duplicate key", err: fmt.Errorf("write record: %w", gorm.ErrDuplicatedKey), want: true},
		// 关闭 GORM 错误转换时会收到驱动错误，验证按 1062 编号识别。
		{name: "driver duplicate entry", err: &driver.MySQLError{Number: 1062}, want: true},
		// 把驱动错误和一个清理错误合并，验证能找到聚合错误中的重复键分支。
		{name: "joined driver duplicate entry", err: errors.Join(errors.New("close failed"), fmt.Errorf("write record: %w", &driver.MySQLError{Number: 1062})), want: true},
		// 聚合错误先包含外键错误，再包含重复键错误，验证前一个驱动错误不会遮蔽后一个。
		{name: "duplicate follows a different driver error", err: errors.Join(&driver.MySQLError{Number: 1452}, &driver.MySQLError{Number: 1062}), want: true},
		// 在多层包装中将空的驱动指针放在重复键之前，验证识别会跳过空指针并继续遍历。
		{
			name: "duplicate follows typed nil in nested join",
			err: errors.Join(
				&driver.MySQLError{Number: 1452},
				errors.Join(nilDriverError, fmt.Errorf("insert failed: %w", &driver.MySQLError{Number: 1062})),
			),
			want: true,
		},
		// 合并两个其他类别的驱动错误，验证遍历所有分支后仍不会报告重复键。
		{name: "joined unrelated driver errors", err: errors.Join(&driver.MySQLError{Number: 1452}, &driver.MySQLError{Number: 1213})},
		// 外键约束同样可能导致写入失败，但不能映射为重复键冲突。
		{name: "different driver number", err: &driver.MySQLError{Number: 1452}},
		// 文案即使与 GORM 哨兵相同，也要按错误身份或编号判定。
		{name: "same message with different identity", err: errors.New(gorm.ErrDuplicatedKey.Error())},
		// 错误接口非空但底层驱动指针为空时，应返回 false 并避免解引用。
		{name: "typed nil driver error", err: nilDriverError},
		// 适配器通过 As 暴露驱动错误，验证识别保留 Go 错误转换约定。
		{name: "custom As duplicate error", err: mysqlErrorAdapter{driverError: &driver.MySQLError{Number: 1062}}, want: true},
		// 适配器的 As 成功但给出空的驱动指针，验证识别会安全返回 false。
		{name: "custom As typed nil driver error", err: mysqlErrorAdapter{}},
		// 无错误时没有违反唯一约束，应返回 false。
		{name: "nil error"},
	}
	testMySQLErrorCases(t, mysql.IsDuplicateKey, tests)
}

func TestIsForeignKeyViolation(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsForeignKeyViolation, []mysqlErrorCase{
		// 有子记录引用父记录，验证 1451 会归类为外键违规。
		{name: "referenced parent row", err: &driver.MySQLError{Number: 1451}, want: true},
		// 写入子记录时找不到对应父记录，验证 1452 也会归类为外键违规。
		{name: "missing parent row", err: &driver.MySQLError{Number: 1452}, want: true},
		// GORM 已转换错误且调用方添加了上下文，验证仍可识别原外键类别。
		{name: "wrapped GORM foreign key error", err: fmt.Errorf("write record: %w", gorm.ErrForeignKeyViolated), want: true},
		// 唯一约束同样是完整性约束，验证重复键不会归入外键违规。
		{name: "duplicate entry", err: &driver.MySQLError{Number: 1062}},
	})
}

func TestIsCheckConstraintViolation(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsCheckConstraintViolation, []mysqlErrorCase{
		// 行不满足 CHECK 表达式时返回 3819，验证可按驱动编号识别。
		{name: "driver check constraint violation", err: &driver.MySQLError{Number: 3819}, want: true},
		// 接收 GORM 的通用 CHECK 错误，验证包装后仍可识别该类别。
		{name: "wrapped GORM check constraint error", err: fmt.Errorf("write record: %w", gorm.ErrCheckConstraintViolated), want: true},
		// NOT NULL 是另一种约束，验证显式 NULL 不会归入 CHECK 违规。
		{name: "null column", err: &driver.MySQLError{Number: 1048}},
	})
}

func TestIsNotNullViolation(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsNotNullViolation, []mysqlErrorCase{
		// 对不可空列显式写入 NULL，验证包装后的 1048 可被识别。
		{name: "wrapped null column error", err: fmt.Errorf("insert row: %w", &driver.MySQLError{Number: 1048}), want: true},
		// 省略无默认值的列和显式写入 NULL 由服务端报告不同错误，验证两者可区分。
		{name: "omitted column without default", err: &driver.MySQLError{Number: 1364}},
	})
}

func TestIsNoDefaultValue(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsNoDefaultValue, []mysqlErrorCase{
		// 省略没有默认值的列，验证 1364 可被识别为缺少默认值。
		{name: "omitted column without default", err: &driver.MySQLError{Number: 1364}, want: true},
		// 显式 NULL 触发不可空约束，验证它不会归入缺少默认值。
		{name: "null column", err: &driver.MySQLError{Number: 1048}},
	})
}

func TestIsDataTooLong(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsDataTooLong, []mysqlErrorCase{
		// 输入超过列长度限制，验证 1406 可被识别为数据过长。
		{name: "column data too long", err: &driver.MySQLError{Number: 1406}, want: true},
		// 数值超出列的取值范围也会拒绝写入，验证它不会归入长度错误。
		{name: "value out of range", err: &driver.MySQLError{Number: 1264}},
	})
}

func TestIsValueOutOfRange(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsValueOutOfRange, []mysqlErrorCase{
		// 数值超出列的取值范围，验证 1264 会归入数值越界。
		{name: "column value out of range", err: &driver.MySQLError{Number: 1264}, want: true},
		// 表达式结果超出其类型的取值范围，验证 1690 也会归入数值越界。
		{name: "expression value out of range", err: &driver.MySQLError{Number: 1690}, want: true},
		// 列值因格式无效而无法转换，验证它不会归入数值越界。
		{name: "invalid column value", err: &driver.MySQLError{Number: 1366}},
	})
}

func TestIsValueConversionError(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsValueConversionError, []mysqlErrorCase{
		// 表达式转换遇到无效值，验证 1292 会归入值转换错误。
		{name: "invalid value in expression", err: &driver.MySQLError{Number: 1292}, want: true},
		// 写入列时遇到无法转换的值，验证 1366 也会归入值转换错误。
		{name: "invalid value for column", err: &driver.MySQLError{Number: 1366}, want: true},
		// 可转换的值也可能超过列的存储长度，验证长度错误仍可独立识别。
		{name: "column data too long", err: &driver.MySQLError{Number: 1406}},
	})
}

func TestIsDeadlock(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsDeadlock, []mysqlErrorCase{
		// 事务上下文包装了 1213，验证调用方仍可识别死锁。
		{name: "wrapped deadlock", err: fmt.Errorf("update transaction: %w", &driver.MySQLError{Number: 1213}), want: true},
		// 事务也可能因等锁过久而失败，验证锁等待超时不会归入死锁。
		{name: "lock wait timeout", err: &driver.MySQLError{Number: 1205}},
	})
}

func TestIsLockWaitTimeout(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsLockWaitTimeout, []mysqlErrorCase{
		// 锁等待超时出现在聚合错误的第二个分支，验证可按 1205 独立识别。
		{name: "joined lock wait timeout", err: errors.Join(&driver.MySQLError{Number: 1213}, &driver.MySQLError{Number: 1205}), want: true},
		// 死锁会提前中止事务，验证它不会归入锁等待超时。
		{name: "deadlock", err: &driver.MySQLError{Number: 1213}},
	})
}

func TestIsReadOnlyTransaction(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsReadOnlyTransaction, []mysqlErrorCase{
		// 只读事务拒绝写操作，验证 1792 可被识别为只读事务错误。
		{name: "write in read-only transaction", err: &driver.MySQLError{Number: 1792}, want: true},
		// 服务端通用配置限制也能阻止执行语句，验证 1290 不会被推断为只读事务。
		{name: "statement prevented by server option", err: &driver.MySQLError{Number: 1290}},
	})
}

func TestIsConnectionLimitExceeded(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsConnectionLimitExceeded, []mysqlErrorCase{
		// 服务端全局连接数达到上限，验证可识别 1040。
		{name: "server connection limit", err: &driver.MySQLError{Number: 1040}, want: true},
		// 单个账户的连接数达到上限，验证可识别 1203。
		{name: "account connection limit", err: &driver.MySQLError{Number: 1203}, want: true},
		// 认证失败也会拒绝连接，验证它不会被归类为连接数超限。
		{name: "authentication denied", err: &driver.MySQLError{Number: 1045}},
	})
}

func TestIsAuthenticationFailure(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsAuthenticationFailure, []mysqlErrorCase{
		// 登录凭证被拒绝，验证 1045 可被识别为认证失败。
		{name: "login denied", err: &driver.MySQLError{Number: 1045}, want: true},
		// 服务端在无密码认证的场景拒绝登录时可返回 1698，验证仍归入认证失败。
		{name: "login denied without password", err: &driver.MySQLError{Number: 1698}, want: true},
		// 认证后的账户可能缺少目标数据库权限，验证权限不足不会归入认证失败。
		{name: "database permission denied", err: &driver.MySQLError{Number: 1044}},
	})
}

func TestIsPermissionDenied(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsPermissionDenied, []mysqlErrorCase{
		// 账户缺少目标数据库的权限，验证可识别 1044。
		{name: "database access denied", err: &driver.MySQLError{Number: 1044}, want: true},
		// 表级命令被拒绝，验证可识别 1142。
		{name: "table command denied", err: &driver.MySQLError{Number: 1142}, want: true},
		// 列级命令被拒绝，验证可识别 1143。
		{name: "column command denied", err: &driver.MySQLError{Number: 1143}, want: true},
		// 操作需要特定权限，验证可识别 1227。
		{name: "specific privilege required", err: &driver.MySQLError{Number: 1227}, want: true},
		// 例程命令被拒绝，验证可识别 1370。
		{name: "routine command denied", err: &driver.MySQLError{Number: 1370}, want: true},
		// 登录被拒绝时属于认证失败，验证它不会归入权限不足。
		{name: "login denied", err: &driver.MySQLError{Number: 1045}},
	})
}

func TestIsUnknownDatabase(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsUnknownDatabase, []mysqlErrorCase{
		// 选择一个不存在的数据库，验证可按 1049 识别。
		{name: "unknown database", err: &driver.MySQLError{Number: 1049}, want: true},
		// 数据库存在但账户缺少权限，验证访问拒绝仍可与数据库缺失区分。
		{name: "database access denied", err: &driver.MySQLError{Number: 1044}},
	})
}

func TestIsTableNotFound(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsTableNotFound, []mysqlErrorCase{
		// 查询引用不存在的表，验证可按 1146 识别。
		{name: "table does not exist", err: &driver.MySQLError{Number: 1146}, want: true},
		// 删除不存在的表时服务端可返回 1051，验证仍归入表缺失。
		{name: "unknown table", err: &driver.MySQLError{Number: 1051}, want: true},
		// 查询所在的数据库不存在时返回不同错误，验证它不会归入表缺失。
		{name: "unknown database", err: &driver.MySQLError{Number: 1049}},
	})
}

func TestIsUnknownColumn(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsUnknownColumn, []mysqlErrorCase{
		// 查询引用无法解析的列名，验证可按 1054 识别。
		{name: "unknown column", err: &driver.MySQLError{Number: 1054}, want: true},
		// 查询找不到整张表时返回另一类错误，验证它不会归入列缺失。
		{name: "table does not exist", err: &driver.MySQLError{Number: 1146}},
	})
}

func TestIsSyntaxError(t *testing.T) {
	testMySQLErrorCases(t, mysql.IsSyntaxError, []mysqlErrorCase{
		// SQL 解析失败时返回 1064，验证可按语法错误识别。
		{name: "SQL parse error", err: &driver.MySQLError{Number: 1064}, want: true},
		// 服务端也可用 1149 报告 SQL 语法错误，验证另一编号同样能识别。
		{name: "SQL syntax error", err: &driver.MySQLError{Number: 1149}, want: true},
		// 语句解析后无法解析列名，验证列错误不会归入语法错误。
		{name: "unknown column", err: &driver.MySQLError{Number: 1054}},
	})
}

func testMySQLErrorCases(t *testing.T, predicate func(error) bool, tests []mysqlErrorCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := predicate(test.err); got != test.want {
				t.Errorf("error classification = %t, want %t", got, test.want)
			}
		})
	}
}

func (mysqlErrorAdapter) Error() string {
	return "database error adapter"
}

func (err mysqlErrorAdapter) As(target any) bool {
	driverError, ok := target.(**driver.MySQLError)
	if !ok {
		return false
	}
	*driverError = err.driverError
	return true
}
