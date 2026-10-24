//go:build windows

package main

import "golang.org/x/sys/windows"

var (
	kernel32        = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole = kernel32.NewProc("FreeConsole")
)

// hideConsole 断开控制台会话：双击进来的黑窗口消失，进程继续跑。
// 浏览器已经接手显示，留在屏幕上的控制台只会让人以为程序没退。
// 断开会话后 stdout 写不进去，所以只在这之后不再依赖终端输出。
func hideConsole() {
	_, _, _ = procFreeConsole.Call()
}
