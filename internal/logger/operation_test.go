package logger

import "testing"

type resolveOperationCase struct {
	name     string
	symbol   string
	preload  *operationInfo
	expected operationInfo
}

func TestResolveOperation(t *testing.T) {
	tests := []resolveOperationCase{
		// 首次解析符合约定的 Service 方法，验证返回结果并缓存解析所得的元数据。
		{
			name: "caches recognized symbol", symbol: applicationPackagePrefix + "domains/fixture/service.FixtureService.Lookup",
			expected: operationInfo{component: ComponentService, name: "fixture.FixtureService.Lookup"},
		},
		// 首次解析未支持的领域目录，验证缓存零值以避免后续重复解析。
		{name: "caches unrecognized symbol", symbol: applicationPackagePrefix + "domains/fixture/model.FixtureModel.Lookup"},
		// 给能识别的符号预置零值，验证缓存命中时采用已缓存的空结果。
		{name: "returns cached zero value", symbol: applicationPackagePrefix + "domains/fixture/service.FixtureService.List", preload: &operationInfo{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preserveOperationCacheEntry(t, test.symbol)
			if test.preload != nil {
				operationCache.Store(test.symbol, *test.preload)
			}

			if got := resolveOperation(test.symbol); got != test.expected {
				t.Errorf("resolveOperation(%q) = %+v, want %+v", test.symbol, got, test.expected)
			}
			cached, ok := operationCache.Load(test.symbol)
			if !ok || cached != test.expected {
				t.Errorf("cached operation = %+v, %t, want %+v, true", cached, ok, test.expected)
			}
		})
	}
}

type parseOperationCase struct {
	name     string
	symbol   string
	expected operationInfo
}

func TestParseOperation(t *testing.T) {
	tests := []parseOperationCase{
		// 指针 Controller 位于版本目录，验证去掉接收者包装且操作名不带版本。
		{
			name: "versioned controller", symbol: applicationPackagePrefix + "domains/user/controller/v1.(*UserController).GetProfile",
			expected: operationInfo{component: ComponentController, name: "user.UserController.GetProfile"},
		},
		// Service 使用值接收者，验证按原类型名构造领域操作标识。
		{
			name: "service value receiver", symbol: applicationPackagePrefix + "domains/order/service.OrderService.Create",
			expected: operationInfo{component: ComponentService, name: "order.OrderService.Create"},
		},
		// Repository 位于职责目录的子目录，验证子目录不改变领域命名空间。
		{
			name: "repository subdirectory", symbol: applicationPackagePrefix + "domains/user/repository/cache.(*UserRepository).Load",
			expected: operationInfo{component: ComponentRepository, name: "user.UserRepository.Load"},
		},
		// 集成路径含多层和连字符，验证保留原始拼写并用点连接命名空间。
		{
			name: "nested integration client", symbol: applicationPackagePrefix + "integrations/company/python-service-template/image.(*ImageClient).Generate",
			expected: operationInfo{component: ComponentClient, name: "company.python-service-template.image.ImageClient.Generate"},
		},
		// 共享底座目录下的另一个具名 Client，验证排除规则只匹配指定底座类型。
		{
			name: "different client in company base", symbol: applicationPackagePrefix + "integrations/company/base.(*SpecializedClient).Send",
			expected: operationInfo{component: ComponentClient, name: "company.base.SpecializedClient.Send"},
		},
		// 其他集成命名空间也可以声明 Client，验证底座排除规则不跨命名空间。
		{
			name: "client in another namespace", symbol: applicationPackagePrefix + "integrations/thirdparty/base.Client.Send",
			expected: operationInfo{component: ComponentClient, name: "thirdparty.base.Client.Send"},
		},
		// 共享 HTTP 底座的完整命名空间及类型命中排除条件，验证不识别为具体 Client 操作。
		{name: "excludes shared company client", symbol: applicationPackagePrefix + "integrations/company/base.(*Client).Do"},
		// 类型有 Client 后缀但方法声明在 integrations 根目录，验证没有具体命名空间时拒绝。
		{name: "missing integration namespace", symbol: applicationPackagePrefix + "integrations.RootClient.Do"},
		// 目录符合约定但类型没有 Client 后缀，验证不能仅凭集成路径识别。
		{name: "wrong integration receiver suffix", symbol: applicationPackagePrefix + "integrations/company/image.ImageHelper.Do"},
		// Service 目录里的 Repository 类型与职责不一致，验证不根据路径或类型单独归类。
		{name: "wrong domain receiver suffix", symbol: applicationPackagePrefix + "domains/user/service.UserRepository.Load"},
		// 模型属于领域代码但不在操作入口职责范围，验证忽略该目录。
		{name: "unsupported domain role", symbol: applicationPackagePrefix + "domains/user/models.UserService.Load"},
		// 空领域名造成不完整的领域命名空间，验证拒绝该路径。
		{name: "missing domain name", symbol: applicationPackagePrefix + "domains//service.UserService.Load"},
		// 缺少职责目录时即使接收者有约定后缀也不能确认组件。
		{name: "missing role directory", symbol: applicationPackagePrefix + "domains/user.UserService.Load"},
		// 第三方模块可以有相似目录和类型，验证仅识别本应用导入路径。
		{name: "different module", symbol: "example.test/another/app/domains/user/service.UserService.Load"},
		// 可导出的方法也可能属于未导出的 SDK 底座，验证拒绝其接收者。
		{name: "unexported receiver", symbol: applicationPackagePrefix + "integrations/thirdparty/ai/llm.(*openAICompatibleClient).Generate"},
		// 普通具名 Service 上的私有方法不能建立独立操作标识。
		{name: "unexported method", symbol: applicationPackagePrefix + "domains/user/service.UserService.load"},
		// 顶层函数只包含一个分隔符，验证缺少接收者时不识别。
		{name: "missing receiver separator", symbol: "service.Load"},
		// 无分隔符的符号无法拆出方法，验证返回空元数据。
		{name: "missing method separator", symbol: "Load"},
		// 闭包符号末尾是编译器生成的 func1，验证不回退到外层具名方法。
		{name: "closure", symbol: applicationPackagePrefix + "domains/user/service.UserService.Load.func1"},
		// 方法值包装符号带 -fm 后缀，验证不当作普通方法声明。
		{name: "method value wrapper", symbol: applicationPackagePrefix + "domains/user/service.(*UserService).Load-fm"},
		// 泛型接收者带类型实参表示，验证生成符号不会被当作普通具名接收者。
		{name: "generic receiver", symbol: applicationPackagePrefix + "domains/user/service.(*UserService[go.shape.int]).Load"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseOperation(test.symbol); got != test.expected {
				t.Errorf("parseOperation(%q) = %+v, want %+v", test.symbol, got, test.expected)
			}
		})
	}
}

type exportedIdentifierCase struct {
	name       string
	identifier string
	want       bool
}

func TestExportedIdentifier(t *testing.T) {
	tests := []exportedIdentifierCase{
		// 首字符大写且后面含数字和下划线，验证普通 Go 标识符可被识别。
		{name: "exported ASCII", identifier: "Read_V2", want: true},
		// 多字节的大写 Unicode 字母开头，验证按首个字符判断导出性。
		{name: "exported Unicode", identifier: "Écrire", want: true},
		// 语法合法但首字母小写，验证无法通过导出约定。
		{name: "unexported Unicode", identifier: "écrire"},
		// 下划线开头仍是合法 Go 标识符，验证不属于导出名称。
		{name: "leading underscore", identifier: "_Load"},
		// Go 关键字不能用作方法或接收者名，验证直接拒绝。
		{name: "keyword", identifier: "type"},
		// 编译器包装包含连字符，验证即使首字符大写也不是普通标识符。
		{name: "wrapper suffix", identifier: "Load-fm"},
		// 缺少名称时验证返回 false。
		{name: "empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exportedIdentifier(test.identifier); got != test.want {
				t.Errorf("exportedIdentifier(%q) = %t, want %t", test.identifier, got, test.want)
			}
		})
	}
}

func preserveOperationCacheEntry(t *testing.T, symbol string) {
	t.Helper()
	previous, existed := operationCache.LoadAndDelete(symbol)
	t.Cleanup(func() {
		if existed {
			operationCache.Store(symbol, previous)
		} else {
			operationCache.Delete(symbol)
		}
	})
}
