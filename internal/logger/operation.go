package logger

import (
	"go/token"
	"reflect"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// operationInfo 是入口识别得到的不可变元数据，零值表示未识别到操作。
type operationInfo struct {
	// component 是已经根据包路径与接收者职责确认的组件类别；空值表示无法识别。
	component Component
	// name 是规范化后的稳定操作标识，不包含实参、调用结果或生命周期后缀。
	name string
}

// 模块前缀来自当前包的静态类型信息，不依赖模块名称、源文件绝对路径或部署配置。
// 1. 取得 operationInfo 声明所在包的完整导入路径。
// 2. 去掉本包固定的 /internal/logger 后缀，得到本模块的导入路径。
// 3. 补上 /app/，作为后续识别允许范围；模板修改模块名后会随编译结果一起变化。
var applicationPackagePrefix = strings.TrimSuffix(reflect.TypeFor[operationInfo]().PkgPath(), "/internal/logger") + "/app/"

// operationCache 只保存二进制中函数符号的解析结果，不保存请求 Context 或业务数据。
// key 为运行时函数符号，value 固定为 operationInfo；识别成功及失败的零值均会缓存。
var operationCache sync.Map

// resolveOperation 复用函数符号的解析结果，允许多个请求并发识别同一个入口。
func resolveOperation(function string) operationInfo {
	// 1. 优先读取缓存，避免同一个入口的每次调用都重复拆分包路径和校验类型名称。
	// 缓存中的零值同样是有效结果，表示该符号已经检查过，但不符合操作入口约定。
	if cached, ok := operationCache.Load(function); ok {
		// 1.1. 缓存只由本方法写入 operationInfo，按值返回即可，不会共享可变请求状态。
		return cached.(operationInfo)
	}
	// 2. 首次遇到该函数符号时执行纯解析，只根据符号文本生成元数据，不读取当前请求。
	info := parseOperation(function)
	// 3. 通过 LoadOrStore 发布结果，处理多个 goroutine 同时首次识别同一入口的情况。
	// 已有结果时复用先发布的值，没有结果时保存本次结果；元数据发布后不再修改。
	actual, _ := operationCache.LoadOrStore(function, info)
	// 4. 返回缓存中最终采用的值，成功及失败的解析都遵循同一条并发读取路径。
	return actual.(operationInfo)
}

// parseOperation 仅接受普通具名方法，不从父调用帧推断归属。
func parseOperation(function string) operationInfo {
	// 1. 从符号末尾定位方法名前的点号，避免把模块名、包路径里的点号误当成方法分隔符。
	methodSeparator := strings.LastIndexByte(function, '.')
	if methodSeparator < 0 {
		// 1.1. 连方法分隔符都不存在时，无法建立“包路径、接收者、方法”的结构，直接识别失败。
		return operationInfo{}
	}
	// 2. 截取最后一段作为候选方法名；此处只拆分文本，不修剪或改写实际声明名称。
	method := function[methodSeparator+1:]
	// 3. 在剩余前缀末尾寻找接收者分隔符，并要求方法名是导出的普通 Go 标识符。
	// 闭包的 funcN、带 -fm 的方法值包装及包含泛型表示的名称不会被当作公开操作入口。
	receiverSeparator := strings.LastIndexByte(function[:methodSeparator], '.')
	if receiverSeparator < 0 || !exportedIdentifier(method) {
		// 3.1. 缺少接收者分隔符，或方法不符合命名约定时，保留“未识别”的零值结果。
		return operationInfo{}
	}
	// 4. 以末尾分隔符为界取得完整包路径和接收者名称，包路径中的已有点号保持原样。
	packagePath := function[:receiverSeparator]
	receiver := function[receiverSeparator+1 : methodSeparator]
	if strings.HasPrefix(receiver, "(*") && strings.HasSuffix(receiver, ")") {
		// 4.1. 指针接收者的运行时表示为 (*Type)，去掉包装后与值接收者统一使用 Type。
		// 这里只处理普通指针形式，不尝试推断泛型、闭包或其他编译器生成的复杂表示。
		receiver = receiver[2 : len(receiver)-1]
	}
	// 5. 接收者同样必须是导出标识符，包路径必须属于当前模块的 app 目录。
	// 这样既能排除现有未导出的 SDK 底座类型，也不会将第三方库的相似类型识别为本项目操作。
	if !exportedIdentifier(receiver) || !strings.HasPrefix(packagePath, applicationPackagePrefix) {
		// 5.1. 任一条件不满足即识别失败，不向上级调用帧或父操作借用名称。
		return operationInfo{}
	}
	// 6. 去掉本模块 app 前缀后按目录层级拆分，用固定目录约定判断职责。
	// 后续使用的是导入路径，不依赖源代码在开发机或部署机器上的绝对位置。
	parts := strings.Split(strings.TrimPrefix(packagePath, applicationPackagePrefix), "/")
	// 7. 识别领域组件：至少要存在 domains、领域名、职责目录三层，且领域名不能为空。
	if len(parts) >= 3 && parts[0] == "domains" && parts[1] != "" {
		// 7.1. 组件类别和类型名后缀必须成对确认，避免只凭目录就接受无关的辅助类型。
		var component Component
		var suffix string
		switch parts[2] {
		case "controller":
			// 7.2. Controller 职责目录对应 Controller 类型后缀；其版本子目录无需进入操作名称。
			component, suffix = ComponentController, "Controller"
		case "service":
			// 7.3. Service 职责目录只接受声明为业务 Service 的类型。
			component, suffix = ComponentService, "Service"
		case "repository":
			// 7.4. Repository 职责目录只接受数据库访问 Repository 类型。
			component, suffix = ComponentRepository, "Repository"
		default:
			// 7.5. models、tables、validator 等其他领域目录不在本阶段操作入口的识别范围内。
			return operationInfo{}
		}
		// 8. 类型后缀与目录职责一致后，按“领域名.类型名.方法名”构造稳定标识。
		// 只取领域名作为命名空间，目录中的版本或内部子目录不会改变约定格式。
		if strings.HasSuffix(receiver, suffix) {
			return operationInfo{component: component, name: parts[1] + "." + receiver + "." + method}
		}
	}
	// 9. 识别集成组件：必须位于 integrations 的子目录，并且接收者名称以 Client 结尾。
	if len(parts) >= 2 && parts[0] == "integrations" && strings.HasSuffix(receiver, "Client") {
		// 9.1. 把 integrations 之后的目录层级用点连接，保留服务、功能等目录名称原有拼写。
		namespace := strings.Join(parts[1:], ".")
		// 10. 显式排除 company.base.Client，避免将共享 HTTP 底座当成具体远端接口。
		// 只有命名空间与类型名同时匹配时才排除；其余符合约定的具体 Client 使用统一组件类别。
		if namespace != "company.base" || receiver != "Client" {
			return operationInfo{component: ComponentClient, name: namespace + "." + receiver + "." + method}
		}
	}
	// 11. 未知目录、职责后缀不匹配或被排除的底座均返回空结果，交由 Context 层遮蔽父操作。
	return operationInfo{}
}

// exportedIdentifier 检查普通导出标识符，排除闭包、方法值包装和泛型等生成符号。
func exportedIdentifier(name string) bool {
	// 1. 先按 Go 标识符规则检查完整名称，拒绝空字符串、关键字及括号、连字符等包装表示。
	if !token.IsIdentifier(name) {
		return false
	}
	// 2. 按 UTF-8 解出首个 Unicode 字符，而非只取第一个字节，兼容合法的非 ASCII 类型或方法名。
	// 标识符检查已经确认名称非空，因此这里可以直接检查首字符的导出属性。
	first, _ := utf8.DecodeRuneInString(name)
	// 3. Go 导出规则要求首字符属于 Unicode 大写字母类别；仅首字节为大写 ASCII 不是完整规则。
	return unicode.IsUpper(first)
}
