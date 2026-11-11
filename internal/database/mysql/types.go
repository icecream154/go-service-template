// Package mysql 管理按环境加载的 MySQL 配置和各业务的独立连接池。
//
// 新增配置字段时，先在对应类型声明 YAML 标签；可插值的字符串还须加入
// expandConnection 的字段列表，再补充必要校验、客户端实际应用逻辑和相应用例。
// 仅增加 YAML 字段不会自动改变驱动或 GORM 行为，不支持的功能应明确报错。
// 新增部署环境时，同步 env 包允许值、LoadConfig 的环境分支和对应的 YAML 文件。
// 当前数据库文件只按环境选择，区域由 env 包独立管理；区域扩展不会自动切换数据库。
// 如需按区域配置端点，应先明确文件组织或变量注入规则，再扩展加载入口。
package mysql

// Config 是一个部署环境的完整数据库配置。
// 加载器负责读取和校验，连接管理器负责建池；两者都不会推测默认业务连接。
// 配置传给 Open 后应保持不变，账号、密码和完整配置不得写入日志。
type Config struct {
	// Connections 以业务名称索引连接；每个启用项独立建池，禁用项不建立连接。
	Connections map[string]ConnectionConfig `yaml:"Connections"`
	// FilePath 保存实际读取的绝对路径，仅供定位文件；不是 YAML 中的配置项。
	FilePath string `yaml:"-"`
	// environment 由 LoadConfig 设置，供 Open 复用同一环境的 TLS 校验规则。
	environment string
}

// ConnectionConfig 对应一个命名连接，当前仅支持由 Write 同时承担读和写。
// 增加业务连接时在 YAML 中增加命名项即可，加载器不写死 user/post/order。
type ConnectionConfig struct {
	// Enabled 控制是否解析该连接的占位符、校验端点以及创建连接池。
	Enabled bool `yaml:"Enabled"`
	// IsOpenReadDb 声明读写分离；首版尚未实现，设为 true 会明确报错。
	IsOpenReadDb bool `yaml:"IsOpenReadDb"`
	// SlowThreshold 是 GORM 慢 SQL 日志阈值，单位为秒，不会取消超时查询。
	SlowThreshold int `yaml:"SlowThreshold"`
	// LogLevel 供 GORM 日志器使用，允许 silent、error、warn、info。
	LogLevel string `yaml:"LogLevel"`
	// Behavior 映射 GORM 原生选项和项目行为；不支持的行为不得静默忽略。
	Behavior BehaviorConfig `yaml:"Behavior"`
	// Write 是主库端点；未启用读写分离时，普通查询也通过该端点执行。
	Write EndpointConfig `yaml:"Write"`
	// Read 为未来的只读端点预留，当前不会使用，也不展开其中的占位符。
	Read *EndpointConfig `yaml:"Read,omitempty"`
}

// BehaviorConfig 集中定义 GORM 初始化行为，不代替业务事务和受控 SQL 迁移。
type BehaviorConfig struct {
	// SkipDefaultTransaction 传给 GORM；false 保留单次写操作的默认事务。
	SkipDefaultTransaction bool `yaml:"SkipDefaultTransaction"`
	// PrepareStmt 控制 GORM 的预处理语句缓存，不代表驱动是否使用预处理协议。
	PrepareStmt bool `yaml:"PrepareStmt"`
	// SuppressRecordNotFound 是项目查询语义开关，当前只支持 false；不控制错误日志。
	SuppressRecordNotFound bool `yaml:"SuppressRecordNotFound"`
	// TranslateError 控制是否把驱动错误转换为 GORM 通用错误。
	TranslateError bool `yaml:"TranslateError"`
	// SingularTable 必须为 true，自动推导表名时使用单数；TableName 方法仍优先。
	SingularTable bool `yaml:"SingularTable"`
	// DisableForeignKeyConstraintWhenMigrating 必须为 true，阻止 GORM 自动生成外键。
	// 它不会删除已有外键，也不允许应用启动时执行迁移。
	DisableForeignKeyConstraintWhenMigrating bool `yaml:"DisableForeignKeyConstraintWhenMigrating"`
	// AutoMigrate 当前只支持 false；库表由提前执行的受控 SQL 维护。
	AutoMigrate bool `yaml:"AutoMigrate"`
	// NowFunc 声明 GORM 自动创建、更新时间的来源，不处理手动传入的事件时间。
	NowFunc NowFuncConfig `yaml:"NowFunc"`
}

// NowFuncConfig 由客户端组装为 UTC 时间函数，并截断到毫秒。
// 支付时间等手动字段仍需业务自行转换 UTC、对齐列精度。
type NowFuncConfig struct {
	// Location 当前必须为 UTC，避免自动时间依赖主机本地时区。
	Location string `yaml:"Location"`
	// Precision 当前必须为 millisecond，与项目 DATETIME(3) 列精度对齐。
	Precision string `yaml:"Precision"`
}

// EndpointConfig 描述一个端点的驱动、会话、TLS 和连接池参数。
// 所有时间数值均以秒计，转换为 time.Duration 前统一检查范围；连接数没有时间单位。
type EndpointConfig struct {
	// Host 是不含端口的主机名或 IP，客户端用它和 Port 构造网络地址。
	Host string `yaml:"Host"`
	// Port 是 TCP 端口，必须在 1–65535 范围内。
	Port int `yaml:"Port"`
	// DataBase 是该连接访问未限定表名时的默认业务库，有值时使用小写 snake_case。
	// 可以留空表示不选择默认库，适用于同一实例上的通用跨库连接；此时 SQL 必须指定库名。
	// 使用未限定表名的操作仍应配置对应业务库；仓储已指定完整库表名的操作不依赖默认库。
	DataBase string `yaml:"DataBase"`
	// User 是数据库账号，应仅具备该连接所承载流程需要的访问权限。
	// 独立连接池不要求独立账号；是否复用账号取决于实际授权范围。
	User string `yaml:"User"`
	// Pass 是数据库密码；仅用于驱动配置，不记录值，也不递归展开替换结果。
	Pass string `yaml:"Pass"`
	// Charset 当前必须为 utf8mb4，只设置连接编码，不改动已有库表。
	Charset string `yaml:"Charset"`
	// Collation 当前必须为 utf8mb4_0900_bin，只设置连接比较规则。
	Collation string `yaml:"Collation"`
	// ParseTime 必须为 true，使驱动将 DATE、DATETIME 解为 Go 时间。
	ParseTime bool `yaml:"ParseTime"`
	// Loc 必须为 UTC，控制驱动解释时间的位置，不会设置 MySQL 会话时区。
	Loc string `yaml:"Loc"`
	// MultiStatements 必须为 false；显式事务仍可分多次调用执行 SQL。
	MultiStatements bool `yaml:"MultiStatements"`
	// Session 应用于每个新建物理连接，包括连接池补充和断线重连后的连接。
	Session SessionConfig `yaml:"Session"`
	// TLS 只在启用时解析证书路径和加载证书。开发环境的本机连接可以关闭。
	TLS TLSConfig `yaml:"TLS"`
	// SetMaxOpenConns 是单命名连接、单进程的最大总连接数，必须为正数。
	SetMaxOpenConns int `yaml:"SetMaxOpenConns"`
	// SetMaxIdleConns 是最大空闲连接数，包含在总数内，允许为零且不能超过总数。
	SetMaxIdleConns int `yaml:"SetMaxIdleConns"`
	// SetConnMaxLifetime 是连接最长存活秒数，必须为正数，由 database/sql 安排回收。
	// 到期不直接打断正在执行的 SQL，也不是 SQL 操作超时。
	SetConnMaxLifetime int `yaml:"SetConnMaxLifetime"`
	// SetConnMaxIdleTime 是连接连续空闲的最长秒数，必须为正数。
	// 仅控制连接池回收空闲连接，不是 SQL 操作超时。
	SetConnMaxIdleTime int `yaml:"SetConnMaxIdleTime"`
	// ConnectTimeout 是单次网络建连超时秒数，不代替整个应用初始化的 context 时限。
	ConnectTimeout int `yaml:"ConnectTimeout"`
	// ReadTimeout 是网络读取超时秒数，不等于整个 SQL 操作的总时限。
	ReadTimeout int `yaml:"ReadTimeout"`
	// WriteTimeout 是网络写入超时秒数，完整业务操作仍需使用 context 控制。
	WriteTimeout int `yaml:"WriteTimeout"`
}

// SessionConfig 声明所有物理连接的常态，不应在单次请求后遗留特殊会话设置。
type SessionConfig struct {
	// TimeZone 必须为 +00:00；不会自动转换已有 DATETIME 中的本地时间值。
	TimeZone string `yaml:"TimeZone"`
	// TransactionIsolation 必须为 READ-COMMITTED；业务多步骤原子性仍需显式事务。
	TransactionIsolation string `yaml:"TransactionIsolation"`
	// SQLMode 必须由项目规定的六种模式组成，比较时忽略模式顺序和大小写。
	SQLMode string `yaml:"SQLMode"`
	// Autocommit 必须为 1，普通语句自动提交，仍允许显式开启事务。
	Autocommit int `yaml:"Autocommit"`
}

// TLSConfig 同时要求传输加密和服务端身份校验，不支持跳过校验或明文回退。
// Enabled=false 时其余字段不参与占位符展开、证书语义校验或证书加载。
// YAML 结构和字段类型仍统一解码检查，不因关闭 TLS 而放宽。
type TLSConfig struct {
	// Enabled 在测试、生产环境必须开启。开发可为回环地址或 host.docker.internal 关闭。
	Enabled bool `yaml:"Enabled"`
	// CAFile 是可信 CA 文件路径；相对路径按当前 YAML 所在目录解析。
	CAFile string `yaml:"CAFile"`
	// ServerName 是证书覆盖的目标主机名或 IP，不含端口，用于身份校验。
	ServerName string `yaml:"ServerName"`
	// MinVersion 允许 1.2 或 1.3，控制最低 TLS 协议版本。
	MinVersion string `yaml:"MinVersion"`
	// InsecureSkipVerify 必须为 false，保留证书链、有效期和身份校验。
	InsecureSkipVerify bool `yaml:"InsecureSkipVerify"`
	// AllowFallbackToPlaintext 必须为 false，不允许加密失败后退回明文。
	AllowFallbackToPlaintext bool `yaml:"AllowFallbackToPlaintext"`
}
