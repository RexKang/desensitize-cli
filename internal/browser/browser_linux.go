//go:build linux

package browser

import (
	"os"
	"os/exec"
)

// OpenFile 图形会话下用 xdg-open；SSH/systemd/容器里没有 DISPLAY，直接跳过。
func OpenFile(path string) error {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return ErrNoGUI
	}
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return err
	}
	return exec.Command("xdg-open", FileURL(path)).Start()
}
