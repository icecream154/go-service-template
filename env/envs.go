// Package env 管理进程级的部署环境和部署区域配置。
// 本包只负责配置读取、校验和访问；初始化失败由调用方处理并记录英文日志。
// 导入本包不会自动读取配置，也不会自动执行 Init。
//
// # 配置来源与启动顺序
//
// APP_ENV 表示部署环境，目前支持 dev、test、prod；未设置或为空字符串时默认 dev。
// APP_REGION 表示部署区域，目前仅支持 cn；未设置或为空字符串时默认 cn。
// 变量值严格匹配，不自动去除空格或转换大小写，例如 " prod "、"PROD" 均为非法值。
// 本包读取的是进程环境变量，不负责加载 .env 文件或 YAML 配置文件。
// Docker 等运行方式应单独配置，不使用 prod_docker 之类的组合环境名。
//
// 应用的初始化顺序为：main.main → bootstrap.Init → env.Init → env.LoadFromEnv。
// main 必须在初始化成功后再启动服务和业务协程，初始化失败则停止启动。
// 初始化完成后，通过 Current().IsProd()、Current().Region 等方式读取配置。
// 例如，APP_ENV=prod APP_REGION=cn go run ./cmd/web 使用生产环境和 cn 区域；
// 未设置这两个变量时，go run ./cmd/web 使用 dev 环境和 cn 区域。
//
// # 新增部署环境
//
// 以新增 stage 环境为例，需要同步完成以下修改：
//
//  1. 在环境常量中增加 StageEnv = "stage"，保持值与部署平台注入的 APP_ENV 一致。
//  2. 在 LoadFromEnv 的 environment switch 白名单中增加 StageEnv。
//     只声明常量不会使新环境自动通过校验。
//     同步 internal/database/mysql.LoadConfig、internal/snowflake.LoadConfig 的环境白名单，
//     并在 config/database/mysql、config/snowflake 中分别增加 stage.yml。
//  3. 更新非法 APP_ENV 的英文错误文案，使允许值列表包含新环境。
//  4. 如业务需要快捷判断，可增加 IsStage() 方法，直接比较 c.Environment == StageEnv。
//     现有 IsDev、IsTest、IsProd 仍只判断各自环境，不应通过排除法识别新环境。
//  5. 检查使用这些判断方法的业务分支，并按实际需要增加对应的数据库等环境配置。
//     在部署平台注入 APP_ENV=stage，并验证新值能加载、旧值仍有效、非法值仍被拒绝。
//  6. 同步更新本文件包说明、Config 字段和 LoadFromEnv 注释中的环境支持值列表。
//
// 新增环境不会改变空值默认 dev 的规则；如需调整默认值，应单独修改 LoadFromEnv。
//
// # 新增部署区域
//
// 以新增 us 区域为例，需要同步完成以下修改：
//
//  1. 在区域常量中增加 UsRegion = "us"，保持值与部署平台注入的 APP_REGION 一致。
//  2. 扩展 DataCenterID 的区域分支，为新区域分配有效的数据中心 ID。
//     可将当前 if 改为 switch，保留 cn 的现有映射，并保留未知区域返回错误的分支。
//     LoadFromEnv 使用 DataCenterID 校验区域，因此只增加区域常量仍会被拒绝。
//  3. 更新未知区域的英文错误文案，使允许值列表包含新区域。
//     如有需要，可增加 IsUsRegion() 等直接比较区域值的辅助方法。
//  4. 检查依赖区域的业务逻辑及资源配置，在部署平台注入 APP_REGION=us。
//     验证新区域加载和 ID 映射、现有区域兼容性，以及未知区域仍返回错误。
//  5. 同步更新本文件包说明、Config 字段、LoadFromEnv 和 DataCenterID 注释中的区域列表。
//
// 当前项目约定数据中心 ID 范围为 0–7，新增映射不得与已有区域重复，
// 并须确认实际使用的 Snowflake 实现支持该范围。已有映射应保持稳定，不随意改号或复用。
// 若同一区域需要多个独立数据中心，或可用 ID 已耗尽，应先调整部署标识和 ID 分配方案。
// 区域映射本身不能保证 Snowflake ID 唯一，还须结合实际组件管理节点 ID 等参数。
// 新增区域不会改变空值默认 cn 的规则；如需调整默认值，应单独修改 LoadFromEnv。
package env

import (
	"fmt"
	"os"
)

const (
	// ProdEnv 表示生产环境，对应 APP_ENV=prod。
	ProdEnv = "prod"
	// TestEnv 表示测试环境，对应 APP_ENV=test。
	TestEnv = "test"
	// DevEnv 表示开发环境，也是 APP_ENV 未设置或为空字符串时的默认值。
	DevEnv = "dev"

	// CnRegion 表示中国大陆业务区域，也是 APP_REGION 的默认值。
	// 这里的区域是业务部署维度，不表示具体云厂商的可用区。
	CnRegion = "cn"
)

// Config 保存启动时读取并校验的部署配置。
// 字段保持导出，方便调用方使用 Current 返回的副本；修改副本不会修改全局配置。
// 手动构造 Config 不会触发校验或默认值填充，应通过 LoadFromEnv 获得有效配置。
// Config 的零值也不等同于默认 dev/cn 配置。
type Config struct {
	// Environment 是已校验的环境名称，当前取值为 dev、test 或 prod。
	// 默认值只在 LoadFromEnv 读取到未设置或空字符串时生效。
	Environment string
	// Region 是已校验的业务部署区域，当前仅支持 cn。
	// 区域允许值与 DataCenterID 中的映射保持一致。
	Region string
}

// current 保存本进程使用的配置，nil 表示尚未成功初始化。
// 不导出该指针，避免外部代码直接修改共享状态。
// 本包没有为初始化写入加锁，调用方必须保证配置发布完成后才启动并发读取。
var current *Config

// Init 加载并保存当前部署配置，加载失败时保留原有配置。
//
// 此函数由 bootstrap.Init 在应用启动阶段调用，必须早于服务和业务协程启动。
// 它是需要显式调用的普通函数，不是 Go 自动执行的 init 函数。
// 所有字段校验成功后才替换 current，避免配置错误时保存不完整的结果。
// 首次初始化失败时 current 仍为 nil，调用方应将错误传回 main 并停止启动。
//
// 正常启动流程只调用一次。重复成功调用会重新读取环境变量并替换配置，
// 多个 Init 之间不能并发调用，Init 也不能与 Current 并发调用。
// 本包不支持运行中重载配置。
// 业务运行期间应只调用 Current 读取，不再调用 Init 修改。
func Init() error {
	config, err := LoadFromEnv()
	if err != nil {
		return err
	}
	current = &config
	return nil
}

// Current 返回当前部署配置的副本。
//
// 必须在 Init 成功后调用，否则会 panic，用于暴露调用顺序错误。
// 环境变量填写错误应在 Init 阶段通过 error 处理，不应依靠这里的 panic 处理。
// 配置在业务协程启动前初始化且此后不再修改时，多个协程可以同时读取。
// 初始化后修改进程环境变量，不会自动改变已保存的配置。
//
// 当前 Config 仅包含 string，返回值副本足以隔离调用方修改。
// 如果以后增加 map、slice 或指针等引用字段，需要重新处理深拷贝或只读约束，
// 避免调用方通过返回值间接修改全局配置。
func Current() Config {
	if current == nil {
		panic("env: configuration is not initialized; call Init before Current")
	}
	return *current
}

// LoadFromEnv 读取部署配置，不修改全局状态。
//
// APP_ENV 未设置或为空时默认 dev，APP_REGION 未设置或为空时默认 cn。
// 非空的 APP_ENV 必须为 dev、test 或 prod，APP_REGION 当前仅支持 cn。
// 两个字段独立应用默认值，例如仅设置 APP_ENV=prod 时，区域仍为 cn。
// 空格字符串不属于空字符串，会按非法值处理。
//
// 每次调用均重新读取进程环境变量，不修改环境变量，也不更新 current。
// 需要保存为进程共享配置时应调用 Init；只调用此函数不能使 Current 可用。
// 校验失败时返回零值 Config 和英文错误，调用方不得继续使用失败结果。
func LoadFromEnv() (Config, error) {
	environment := os.Getenv("APP_ENV")
	if environment == "" {
		environment = DevEnv
	}

	// 环境白名单必须与环境常量、下方错误文案中的允许值列表同步维护。
	switch environment {
	case DevEnv, TestEnv, ProdEnv:
	default:
		return Config{}, fmt.Errorf("invalid APP_ENV %q: expected %q, %q or %q", environment, DevEnv, TestEnv, ProdEnv)
	}

	region := os.Getenv("APP_REGION")
	if region == "" {
		region = CnRegion
	}

	config := Config{
		Environment: environment,
		Region:      region,
	}
	// 复用数据中心映射检查区域，避免维护两份可能不一致的区域白名单。
	if _, err := config.DataCenterID(); err != nil {
		return Config{}, fmt.Errorf("invalid APP_REGION: %w", err)
	}
	return config, nil
}

// IsProd 判断配置是否明确指定为生产环境。
func (c Config) IsProd() bool {
	return c.Environment == ProdEnv
}

// IsTest 判断配置是否明确指定为测试环境。
func (c Config) IsTest() bool {
	return c.Environment == TestEnv
}

// IsDev 判断配置是否明确指定为开发环境。
// 未初始化的零值或手动构造的非法环境不会被当作开发环境。
func (c Config) IsDev() bool {
	return c.Environment == DevEnv
}

// IsCnRegion 判断配置是否明确指定为 cn 区域。
func (c Config) IsCnRegion() bool {
	return c.Region == CnRegion
}

// DataCenterID 返回区域对应的 Snowflake 数据中心 ID（范围 0–7）。
// 当前仅定义 cn 对应 0，未知区域返回错误。
//
// 此映射同时是 LoadFromEnv 的区域白名单，新增区域必须扩展这里的分支。
// 本方法只检查 Region，不校验 Environment，也不会为手动构造的空区域填充默认值。
// 调用方必须先检查 error：错误返回中的 0 不能作为有效数据中心 ID 使用。
// 数据中心 ID 只是 Snowflake 参数之一，不能替代同一数据中心内的节点 ID 分配。
func (c Config) DataCenterID() (int64, error) {
	if c.IsCnRegion() {
		return 0, nil
	}
	return 0, fmt.Errorf("unsupported region %q: expected %q", c.Region, CnRegion)
}
