//go:build windows

package filedialog

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procFindWindow  = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowW")
	procPostMessage = windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW")
)

// 不弹窗的冒烟测试：只验证 COM 对象能建出来、vtable 槽位没数错。
// 槽位错一个的后果是崩溃或乱调用，所以这一步值得每次 CI 都跑。
func TestDialogObjectAndOptionsRoundTrip(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if failed(hr) && uint32(hr) != changedMode {
		t.Fatalf("CoInitializeEx 失败: 0x%08X", uint32(hr))
	}
	if !failed(hr) {
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
		t.Fatalf("创建对话框失败: 0x%08X", uint32(hr))
	}
	d := comObj{p: dlg}
	defer d.release()

	opts := uintptr(fosNoChangeDir | fosPickFolders | fosForceFileSystem)
	if _, err := d.call(idxSetOptions, opts); err != nil {
		t.Fatalf("SetOptions: %v", err)
	}
	var got uintptr
	if _, err := d.call(idxGetOptions, uintptr(unsafe.Pointer(&got))); err != nil {
		t.Fatalf("GetOptions: %v", err)
	}
	if uint32(got)&fosPickFolders == 0 {
		t.Fatalf("GetOptions 没带上 FOS_PICKFOLDERS: 0x%08X", uint32(got))
	}

	title, err := windows.UTF16PtrFromString("desensitize 测试")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.call(idxSetTitle, uintptr(unsafe.Pointer(title))); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
}

// 真弹一次对话框，由测试自己找到窗口并关掉，验证 Show → 取消 这条链路。
// 默认跳过：CI 和无人值守环境不该弹窗。手工验证时：
//
//	DESENSITIZE_DIALOG_TEST=1 go test ./internal/filedialog -run Show -v
func TestDialogShowAndCancel(t *testing.T) {
	if os.Getenv("DESENSITIZE_DIALOG_TEST") == "" {
		t.Skip("设置 DESENSITIZE_DIALOG_TEST=1 才会真的弹窗")
	}
	const title = "desensitize 对话框自检"

	done := make(chan struct{})
	go func() {
		defer close(done)
		tp, _ := windows.UTF16PtrFromString(title)
		for i := 0; i < 100; i++ {
			time.Sleep(100 * time.Millisecond)
			hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(tp)))
			if hwnd != 0 {
				_, _, _ = procPostMessage.Call(hwnd, wmClose, 0, 0)
				return
			}
		}
	}()

	start := time.Now()
	_, err := pickNative("", true, title)
	<-done

	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("关掉窗口应返回 ErrCanceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("对话框关掉后过了 %v 才返回", elapsed)
	}
}
