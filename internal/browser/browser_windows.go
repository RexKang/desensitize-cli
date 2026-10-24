//go:build windows

package browser

import "os/exec"

// OpenFile 用系统默认浏览器打开本地文件。
// 走 rundll32 FileProtocolHandler，不会额外闪一个控制台窗口。
func OpenFile(path string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", FileURL(path)).Start()
}
