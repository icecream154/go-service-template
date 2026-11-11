package mysql

import (
	"errors"
	"slices"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// 错误编号及符号名来自 MySQL 8.4 Server Error Message Reference。
// https://dev.mysql.com/doc/mysql-errors/8.4/en/server-error-reference.html
const (
	errorNumberDuplicateEntry           uint16 = 1062 // ER_DUP_ENTRY
	errorNumberRowIsReferenced          uint16 = 1451 // ER_ROW_IS_REFERENCED_2
	errorNumberNoReferencedRow          uint16 = 1452 // ER_NO_REFERENCED_ROW_2
	errorNumberCheckConstraintViolated  uint16 = 3819 // ER_CHECK_CONSTRAINT_VIOLATED
	errorNumberBadNull                  uint16 = 1048 // ER_BAD_NULL_ERROR
	errorNumberNoDefaultForField        uint16 = 1364 // ER_NO_DEFAULT_FOR_FIELD
	errorNumberDataTooLong              uint16 = 1406 // ER_DATA_TOO_LONG
	errorNumberColumnValueOutOfRange    uint16 = 1264 // ER_WARN_DATA_OUT_OF_RANGE
	errorNumberDataOutOfRange           uint16 = 1690 // ER_DATA_OUT_OF_RANGE
	errorNumberTruncatedWrongValue      uint16 = 1292 // ER_TRUNCATED_WRONG_VALUE
	errorNumberTruncatedWrongFieldValue uint16 = 1366 // ER_TRUNCATED_WRONG_VALUE_FOR_FIELD

	errorNumberLockDeadlock           uint16 = 1213 // ER_LOCK_DEADLOCK
	errorNumberLockWaitTimeout        uint16 = 1205 // ER_LOCK_WAIT_TIMEOUT
	errorNumberReadOnlyTransaction    uint16 = 1792 // ER_CANT_EXECUTE_IN_READ_ONLY_TRANSACTION
	errorNumberTooManyConnections     uint16 = 1040 // ER_CON_COUNT_ERROR
	errorNumberTooManyUserConnections uint16 = 1203 // ER_TOO_MANY_USER_CONNECTIONS
	errorNumberAccessDenied           uint16 = 1045 // ER_ACCESS_DENIED_ERROR
	errorNumberAccessDeniedNoPassword uint16 = 1698 // ER_ACCESS_DENIED_NO_PASSWORD_ERROR
	errorNumberDatabaseAccessDenied   uint16 = 1044 // ER_DBACCESS_DENIED_ERROR
	errorNumberTableAccessDenied      uint16 = 1142 // ER_TABLEACCESS_DENIED_ERROR
	errorNumberColumnAccessDenied     uint16 = 1143 // ER_COLUMNACCESS_DENIED_ERROR
	errorNumberSpecificAccessDenied   uint16 = 1227 // ER_SPECIFIC_ACCESS_DENIED_ERROR
	errorNumberRoutineAccessDenied    uint16 = 1370 // ER_PROCACCESS_DENIED_ERROR

	errorNumberBadDatabase uint16 = 1049 // ER_BAD_DB_ERROR
	errorNumberBadTable    uint16 = 1051 // ER_BAD_TABLE_ERROR
	errorNumberNoSuchTable uint16 = 1146 // ER_NO_SUCH_TABLE
	errorNumberBadField    uint16 = 1054 // ER_BAD_FIELD_ERROR
	errorNumberParse       uint16 = 1064 // ER_PARSE_ERROR
	errorNumberSyntax      uint16 = 1149 // ER_SYNTAX_ERROR
)

// IsDuplicateKey 识别重复键错误，兼容 GORM 转换后的错误及 MySQL 驱动原始错误。
// 调用方根据实际违反的约束或复查结果判断业务冲突。
func IsDuplicateKey(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || hasMySQLErrorNumber(err, errorNumberDuplicateEntry)
}

// IsForeignKeyViolation 识别父记录仍被引用或子记录引用的父记录不存在。
func IsForeignKeyViolation(err error) bool {
	return errors.Is(err, gorm.ErrForeignKeyViolated) ||
		hasMySQLErrorNumber(err, errorNumberRowIsReferenced, errorNumberNoReferencedRow)
}

// IsCheckConstraintViolation 识别数据违反 CHECK 约束。
func IsCheckConstraintViolation(err error) bool {
	return errors.Is(err, gorm.ErrCheckConstraintViolated) ||
		hasMySQLErrorNumber(err, errorNumberCheckConstraintViolated)
}

// IsNotNullViolation 识别向非空列写入 NULL。
func IsNotNullViolation(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberBadNull)
}

// IsNoDefaultValue 识别操作需要某列的默认值，但该列没有默认值。
func IsNoDefaultValue(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberNoDefaultForField)
}

// IsDataTooLong 识别写入值超过列的长度限制。
func IsDataTooLong(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberDataTooLong)
}

// IsValueOutOfRange 识别列值或表达式计算结果超出数值范围。
func IsValueOutOfRange(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberColumnValueOutOfRange, errorNumberDataOutOfRange)
}

// IsValueConversionError 识别 MySQL 报告的错误值转换，包括 1292 和 1366。
func IsValueConversionError(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberTruncatedWrongValue, errorNumberTruncatedWrongFieldValue)
}

// IsDeadlock 识别 MySQL 检测到的事务死锁。
func IsDeadlock(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberLockDeadlock)
}

// IsLockWaitTimeout 识别 MySQL 报告的锁等待超时。
func IsLockWaitTimeout(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberLockWaitTimeout)
}

// IsReadOnlyTransaction 识别在只读事务中执行了不允许的语句。
func IsReadOnlyTransaction(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberReadOnlyTransaction)
}

// IsConnectionLimitExceeded 识别服务器连接数或账号并发连接数达到上限。
func IsConnectionLimitExceeded(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberTooManyConnections, errorNumberTooManyUserConnections)
}

// IsAuthenticationFailure 识别 MySQL 拒绝账号认证的 1045 或 1698 错误。
func IsAuthenticationFailure(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberAccessDenied, errorNumberAccessDeniedNoPassword)
}

// IsPermissionDenied 识别 MySQL 拒绝数据库、表、列、例程或特定操作的访问权限。
func IsPermissionDenied(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberDatabaseAccessDenied, errorNumberTableAccessDenied,
		errorNumberColumnAccessDenied, errorNumberSpecificAccessDenied, errorNumberRoutineAccessDenied)
}

// IsUnknownDatabase 识别指定的数据库不存在。
func IsUnknownDatabase(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberBadDatabase)
}

// IsTableNotFound 识别 SQL 引用的数据库表不存在。
func IsTableNotFound(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberBadTable, errorNumberNoSuchTable)
}

// IsUnknownColumn 识别 SQL 引用的列不存在。
func IsUnknownColumn(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberBadField)
}

// IsSyntaxError 识别 MySQL 报告的 SQL 语法错误。
func IsSyntaxError(err error) bool {
	return hasMySQLErrorNumber(err, errorNumberParse, errorNumberSyntax)
}

// hasMySQLErrorNumber 检查包装和聚合中的每条错误分支，同时兼容自定义 As。
// 聚合中更早出现的其他错误编号或空指针不会遮蔽后面的匹配项。
func hasMySQLErrorNumber(err error, numbers ...uint16) bool {
	for err != nil {
		switch cause := err.(type) {
		case *driver.MySQLError:
			return cause != nil && slices.Contains(numbers, cause.Number)
		case interface{ As(any) bool }:
			var mysqlError *driver.MySQLError
			if cause.As(&mysqlError) && mysqlError != nil && slices.Contains(numbers, mysqlError.Number) {
				return true
			}
		}

		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			return slices.ContainsFunc(joined.Unwrap(), func(cause error) bool {
				return hasMySQLErrorNumber(cause, numbers...)
			})
		}
		err = errors.Unwrap(err)
	}
	return false
}
