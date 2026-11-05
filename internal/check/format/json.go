package format

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// JSONKind 是 JSON 顶层值的标准类型，不是业务枚举。
type JSONKind string

const (
	JSONObject JSONKind = "object"
	JSONArray  JSONKind = "array"
	JSONString JSONKind = "string"
	JSONNumber JSONKind = "number"
	JSONBool   JSONKind = "boolean"
	JSONNull   JSONKind = "null"
	// MaximumJSONDepth 是递归检查的技术保护上限，调用方仍需明确提供自己的深度限制。
	MaximumJSONDepth = 1000
)

// JSONOptions 指定本次独立 JSON 数据检查的资源边界及表示要求。
type JSONOptions struct {
	// MaximumBytes 必须为正，表示允许的原始 UTF-8 字节数上限，包含空白。
	MaximumBytes int
	// MaximumDepth 表示容器嵌套层数；顶层对象/数组为 1，标量为 0。
	MaximumDepth int
	// RejectDuplicateKeys 为 true 时拒绝同一对象中解码后名称相同的键。
	RejectDuplicateKeys bool
	// AllowedKinds 为空时允许所有标准 JSON 类型，否则限定顶层类型。
	AllowedKinds []JSONKind
}

// ValidJSON 检查一个完整 JSON 值，拒绝尾部第二个值、非法 UTF-8 和未配对代理项。
// 不构造完整结果树；拒绝重复键时仍需保留每个对象的键集合。
func ValidJSON(value []byte, options JSONOptions) error {
	_, err := parseJSON(value, options, false)
	return err
}

// ParseJSON 返回新的 JSON 值树，数字使用 json.Number，避免隐式转换 float64 丢失精度。
// 重复键未被禁止时遵循后值覆盖前值的语义；不绑定 Request DTO。
func ParseJSON(value []byte, options JSONOptions) (any, error) {
	return parseJSON(value, options, true)
}

func parseJSON(value []byte, options JSONOptions, build bool) (any, error) {
	if options.MaximumBytes <= 0 || options.MaximumDepth < 0 || options.MaximumDepth > MaximumJSONDepth {
		return nil, check.NewRuleError("json", "byte limit must be positive and depth must be within the supported range")
	}
	for _, kind := range options.AllowedKinds {
		if kind != JSONObject && kind != JSONArray && kind != JSONString && kind != JSONNumber && kind != JSONBool && kind != JSONNull {
			return nil, check.NewRuleError("json.kind", "unknown allowed JSON kind")
		}
	}
	if len(value) > options.MaximumBytes {
		return nil, check.NewViolation("json.length", "JSON byte count exceeds the maximum")
	}
	if !utf8.Valid(value) || !jsonUnicodeEscapesValid(value) {
		return nil, check.NewViolation("json.unicode", "must contain valid Unicode text and paired surrogate escapes")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	result, kind, err := readJSONValue(decoder, 0, options, build)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, check.NewViolation("json.syntax", "must contain exactly one JSON value")
	}
	if len(options.AllowedKinds) > 0 {
		allowed := false
		for _, candidate := range options.AllowedKinds {
			allowed = allowed || candidate == kind
		}
		if !allowed {
			return nil, check.NewViolation("json.kind", "top-level JSON kind is not allowed")
		}
	}
	return result, nil
}

func readJSONValue(decoder *json.Decoder, depth int, options JSONOptions, build bool) (any, JSONKind, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, "", check.NewViolation("json.syntax", "must contain valid JSON")
	}
	switch token := token.(type) {
	case json.Delim:
		if depth >= options.MaximumDepth {
			return nil, "", check.NewViolation("json.depth", "JSON nesting exceeds the maximum")
		}
		switch token {
		case '{':
			var object map[string]any
			if build {
				object = make(map[string]any)
			}
			var keys map[string]struct{}
			if options.RejectDuplicateKeys {
				keys = make(map[string]struct{})
			}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, "", check.NewViolation("json.syntax", "must contain valid object keys")
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, "", check.NewViolation("json.syntax", "object keys must be strings")
				}
				if options.RejectDuplicateKeys {
					if _, exists := keys[key]; exists {
						return nil, "", &check.KeyError{Key: key, Err: check.NewViolation("json.duplicate_key", "object keys must be unique")}
					}
					keys[key] = struct{}{}
				}
				item, _, err := readJSONValue(decoder, depth+1, options, build)
				if err != nil {
					return nil, "", &check.KeyError{Key: key, Err: err}
				}
				if build {
					object[key] = item
				}
			}
			if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
				return nil, "", check.NewViolation("json.syntax", "must contain a complete JSON object")
			}
			return object, JSONObject, nil
		case '[':
			var array []any
			if build {
				array = make([]any, 0)
			}
			for index := 0; decoder.More(); index++ {
				item, _, err := readJSONValue(decoder, depth+1, options, build)
				if err != nil {
					return nil, "", &check.IndexError{Index: index, Err: err}
				}
				if build {
					array = append(array, item)
				}
			}
			if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
				return nil, "", check.NewViolation("json.syntax", "must contain a complete JSON array")
			}
			return array, JSONArray, nil
		}
	case nil:
		return nil, JSONNull, nil
	case string:
		return token, JSONString, nil
	case bool:
		return token, JSONBool, nil
	case json.Number:
		return token, JSONNumber, nil
	}
	return nil, "", check.NewViolation("json.syntax", "must contain a JSON value")
}

// jsonUnicodeEscapesValid 补充 encoding/json 的代理项检查，不解码或改变原始数据。
func jsonUnicodeEscapesValid(value []byte) bool {
	inString := false
	for i := 0; i < len(value); i++ {
		if value[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || value[i] != '\\' {
			continue
		}
		i++
		if i >= len(value) {
			return false
		}
		if value[i] != 'u' {
			continue
		}
		unit, ok := jsonHexUnit(value, i+1)
		if !ok {
			return false
		}
		i += 4
		if unit >= 0xDC00 && unit <= 0xDFFF {
			return false
		}
		if unit >= 0xD800 && unit <= 0xDBFF {
			if i+6 >= len(value) || value[i+1] != '\\' || value[i+2] != 'u' {
				return false
			}
			low, ok := jsonHexUnit(value, i+3)
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return !inString
}

func jsonHexUnit(value []byte, offset int) (uint16, bool) {
	if offset > len(value)-4 {
		return 0, false
	}
	var result uint16
	for _, c := range value[offset : offset+4] {
		var digit byte
		switch {
		case c >= '0' && c <= '9':
			digit = c - '0'
		case c >= 'a' && c <= 'f':
			digit = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			digit = c - 'A' + 10
		default:
			return 0, false
		}
		result = result<<4 | uint16(digit)
	}
	return result, true
}
