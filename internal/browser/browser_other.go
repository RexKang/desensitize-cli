//go:build !windows && !linux

package browser

import "os/exec"

// OpenFile 其余平台按 macOS 处理（open）；失败由调用方回退成打印路径。
func OpenFile(path string) error {
	return exec.Command("open", FileURL(path)).Start()
}
