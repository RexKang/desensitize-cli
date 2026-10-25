// Package filedialog 调起操作系统原生的「选择目录 / 选择文件」对话框。
//
// 网页里的 <input type="file"> 拿不到本机绝对路径（浏览器只给相对路径，
// 目录更是完全不给），所以选路径这件事必须由本进程代劳。Windows 上直接
// 走 shell 的 IFileOpenDialog：就是资源管理器里那个熟悉的对话框，
// 能输地址栏、能记住上次位置、能访问网络位置。
//
// 其它平台没有实现，返回 ErrUnsupported，界面回退到自带的目录浏览器。
package filedialog

import "errors"

// ErrCanceled 表示用户点了取消。
var ErrCanceled = errors.New("已取消")

// ErrUnsupported 表示当前平台没有原生对话框实现。
var ErrUnsupported = errors.New("当前平台没有原生文件对话框")

// PickDir 让用户选一个目录，返回绝对路径。initial 是初始定位的目录或文件（可为空）。
func PickDir(initial string) (string, error) {
	return pickNative(initial, true, "选择要脱敏的目录")
}

// PickFile 让用户选一个文件，返回绝对路径。
func PickFile(initial string) (string, error) {
	return pickNative(initial, false, "选择要脱敏的文件")
}
