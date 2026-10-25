package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runBody(target string, dry bool, steps ...map[string]any) map[string]any {
	body := map[string]any{
		"target": target,
		"dry":    dry,
		"limit":  50,
		"steps":  steps,
	}
	return body
}

func step(name string, rules ...map[string]any) map[string]any {
	return map[string]any{"name": name, "rules": rules}
}

func mobileRule() map[string]any {
	return map[string]any{"id": "m", "desc": "手机号", "type": "regex", "pattern": `1[3-9]\d{9}`, "replace": "{stars}"}
}

func tagRule() map[string]any {
	return map[string]any{"id": "t", "desc": "编号", "type": "literal", "find": "TCK-", "boundary": "word", "replace": "SITE-"}
}

// 队列里两套策略按顺序跑：第二套要能看到第一套的结果。
func TestRunTwoStepsInOrder(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("手机 13800138000\n单号 TCK-2026-0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 第一步只改手机号，第二步只改编号
	rec := do(s, "POST", "/api/run", runBody(dir, true, step("手机", mobileRule()), step("编号", tagRule())), true)
	var resp struct {
		OK     bool    `json:"ok"`
		Result runResp `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, rec.Body.String())
	}
	if !resp.OK {
		t.Fatalf("空跑失败: %s", rec.Body.String())
	}
	if resp.Result.Hits != 2 {
		t.Errorf("应命中 2 处, got %d", resp.Result.Hits)
	}
	if len(resp.Result.Steps) != 2 {
		t.Fatalf("应返回两步, got %d", len(resp.Result.Steps))
	}
	if resp.Result.Steps[0].Hits != 1 || resp.Result.Steps[1].Hits != 1 {
		t.Errorf("每步应各 1 处, got %+v", resp.Result.Steps)
	}
	if resp.Result.Steps[0].Files != 1 || resp.Result.Steps[1].Files != 1 {
		t.Errorf("每步应各涉及 1 个文件, got %+v", resp.Result.Steps)
	}
	if resp.Result.HitFiles != 1 {
		t.Errorf("有命中的文件数 = %d, 期望 1", resp.Result.HitFiles)
	}
	// 空跑不许动文件
	if got, _ := os.ReadFile(file); strings.Contains(string(got), "****") {
		t.Fatal("空跑把文件改了")
	}
}

func TestRunWritesWithBackupAndReport(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("手机 13800138000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "明细.html")

	body := runBody(dir, false, step("手机", mobileRule()))
	body["backup"] = "file"
	body["report"] = reportPath
	rec := do(s, "POST", "/api/run", body, true)

	var resp struct {
		OK     bool    `json:"ok"`
		Result runResp `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, rec.Body.String())
	}
	if !resp.OK || resp.Result.Written != 1 || resp.Result.Failed != 0 {
		t.Fatalf("执行不成功: %s", rec.Body.String())
	}
	got, _ := os.ReadFile(file)
	if strings.Contains(string(got), "13800138000") {
		t.Fatalf("文件没被脱敏: %q", got)
	}
	baks, _ := filepath.Glob(file + ".*.bak")
	if len(baks) != 1 {
		t.Fatalf("应当有 1 个 .bak, got %v", baks)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("报告没生成: %v", err)
	}
}

func TestRunRejectsEmptyQueueAndBadTarget(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()

	rec := do(s, "POST", "/api/run", map[string]any{"target": dir, "dry": true, "steps": []any{}}, true)
	if !strings.Contains(rec.Body.String(), "队列是空的") {
		t.Errorf("空队列应报错: %s", rec.Body.String())
	}

	rec = do(s, "POST", "/api/run", runBody(filepath.Join(dir, "不存在"), true, step("手机", mobileRule())), true)
	if rec.Code != 400 {
		t.Errorf("不存在的路径应 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunDryDoesNotCreateBak(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(file, []byte("13800138000"), 0o644)

	body := runBody(dir, true, step("手机", mobileRule()))
	body["backup"] = "zip"
	do(s, "POST", "/api/run", body, true)

	if baks, _ := filepath.Glob(filepath.Join(dir, "*")); len(baks) != 1 {
		t.Errorf("空跑不该产生备份或其它文件: %v", baks)
	}
}

// 另存为：写到指定位置，并把后续保存切过去。
func TestSaveRulesAs(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "子目录", "我的规则.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}

	rules := []map[string]any{mobileRule()}
	rec := do(s, "POST", "/api/rules", map[string]any{"rules": rules, "baseMtime": 0, "path": dest}, true)
	var resp struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
		Err  string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK {
		t.Fatalf("另存为失败: %s", rec.Body.String())
	}
	if resp.Path != dest || s.RulesPath() != dest {
		t.Fatalf("保存位置不对: %q / %q", resp.Path, s.RulesPath())
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("文件没写出来: %v", err)
	}

	// 后续不带 path 的保存也写到这里
	rec = do(s, "POST", "/api/rules", map[string]any{"rules": rules, "baseMtime": 0}, true)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK || resp.Path != dest {
		t.Fatalf("第二次保存没跟着走: %s", rec.Body.String())
	}

	// 目录不存在要明确报错
	rec = do(s, "POST", "/api/rules", map[string]any{"rules": rules, "path": filepath.Join(dir, "没这个目录", "x.json")}, true)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.OK || !strings.Contains(resp.Err, "目录不存在") {
		t.Errorf("坏路径应报目录不存在: %s", rec.Body.String())
	}
}
