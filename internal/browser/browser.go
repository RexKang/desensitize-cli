// Package browser 在图形会话里用系统默认程序打开本地 HTML 报告。
//
// 打不开不算错误路径的终点：调用方一律回退成「打印路径让用户自己点」。
package browser

import (
	"errors"

	"github.com/RexKang/desensitize-cli/internal/fileurl"
)

// ErrNoGUI 表示当前没有图形会话（Linux SSH/systemd/容器）。
var ErrNoGUI = errors.New("未检测到图形环境")

// FileURL 把本地路径转成 file:// URL。
func FileURL(path string) string { return fileurl.Of(path) }
