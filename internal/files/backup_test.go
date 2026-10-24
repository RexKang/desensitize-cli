package files

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupZipDir(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "数据")
	if err := os.MkdirAll(filepath.Join(data, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(data, "a.txt"), []byte("原始内容"), 0o644)
	_ = os.WriteFile(filepath.Join(data, "sub", "b.md"), []byte("# 标题"), 0o644)
	// 上一次的备份不该被装进新包
	_ = os.WriteFile(filepath.Join(data, "a.txt.20260101-000000.bak"), []byte("旧备份"), 0o644)

	out, err := BackupZip(data)
	if err != nil {
		t.Fatalf("整体备份失败: %v", err)
	}
	if filepath.Dir(out) != root {
		t.Errorf("备份包应落在上层目录, got %s", out)
	}
	if !strings.HasSuffix(out, ".bak.zip") {
		t.Errorf("备份包命名不对: %s", out)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	names := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(body)
	}
	if _, ok := names["数据/a.txt"]; !ok {
		t.Errorf("包里缺少 数据/a.txt，实际条目: %v", keys(names))
	}
	if _, ok := names["数据/sub/b.md"]; !ok {
		t.Errorf("包里缺少 数据/sub/b.md，实际条目: %v", keys(names))
	}
	for name := range names {
		if strings.Contains(name, ".bak") {
			t.Errorf("旧备份被装进包里了: %s", name)
		}
	}
	if names["数据/a.txt"] != "原始内容" {
		t.Errorf("内容不一致: %q", names["数据/a.txt"])
	}
}

func TestBackupZipSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	_ = os.WriteFile(path, []byte("10.1.2.3 host\n"), 0o644)

	out, err := BackupZip(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 1 || zr.File[0].Name != "hosts" {
		t.Fatalf("单文件包异常: %+v", zr.File)
	}
}

func TestBackupZipSkipsOwnTempFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.desensitize.tmp"), []byte("tmp"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "x.txt"), []byte("ok"), 0o644)

	out, err := BackupZip(dir)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.OpenReader(out)
	defer zr.Close()
	for _, f := range zr.File {
		if strings.Contains(f.Name, ".desensitize.tmp") {
			t.Fatalf("临时文件不该进备份包: %s", f.Name)
		}
	}
}

func TestIsBackupName(t *testing.T) {
	for _, name := range []string{"a.txt.20260101-101500.bak", "数据.20260101-101500.bak.zip"} {
		if !isBackupName(name) {
			t.Errorf("%s 应识别为备份文件", name)
		}
	}
	for _, name := range []string{"a.txt", "hosts", "rules.json"} {
		if isBackupName(name) {
			t.Errorf("%s 不该识别为备份文件", name)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{512: "512 B", 2048: "2.0 KB", 5 << 20: "5.0 MB"}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %s, want %s", in, got, want)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
