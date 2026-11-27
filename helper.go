package mie

import "net/http"

// headerVal 读取已规范形式的键，跳过 Header.Get 的键规范化；key 必须是框架里的常量。
func headerVal(h http.Header, key string) string {
	if values := h[key]; len(values) > 0 {
		return values[0]
	}

	return ""
}
