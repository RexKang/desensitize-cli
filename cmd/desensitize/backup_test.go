package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupZipMode(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "数据")
	if err := os.MkdirAll(filepath.Join(data, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(data, "a.txt"), []byte("手机 13800138000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(data, "sub", "b.txt"), []byte("IP 10.1.2.3\n"), 0o644)

	rulesPath := filepath.Join(root, "rules.json")
	_ = os.WriteFile(rulesPath, []byte(`[{"id":"m","desc":"手机","type":"regex","pattern":"1[3-9]\\d{9}","replace":"{stars}"}]`), 0o644)

	code, out := runCLI(t, data, "--yes", "--rules", rulesPath, "--backup", "zip", "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "整体备份：") {
		t.Errorf("缺少整体备份提示:\n%s", out)
	}

	archives, _ := filepath.Glob(filepath.Join(root, "数据.*.bak.zip"))
	if len(archives) != 1 {
		t.Fatalf("应当在上层目录生成 1 个备份包, 实际 %d", len(archives))
	}
	zr, err := zip.OpenReader(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["数据/a.txt"] || !names["数据/sub/b.txt"] {
		t.Fatalf("备份包内容不全: %v", names)
	}

	// zip 模式下不应该再散落 .bak
	baks, _ := filepath.Glob(filepath.Join(data, "*.bak"))
	if len(baks) != 0 {
		t.Fatalf("zip 模式不该生成逐文件备份, 发现 %v", baks)
	}
	// 原文已脱敏
	body, _ := os.ReadFile(filepath.Join(data, "a.txt"))
	if !strings.Contains(string(body), "***********") {
		t.Errorf("文件未脱敏: %q", body)
	}
}

func TestBackupFileModeIsDefault(t *testing.T) {
	fix, rs := buildFixtures(t)
	if code, out := runCLI(t, fix, "--yes", "--rules", rs, "--no-open"); code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	baks, _ := filepath.Glob(filepath.Join(fix, "*.bak"))
	if len(baks) == 0 {
		t.Fatal("默认应当生成逐文件 .bak")
	}
}

func TestBackupNoneMode(t *testing.T) {
	fix, rs := buildFixtures(t)
	code, out := runCLI(t, fix, "--yes", "--rules", rs, "--backup", "none", "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "未做备份") {
		t.Errorf("应提示未备份:\n%s", out)
	}
	baks, _ := filepath.Glob(filepath.Join(fix, "*.bak"))
	if len(baks) != 0 {
		t.Fatalf("--backup=none 不该生成备份: %v", baks)
	}
}

func TestBackupInvalidMode(t *testing.T) {
	fix, rs := buildFixtures(t)
	code, out := runCLI(t, fix, "--rules", rs, "--backup", "tar")
	if code != 2 || !strings.Contains(out, "只能是 file / zip / none") {
		t.Fatalf("非法备份方式应退出 2, got %d\n%s", code, out)
	}
}

// 备份包不能反过来被扫描到：它落在上层目录，而且名字里带 .bak。
func TestBackupZipNotScanned(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "数据")
	_ = os.MkdirAll(data, 0o755)
	_ = os.WriteFile(filepath.Join(data, "a.txt"), []byte("13800138000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "数据.20260101-000000.bak.zip"), []byte("PK\x03\x04 fake"), 0o644)

	rulesPath := filepath.Join(root, "r.json")
	_ = os.WriteFile(rulesPath, []byte(`[{"id":"m","type":"regex","pattern":"1[3-9]\\d{9}","replace":"{stars}"}]`), 0o644)

	code, out := runCLI(t, root, "--dry-run", "--rules", rulesPath, "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "bak.zip") && strings.Contains(out, "无法识别编码") {
		t.Fatalf("备份包被当成待处理文件了:\n%s", out)
	}
}

func TestHelpExitsZero(t *testing.T) {
	code, out := runCLI(t, "-h")
	if code != 0 {
		t.Fatalf("-h 应退出 0, got %d", code)
	}
	for _, want := range []string{"用法:", "--backup", "--preset", "--report"} {
		if !strings.Contains(out, want) {
			t.Errorf("帮助里缺少 %q", want)
		}
	}
}

func TestNoArgsUsageError(t *testing.T) {
	code, out := runCLI(t)
	if code != 2 || !strings.Contains(out, "至少给一个") {
		t.Fatalf("无参应退出 2, got %d\n%s", code, out)
	}
}
