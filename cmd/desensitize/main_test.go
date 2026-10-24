package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildFixtures 造一套最小夹具：文本 + xlsx + 嵌套 + 隐藏目录。
func buildFixtures(t *testing.T) (dir string, rulesPath string) {
	t.Helper()
	root := t.TempDir()
	fix := filepath.Join(root, "data")
	for _, d := range []string{"nested/l2", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(fix, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(fix, "a.txt"),
		[]byte("工单 TCK-2026-0001\n联系人 13800138000\n内网 10.1.2.3\n标签 AP-SRV01\n"), 0o644)
	_ = os.WriteFile(filepath.Join(fix, "nested", "l2", "b.md"),
		[]byte("| 手机 | 13900139000 |\n"), 0o644)
	_ = os.WriteFile(filepath.Join(fix, ".hidden", "skip.txt"), []byte("13800138000\n"), 0o644)

	rs := filepath.Join(root, "rules.json")
	_ = os.WriteFile(rs, []byte(`{"rules":[
	 {"id":"mobile","desc":"手机号","type":"regex","pattern":"1[3-9]\\d{9}","digit_boundary":true,"replace":"{stars}"},
	 {"id":"ip","desc":"IPv4","type":"ip_shift","first_octet":203,"offset":7,"source_cidrs":["10.0.0.0/8"]},
	 {"id":"tag","desc":"代号前缀","type":"literal","find":"AP-","boundary":"word","replace":"SITE-"},
	 {"id":"t","desc":"工单号","type":"literal","find":"TCK-","replace":""}
	]}`), 0o644)
	return fix, rs
}

func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	code := run(args, &buf, strings.NewReader("n\n"))
	return code, buf.String()
}

func TestDryRunDoesNotWrite(t *testing.T) {
	fix, rs := buildFixtures(t)
	before, _ := os.ReadFile(filepath.Join(fix, "a.txt"))

	code, out := runCLI(t, fix, "--dry-run", "--rules", rs, "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	after, _ := os.ReadFile(filepath.Join(fix, "a.txt"))
	if string(before) != string(after) {
		t.Fatal("dry-run 不该改文件")
	}
	if !strings.Contains(out, "脱敏预演") || !strings.Contains(out, "规则汇总") {
		t.Fatalf("汇总输出异常:\n%s", out)
	}
	for _, want := range []string{"手机号", "IPv4", "代号前缀", "工单号"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少规则 %s:\n%s", want, out)
		}
	}
}

func TestApplyWritesAndBacksUp(t *testing.T) {
	fix, rs := buildFixtures(t)
	code, out := runCLI(t, fix, "--yes", "--rules", rs, "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	body, _ := os.ReadFile(filepath.Join(fix, "a.txt"))
	s := string(body)
	if !strings.Contains(s, "工单 2026-0001") {
		t.Errorf("TCK- 应删除: %q", s)
	}
	if !strings.Contains(s, "***********") {
		t.Errorf("手机号应打码: %q", s)
	}
	if !strings.Contains(s, "203.8.9.10") {
		t.Errorf("IP 应位移: %q", s)
	}
	if !strings.Contains(s, "SITE-SRV01") {
		t.Errorf("前缀应替换: %q", s)
	}
	if !strings.Contains(out, "已写入") {
		t.Errorf("缺少完成提示:\n%s", out)
	}

	backs, _ := filepath.Glob(filepath.Join(fix, "*.bak"))
	if len(backs) == 0 {
		t.Fatal("应生成 .bak 备份")
	}
}

func TestNoHitsExitCode(t *testing.T) {
	_, rs := buildFixtures(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("没有敏感内容\n"), 0o644)
	code, out := runCLI(t, dir, "--rules", rs, "--no-open")
	if code != 4 {
		t.Fatalf("无命中应退出 4, got %d\n%s", code, out)
	}
}

func TestMissingTargetExitCode(t *testing.T) {
	_, rs := buildFixtures(t)
	code, _ := runCLI(t, filepath.Join(t.TempDir(), "nope"), "--rules", rs)
	if code != 1 {
		t.Fatalf("路径不存在应退出 1, got %d", code)
	}
}

func TestNoTargetExitCode(t *testing.T) {
	code, out := runCLI(t, "--dry-run")
	if code != 2 || !strings.Contains(out, "至少给一个") {
		t.Fatalf("缺目标应退出 2, got %d\n%s", code, out)
	}
}

func TestRulesNotFound(t *testing.T) {
	fix, _ := buildFixtures(t)
	code, out := runCLI(t, fix, "--dry-run", "--rules", filepath.Join(t.TempDir(), "nope.json"))
	if code != 1 || !strings.Contains(out, "规则文件") {
		t.Fatalf("规则缺失应报错退出 1, got %d\n%s", code, out)
	}
}

func TestPartialFixturesAboveAreHonest(t *testing.T) {
	// .hidden 里的号码不该被处理
	fix, rs := buildFixtures(t)
	if _, out := runCLI(t, fix, "--yes", "--rules", rs, "--no-open"); !strings.Contains(out, "已写入") {
		t.Fatalf("写入失败:\n%s", out)
	}
	body, _ := os.ReadFile(filepath.Join(fix, ".hidden", "skip.txt"))
	if string(body) != "13800138000\n" {
		t.Fatalf("隐藏目录不该被处理: %q", body)
	}
}

func TestReportHTMLWritten(t *testing.T) {
	fix, rs := buildFixtures(t)
	out := filepath.Join(t.TempDir(), "r.html")
	code, stdout := runCLI(t, fix, "--dry-run", "--rules", rs, "--report", out, "--no-open")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("报告未生成: %v", err)
	}
	html := string(body)
	for _, want := range []string{"<!doctype html>", "报告里含脱敏前的原始内容", "<mark>", "file://", "手机号"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML 缺少 %q", want)
		}
	}
}

func TestReportCSV(t *testing.T) {
	fix, rs := buildFixtures(t)
	out := filepath.Join(t.TempDir(), "r.csv")
	if code, _ := runCLI(t, fix, "--dry-run", "--rules", rs, "--report", out, "--no-open"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	body, _ := os.ReadFile(out)
	if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV 应带 UTF-8 BOM")
	}
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(body), "\uFEFF")), "\n")
	if len(lines) < 3 {
		t.Fatalf("CSV 行数不足: %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "文件,规则,位置") {
		t.Fatalf("表头异常: %q", lines[0])
	}
}

func TestPermuteAllowsFlagsAfterTargets(t *testing.T) {
	got := permute([]string{"data", "--dry-run", "--samples", "5", "b.txt"})
	want := []string{"--dry-run", "--samples", "5", "data", "b.txt"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("permute = %v, want %v", got, want)
	}
}

func TestNoOpenEnvKeepsHeadless(t *testing.T) {
	t.Setenv("DESENSITIZE_NO_BROWSER", "1")
	fix, rs := buildFixtures(t)
	out := filepath.Join(t.TempDir(), "r.html")
	code, stdout := runCLI(t, fix, "--dry-run", "--rules", rs, "--report", out)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout, "已在浏览器打开") {
		t.Fatal("禁用后不该尝试打开浏览器")
	}
	if !strings.Contains(stdout, "已禁用自动打开") {
		t.Fatalf("应提示已禁用:\n%s", stdout)
	}
}

func TestDecideOpenModes(t *testing.T) {
	t.Setenv("DESENSITIZE_NO_BROWSER", "")
	if got := decideOpen(options{noOpen: true}); got != openDisabled {
		t.Errorf("--no-open 应为 openDisabled, got %v", got)
	}
	t.Setenv("DESENSITIZE_NO_BROWSER", "1")
	if got := decideOpen(options{}); got != openDisabled {
		t.Errorf("环境变量关闭应为 openDisabled, got %v", got)
	}
	t.Setenv("DESENSITIZE_NO_BROWSER", "")
	// 测试进程的 stdout 是管道，属于非交互场景
	if got := decideOpen(options{}); got != openHeadless {
		t.Errorf("非交互环境应为 openHeadless, got %v", got)
	}
}
