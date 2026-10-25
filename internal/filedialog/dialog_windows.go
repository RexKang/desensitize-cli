//go:build windows

package filedialog

// Windows 原生文件对话框：直接用 shell 的 IFileOpenDialog（Vista 起就是它）。
//
// 不用 PowerShell 的 FolderBrowserDialog：那要额外起一个进程，慢，而且样式是
// 老式的。这里手工把 COM 接口的 vtable 摆出来——COM 对象的头一个字是 vtable
// 指针，按 shobjidl.h 里的声明顺序取第 N 个槽位即可。槽位索引都写在常量里，
// 改的时候对照头文件数一遍。
//
// 线程要求：IFileOpenDialog 是套间线程模型，必须在 STA 线程上创建并 Show。
// 所以先 LockOSThread，再 CoInitializeEx(COINIT_APARTMENTTHREADED)。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")

	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	procGetForegroundWindow         = user32.NewProc("GetForegroundWindow")
)

// CLSID_FileOpenDialog / IID_IFileOpenDialog / IID_IShellItem
var (
	clsidFileOpenDialog = windows.GUID{
		Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE,
		Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7},
	}
	iidIFileOpenDialog = windows.GUID{
		Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768,
		Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60},
	}
	iidIShellItem = windows.GUID{
		Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE,
		Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE},
	}
)

// vtable 槽位（shobjidl.h 顺序：IUnknown 0-2 → IModalWindow.Show 3 → IFileDialog …）
const (
	idxRelease    = 2
	idxShow       = 3
	idxSetOptions = 9
	idxGetOptions = 10
	idxSetFolder  = 12
	idxSetTitle   = 17
	idxGetResult  = 20
	idxGetName    = 5 // IShellItem::GetDisplayName
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1

	// FILEOPENDIALOGOPTIONS
	fosNoChangeDir     = 0x00000008
	fosPickFolders     = 0x00000020
	fosForceFileSystem = 0x00000040
	fosPathMustExist   = 0x00000800
	fosFileMustExist   = 0x00001000
	fosDontAddToRecent = 0x02000000

	sigdnFileSysPath = 0x80058000

	hresultCancelled = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	changedMode      = 0x80010106 // RPC_E_CHANGED_MODE
	wmClose          = 0x0010
)

// comObj 是一个裸 COM 接口指针。
// 用 unsafe.Pointer 而不是 uintptr 存：uintptr 会被 GC 当成普通整数，
// 也不能参与指针运算（go vet 会直接报 possible misuse）。
type comObj struct{ p unsafe.Pointer }

// method 取 vtable 第 i 个函数的地址。
func (o comObj) method(i int) uintptr {
	vtbl := *(*unsafe.Pointer)(o.p)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
}

// call 调一个返回 HRESULT 的方法（参数里不含 this，自动补上）。
func (o comObj) call(i int, args ...uintptr) (uintptr, error) {
	if o.p == nil {
		return 0, errors.New("空的 COM 对象")
	}
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(o.p))
	all = append(all, args...)
	hr, _, _ := syscall.SyscallN(o.method(i), all...)
	if failed(hr) {
		return hr, fmt.Errorf("COM 调用 #%d 失败: 0x%08X", i, uint32(hr))
	}
	return hr, nil
}

func (o comObj) release() {
	if o.p != nil {
		syscall.SyscallN(o.method(idxRelease), uintptr(o.p))
		o.p = nil
	}
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func pickNative(initial string, dir bool, title string) (string, error) {
	// 套间线程：整个 Show 都在这条线程上跑完
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	inited := false
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	switch uint32(hr) {
	case 0, 1: // S_OK / S_FALSE
		inited = true
	case changedMode:
		// 线程已被别处初始化成 MTA，COM 会自动 marshal，照用
	default:
		return "", fmt.Errorf("初始化 COM 失败: 0x%08X", uint32(hr))
	}
	if inited {
		defer procCoUninitialize.Call()
	}

	var dlg unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&dlg)),
	)
	if failed(hr) || dlg == nil {
		return "", fmt.Errorf("创建文件对话框失败: 0x%08X", uint32(hr))
	}
	d := comObj{p: dlg}
	defer d.release()

	opts := uint32(fosNoChangeDir | fosForceFileSystem | fosPathMustExist | fosDontAddToRecent)
	if dir {
		opts |= fosPickFolders
	} else {
		opts |= fosFileMustExist
	}
	if _, err := d.call(idxSetOptions, uintptr(opts)); err != nil {
		return "", err
	}
	if tp, err := windows.UTF16PtrFromString(title); err == nil {
		// 标题只为界面好看；设不上不影响选择
		_, _ = d.call(idxSetTitle, uintptr(unsafe.Pointer(tp)))
	}
	if start := startDir(initial); start != "" {
		if item, err := newShellItem(start); err == nil {
			_, _ = d.call(idxSetFolder, uintptr(item.p))
			item.release()
		}
	}

	// 让浏览器窗口当 owner：对话框会浮在浏览器上面，而不是躲在后面
	owner, _, _ := procGetForegroundWindow.Call()
	hr, _, _ = syscall.SyscallN(d.method(idxShow), uintptr(d.p), owner)
	switch {
	case uint32(hr) == hresultCancelled:
		return "", ErrCanceled
	case failed(hr):
		return "", fmt.Errorf("对话框返回错误: 0x%08X", uint32(hr))
	}

	var item unsafe.Pointer
	if _, err := d.call(idxGetResult, uintptr(unsafe.Pointer(&item))); err != nil {
		return "", err
	}
	chosen := comObj{p: item}
	defer chosen.release()

	path, err := chosen.displayName()
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", ErrCanceled
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

// displayName 取 IShellItem 的文件系统路径（SIGDN_FILESYSPATH）。
func (o comObj) displayName() (string, error) {
	var p *uint16
	if _, err := o.call(idxGetName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	if p == nil {
		return "", nil
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p), nil
}

// newShellItem 用路径建 IShellItem，失败就当没给初始目录。
func newShellItem(path string) (comObj, error) {
	pp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return comObj{}, err
	}
	var item unsafe.Pointer
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(pp)),
		0,
		uintptr(unsafe.Pointer(&iidIShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if failed(hr) || item == nil {
		return comObj{}, fmt.Errorf("解析初始目录失败: 0x%08X", uint32(hr))
	}
	return comObj{p: item}, nil
}

// startDir 把「初始路径」规整成目录：给的是文件就退到它所在的目录。
func startDir(initial string) string {
	initial = filepath.Clean(initial)
	if initial == "" || initial == "." {
		return ""
	}
	info, err := os.Stat(initial)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return initial
	}
	return filepath.Dir(initial)
}
