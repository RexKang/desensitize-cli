//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// startedByExplorer 判断进程是不是从资源管理器双击起来的。
//
// 双击时父进程是 explorer.exe；从命令行 / 脚本 / 计划任务启动时不是。
// 比「stdout 是不是字符设备」可靠：后者在 ConPTY、重定向、服务里都会误判。
func startedByExplorer() bool {
	self := uint32(os.Getpid())
	ppid := parentPID(self)
	if ppid == 0 {
		return false
	}
	name := processName(ppid)
	return strings.EqualFold(filepath.Base(name), "explorer.exe")
}

func parentPID(pid uint32) uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if entry.ProcessID == pid {
			return entry.ParentProcessID
		}
	}
	return 0
}

func processName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}
