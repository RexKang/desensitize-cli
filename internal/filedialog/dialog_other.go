//go:build !windows

package filedialog

// pickNative 在非 Windows 平台没有原生实现，交给界面回退到自带浏览器。
func pickNative(initial string, dir bool, title string) (string, error) {
	return "", ErrUnsupported
}
