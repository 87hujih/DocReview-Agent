package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
)

var ErrUnsupportedSchema = errors.New("JSON Schema keyword 不受支持由工具运行时")

type schemaNode struct {
	typeName             string
	properties           map[string]*schemaNode
	required             map[string]struct{}
	additionalProperties bool
	items                *schemaNode
	enum                 []any
	constant             any
	hasConstant          bool
	minimum              *float64
	maximum              *float64
	minLength            *int
	maxLength            *int
	minItems             *int
	maxItems             *int
	pattern              *regexp.Regexp
}

var supportedSchemaKeywords = map[string]struct{}{
	"$schema": {}, "$id": {}, "title": {}, "description": {}, "default": {}, "examples": {},
	"type": {}, "properties": {}, "required": {}, "additionalProperties": {}, "items": {},
	"enum": {}, "const": {}, "minimum": {}, "maximum": {}, "minLength": {}, "maxLength": {},
	"minItems": {}, "maxItems": {}, "pattern": {},
}

// compileSchema 执行该函数负责的核心处理逻辑。
func compileSchema(raw json.RawMessage) (*schemaNode, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("解析 JSON Schema：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("JSON Schema 必须且只能包含一个值")
	}
	object, ok := document.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON Schema 必须为一个对象")
	}
	return compileSchemaObject(object, "$schema")
}

// compileSchemaObject 执行该函数负责的核心处理逻辑。
func compileSchemaObject(object map[string]any, path string) (*schemaNode, error) {
	for keyword := range object {
		if _, supported := supportedSchemaKeywords[keyword]; !supported {
			return nil, fmt.Errorf("处理失败：%w：%s 位于 %s", ErrUnsupportedSchema, keyword, path)
		}
	}
	typeName, ok := object["type"].(string)
	if !ok || !validSchemaType(typeName) {
		return nil, fmt.Errorf("处理失败：schema 类型位于 %s 必须为一个 supported string", path)
	}
	node := &schemaNode{typeName: typeName, additionalProperties: true}

	if propertiesValue, exists := object["properties"]; exists {
		properties, ok := propertiesValue.(map[string]any)
		if !ok || typeName != "object" {
			return nil, fmt.Errorf("schema properties 位于 %s 需要对象类型", path)
		}
		node.properties = make(map[string]*schemaNode, len(properties))
		for name, childValue := range properties {
			childObject, ok := childValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("property schema %s.%s 必须为一个对象", path, name)
			}
			child, err := compileSchemaObject(childObject, path+".properties."+name)
			if err != nil {
				return nil, err
			}
			node.properties[name] = child
		}
	}
	if requiredValue, exists := object["required"]; exists {
		required, ok := stringArray(requiredValue)
		if !ok || typeName != "object" {
			return nil, fmt.Errorf("schema 必需位于 %s 必须是字符串数组用于对象类型", path)
		}
		node.required = make(map[string]struct{}, len(required))
		for _, name := range required {
			node.required[name] = struct{}{}
		}
	}
	if additional, exists := object["additionalProperties"]; exists {
		value, ok := additional.(bool)
		if !ok || typeName != "object" {
			return nil, fmt.Errorf("additionalProperties 位于 %s 必须为一个 boolean 用于对象类型", path)
		}
		node.additionalProperties = value
	}
	if itemsValue, exists := object["items"]; exists {
		itemsObject, ok := itemsValue.(map[string]any)
		if !ok || typeName != "array" {
			return nil, fmt.Errorf("items 位于 %s 必须为一个 schema 对象用于数组类型", path)
		}
		items, err := compileSchemaObject(itemsObject, path+".items")
		if err != nil {
			return nil, err
		}
		node.items = items
	}
	if enumValue, exists := object["enum"]; exists {
		enum, ok := enumValue.([]any)
		if !ok || len(enum) == 0 {
			return nil, fmt.Errorf("enum 位于 %s 必须为一个 non-空数组", path)
		}
		node.enum = enum
	}
	if constant, exists := object["const"]; exists {
		node.constant, node.hasConstant = constant, true
	}
	var err error
	if node.minimum, err = optionalFloat(object, "minimum", path); err != nil {
		return nil, err
	}
	if node.maximum, err = optionalFloat(object, "maximum", path); err != nil {
		return nil, err
	}
	if (node.minimum != nil || node.maximum != nil) && typeName != "number" && typeName != "integer" {
		return nil, fmt.Errorf("最小值/最大值位于 %s 需要 number 或 integer 类型", path)
	}
	if node.minLength, err = optionalNonNegativeInt(object, "minLength", path); err != nil {
		return nil, err
	}
	if node.maxLength, err = optionalNonNegativeInt(object, "maxLength", path); err != nil {
		return nil, err
	}
	if (node.minLength != nil || node.maxLength != nil) && typeName != "string" {
		return nil, fmt.Errorf("处理失败：minLength/maxLength 位于 %s 需要 string 类型", path)
	}
	if node.minItems, err = optionalNonNegativeInt(object, "minItems", path); err != nil {
		return nil, err
	}
	if node.maxItems, err = optionalNonNegativeInt(object, "maxItems", path); err != nil {
		return nil, err
	}
	if (node.minItems != nil || node.maxItems != nil) && typeName != "array" {
		return nil, fmt.Errorf("minitems/maxitems 位于 %s 需要数组类型", path)
	}
	if patternValue, exists := object["pattern"]; exists {
		pattern, ok := patternValue.(string)
		if !ok || typeName != "string" {
			return nil, fmt.Errorf("pattern 位于 %s 需要 string 类型", path)
		}
		node.pattern, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("无效的 pattern 位于 %s：%w", path, err)
		}
	}
	return node, nil
}

// validateJSON 校验输入及领域约束。
func (node *schemaNode) validateJSON(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("无效的 JSON：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON 值为不允许")
	}
	return node.validate(value, "$", 0)
}

// validate 校验输入及领域约束。
func (node *schemaNode) validate(value any, path string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON 值超过最大值 schema 深度位于 %s", path)
	}
	if !matchesType(node.typeName, value) {
		return fmt.Errorf("处理失败：%s 必须为 %s", path, node.typeName)
	}
	if len(node.enum) > 0 {
		matched := false
		for _, allowed := range node.enum {
			if reflect.DeepEqual(allowed, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s 为 not 一个允许的 enum 值", path)
		}
	}
	if node.hasConstant && !reflect.DeepEqual(node.constant, value) {
		return fmt.Errorf("%s 不匹配 const", path)
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch typed := value.(type) {
	case map[string]any:
		for name := range node.required {
			if _, exists := typed[name]; !exists {
				return fmt.Errorf("%s.%s 不能为空", path, name)
			}
		}
		keys := make([]string, 0, len(typed))
		for name := range typed {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			child, known := node.properties[name]
			if !known {
				if !node.additionalProperties {
					return fmt.Errorf("%s.%s 为不允许", path, name)
				}
				continue
			}
			if err := child.validate(typed[name], path+"."+name, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if node.minItems != nil && len(typed) < *node.minItems {
			return fmt.Errorf("处理失败：%s 具有 fewer 于 %d items", path, *node.minItems)
		}
		if node.maxItems != nil && len(typed) > *node.maxItems {
			return fmt.Errorf("处理失败：%s 具有更多于 %d items", path, *node.maxItems)
		}
		if node.items != nil {
			for index, item := range typed {
				if err := node.items.validate(item, fmt.Sprintf("%s[%d]", path, index), depth+1); err != nil {
					return err
				}
			}
		}
	case string:
		length := len([]rune(typed))
		if node.minLength != nil && length < *node.minLength {
			return fmt.Errorf("处理失败：%s 为更短于 %d 字符", path, *node.minLength)
		}
		if node.maxLength != nil && length > *node.maxLength {
			return fmt.Errorf("处理失败：%s 为 longer 于 %d 字符", path, *node.maxLength)
		}
		if node.pattern != nil && !node.pattern.MatchString(typed) {
			return fmt.Errorf("%s 不匹配必需 pattern", path)
		}
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return fmt.Errorf("处理失败：%s 为 not 一个有效的 number", path)
		}
		if node.minimum != nil && number < *node.minimum {
			return fmt.Errorf("%s 为 below 最小值", path)
		}
		if node.maximum != nil && number > *node.maximum {
			return fmt.Errorf("%s 为 above 最大值", path)
		}
	}
	return nil
}

// validSchemaType 执行该函数负责的核心处理逻辑。
func validSchemaType(value string) bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch value {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

// matchesType 执行该函数负责的核心处理逻辑。
func matchesType(expected string, value any) bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

// stringArray 执行该函数负责的核心处理逻辑。
func stringArray(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

// optionalFloat 执行该函数负责的核心处理逻辑。
func optionalFloat(object map[string]any, name, path string) (*float64, error) {
	value, exists := object[name]
	if !exists {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("处理失败：%s 位于 %s 必须为 numeric", name, path)
	}
	parsed, err := number.Float64()
	if err != nil {
		return nil, fmt.Errorf("处理失败：%s 位于 %s 必须为 numeric", name, path)
	}
	return &parsed, nil
}

// optionalNonNegativeInt 执行该函数负责的核心处理逻辑。
func optionalNonNegativeInt(object map[string]any, name, path string) (*int, error) {
	value, exists := object[name]
	if !exists {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("处理失败：%s 位于 %s 必须为一个 integer", name, path)
	}
	parsed, err := number.Int64()
	if err != nil || parsed < 0 {
		return nil, fmt.Errorf("%s 位于 %s 必须为一个 non-负数 integer", name, path)
	}
	converted := int(parsed)
	return &converted, nil
}
