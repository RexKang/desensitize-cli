// Package fileurl 生成 file:// URL。
//
// Windows 必须写成 file:///C:/... 三个斜杠：只给两个斜杠时浏览器与资源管理器
// 都会把它当成主机名 C: 处理，链接点了没反应。
package fileurl

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Of 把本地路径转成 file:// URL。
func Of(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
