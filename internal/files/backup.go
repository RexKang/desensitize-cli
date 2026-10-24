package files

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 备份方式。
const (
	BackupModeFile = "file" // 逐个文件留 .bak（默认）
	BackupModeZip  = "zip"  // 整个目标打包到上层目录
	BackupModeNone = "none" // 不备份（不建议）
)

// 备份文件名里必定含 .bak，扫描时靠这一条把它们全部排除。
func isBackupName(name string) bool {
	return strings.Contains(name, ".bak") || strings.HasSuffix(name, ".desensitize.tmp")
}

// BackupZip 把目标整体打包到上层目录：<名称>.<时间戳>.bak.zip。
//
// 目录会按「上层目录」为根打包，所以解压回原位置就是完整还原；
// 文件目标则单独装一个条目。上一次留下的 .bak / .bak.zip 不会被装进新包。
func BackupZip(target string) (string, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	parent := filepath.Dir(abs)
	stamp := time.Now().Format("20060102-150405")
	out := filepath.Join(parent, base+"."+stamp+".bak.zip")

	f, err := os.Create(out)
	if err != nil {
		return "", fmt.Errorf("创建备份包失败: %w", err)
	}
	zw := zip.NewWriter(f)

	add := func(path, name string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(name)
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	}

	info, err := os.Stat(abs)
	switch {
	case err != nil:
		// 交给下面统一收尾
	case info.IsDir():
		err = filepath.WalkDir(abs, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			if isBackupName(d.Name()) || p == out {
				return nil
			}
			rel, relErr := filepath.Rel(parent, p)
			if relErr != nil {
				return nil
			}
			return add(p, rel)
		})
	default:
		err = add(abs, base)
	}

	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(out)
		return "", fmt.Errorf("整体备份失败: %w", err)
	}
	return out, nil
}

// HumanSize 给提示用的可读体积。
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
