//go:build windows

package main

import "golang.org/x/sys/windows"

// init 把控制台输出代码页切到 UTF-8：中文 Windows 默认 GBK，否则表格与中文会乱码。
func init() {
	_ = windows.SetConsoleOutputCP(65001)
}
