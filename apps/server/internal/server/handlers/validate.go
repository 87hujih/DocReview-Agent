package handlers

import "regexp"

// uuidPattern 匹配标准 UUID 格式：8-4-4-4-12 十六进制字符，不区分大小写。
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isValidUUID 判断字符串是否为合法 UUID 格式。
func isValidUUID(s string) bool {
	return uuidPattern.MatchString(s)
}