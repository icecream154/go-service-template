package collection_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/collection"
)

func TestMapLengthInRange(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]int
		minimum int
		maximum int
		wantErr error
	}{
		// nil map 的条目数量为零，应通过零长度闭区间。
		{name: "nil map at zero"},
		// 单个条目恰好等于下限，验证包含下端点。
		{name: "lower endpoint", values: map[string]int{"a": 0}, minimum: 1, maximum: 2},
		// 两个条目恰好等于上限，验证包含上端点。
		{name: "upper endpoint", values: map[string]int{"a": 0, "b": 1}, minimum: 1, maximum: 2},
		// 非 nil 空 map 的长度仍为零，低于下限 1。
		{name: "below minimum", values: map[string]int{}, minimum: 1, maximum: 2, wantErr: check.ErrInvalidValue},
		// 两个条目超过上限 1，应返回输入错误。
		{name: "above maximum", values: map[string]int{"a": 0, "b": 1}, maximum: 1, wantErr: check.ErrInvalidValue},
		// 下限为负时，验证数量规则本身会被拒绝。
		{name: "negative minimum", minimum: -1, maximum: 1, wantErr: check.ErrInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCollectionError(t, collection.MapLengthInRange(test.values, test.minimum, test.maximum), test.wantErr)
		})
	}
}

func TestMapNotNil(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]int
		wantErr error
	}{
		// nil map 验证尚未分配的值会被拒绝。
		{name: "nil map", wantErr: check.ErrInvalidValue},
		// 已分配的空 map 验证规则允许零个条目。
		{name: "allocated empty map", values: map[string]int{}},
		// 已有条目的 map 验证普通非 nil 输入可以通过。
		{name: "populated map", values: map[string]int{"a": 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertCollectionError(t, collection.MapNotNil(test.values), test.wantErr)
		})
	}
}

type mapKeysCase struct {
	name    string
	values  map[string]int
	keys    []string
	wantErr error
	wantKey string
}

func TestRequireKeys(t *testing.T) {
	testMapKeys(t, collection.RequireKeys[int], []mapKeysCase{
		// 无必需键时，nil map 也满足要求。
		{name: "no required keys"},
		// 键存在且值为零，验证存在性取决于键本身。
		{name: "key with zero value", values: map[string]int{"count": 0}, keys: []string{"count"}},
		// 请求顺序为 z 再 a，验证缺失键按请求顺序报告。
		{name: "first requested missing key", values: map[string]int{"present": 1}, keys: []string{"present", "z", "a"}, wantErr: check.ErrInvalidValue, wantKey: "z"},
		// nil map 缺少显式要求的空字符串键，错误应保留这个键。
		{name: "missing empty key", keys: []string{""}, wantErr: check.ErrInvalidValue, wantKey: ""},
	})
}

func TestAllowedKeys(t *testing.T) {
	testMapKeys(t, collection.AllowedKeys[int], []mapKeysCase{
		// map 为空且允许集合为空时，规则应通过。
		{name: "empty map with no allowed keys"},
		// map 只包含允许集合的子集，验证允许项可以缺失。
		{name: "subset of allowed keys", values: map[string]int{"a": 0}, keys: []string{"b", "a"}},
		// 有条目且允许集合为空，应报告该条目的键。
		{name: "nonempty map with no allowed keys", values: map[string]int{"x": 1}, wantErr: check.ErrInvalidValue, wantKey: "x"},
		// 插入顺序从 z 开始，验证在允许的 a 之后按字典序报告首个未知键 b。
		{name: "first unexpected key in lexical order", values: map[string]int{"z": 1, "b": 2, "a": 0}, keys: []string{"a"}, wantErr: check.ErrInvalidValue, wantKey: "b"},
	})
}

func TestEachMapValue(t *testing.T) {
	rejected := errors.New("callback rejected value")
	tests := []struct {
		name        string
		values      map[string]int
		nilCallback bool
		rejectValue int
		rejection   error
		wantErr     error
		wantKey     string
		wantVisited []int
	}{
		// 即使 map 为空，缺少回调仍属于规则错误。
		{name: "nil callback", nilCallback: true, wantErr: check.ErrInvalidRule},
		// nil map 配有有效回调时，验证检查通过且没有调用回调。
		{name: "nil map"},
		// 值的排列刻意与数值顺序不同，验证回调按键 a、m、z 的顺序接收值。
		{name: "values in key order", values: map[string]int{"z": 10, "m": 30, "a": 20}, wantVisited: []int{20, 30, 10}},
		// 中间键对应的值触发自定义错误，验证保留错误和键并停止遍历。
		{name: "first failing value", values: map[string]int{"z": 10, "m": 30, "a": 20}, rejectValue: 30, rejection: rejected, wantErr: rejected, wantKey: "m", wantVisited: []int{20, 30}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var visited []int
			var validate func(int) error
			if !test.nilCallback {
				validate = func(value int) error {
					visited = append(visited, value)
					if test.rejection != nil && value == test.rejectValue {
						return test.rejection
					}
					return nil
				}
			}
			err := collection.EachMapValue(test.values, validate)
			assertCollectionError(t, err, test.wantErr)
			if test.rejection != nil {
				assertCollectionKey(t, err, test.wantKey)
			}
			if !slices.Equal(visited, test.wantVisited) {
				t.Errorf("visited values = %v, want %v", visited, test.wantVisited)
			}
		})
	}
}

type mapEntry struct {
	key   string
	value int
}

func TestEachMapEntry(t *testing.T) {
	rejected := check.NewViolation("example.entry", "entry was rejected")
	tests := []struct {
		name        string
		values      map[string]int
		nilCallback bool
		rejectKey   string
		wantErr     error
		wantVisited []mapEntry
	}{
		// 提供有效 map 和 nil 回调，验证回调配置错误优先返回。
		{name: "nil callback", values: map[string]int{"a": 1}, nilCallback: true, wantErr: check.ErrInvalidRule},
		// 已分配的空 map 配有有效回调，验证没有条目时直接通过。
		{name: "empty map", values: map[string]int{}},
		// 按乱序提供三个键，验证回调按字典序接收每个键和正确的对应值。
		{name: "entries in key order", values: map[string]int{"z": 10, "m": 30, "a": 20}, wantVisited: []mapEntry{{key: "a", value: 20}, {key: "m", value: 30}, {key: "z", value: 10}}},
		// 拒绝中间键，验证保留输入错误分类和原始原因并跳过后续条目。
		{name: "first failing entry", values: map[string]int{"z": 10, "m": 30, "a": 20}, rejectKey: "m", wantErr: check.ErrInvalidValue, wantVisited: []mapEntry{{key: "a", value: 20}, {key: "m", value: 30}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var visited []mapEntry
			var validate func(string, int) error
			if !test.nilCallback {
				validate = func(key string, value int) error {
					visited = append(visited, mapEntry{key: key, value: value})
					if test.rejectKey != "" && key == test.rejectKey {
						return rejected
					}
					return nil
				}
			}
			err := collection.EachMapEntry(test.values, validate)
			assertCollectionError(t, err, test.wantErr)
			if test.rejectKey != "" {
				assertCollectionKey(t, err, test.rejectKey)
				assertCollectionError(t, err, rejected)
			}
			if !slices.Equal(visited, test.wantVisited) {
				t.Errorf("visited entries = %v, want %v", visited, test.wantVisited)
			}
		})
	}
}

func testMapKeys(t *testing.T, validate func(map[string]int, ...string) error, tests []mapKeysCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := maps.Clone(test.values)
			err := validate(test.values, test.keys...)
			assertCollectionError(t, err, test.wantErr)
			if test.wantErr != nil {
				assertCollectionKey(t, err, test.wantKey)
			}
			if !maps.Equal(test.values, before) {
				t.Errorf("values = %v, want original entries %v", test.values, before)
			}
		})
	}
}

func assertCollectionKey(t *testing.T, err error, want string) {
	t.Helper()
	var keyed *check.KeyError
	if !errors.As(err, &keyed) {
		t.Fatalf("error = %v, want *check.KeyError", err)
	}
	if keyed.Key != want {
		t.Errorf("key = %q, want %q", keyed.Key, want)
	}
}
