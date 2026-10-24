package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListPresets(t *testing.T) {
	code, out := runCLI(t, "--list-presets")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	for _, want := range []string{"内置案例库", "network", "网络配置", "高风险"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestPresetFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts") // 无扩展名，按文本处理
	if err := os.WriteFile(path, []byte("host-core01 10.1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runCLI(t, path, "--preset", "network", "--yes", "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	body, _ := os.ReadFile(path)
	s := string(body)
	if strings.Contains(s, "10.1.2.3") || !strings.Contains(s, "203.") {
		t.Errorf("IP 未位移: %q", s)
	}
	if strings.Contains(s, "host-core01") {
		t.Errorf("主机名未哈希: %q", s)
	}
	if !strings.Contains(out, "已写入") {
		t.Errorf("缺少完成提示:\n%s", out)
	}
}

func TestUnknownPreset(t *testing.T) {
	fix, _ := buildFixtures(t)
	code, out := runCLI(t, fix, "--preset", "nope")
	if code != 1 || !strings.Contains(out, "没有这个内置案例") {
		t.Fatalf("未知案例应退出 1, got %d\n%s", code, out)
	}
}
