//go:build !windows

package main

// startedByExplorer 非 Windows 平台没有「资源管理器双击」这种启动方式。
func startedByExplorer() bool { return false }
