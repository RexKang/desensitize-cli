//go:build !windows

package main

// hideConsole 非 Windows 平台没有需要收起的控制台窗口。
func hideConsole() {}
