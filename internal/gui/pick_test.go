package gui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 编辑框里每敲几个字就调一次 /api/rule-check，它必须只做内存校验，
// 顺带给出「样本里会变成什么」。
func TestRuleCheckValidRuleReturnsDemo(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "POST", "/api/rule-check", map[string]any{
		"rule": map[string]any{
			"id": "m", "desc": "手机号", "type": "regex",
			"pattern": `1[3-9]\d{9}`, "replace": "{stars}", "enabled": true,
		},
	}, true)

	var resp struct {
		OK    bool          `json:"ok"`
		Check ruleCheckResp `json:"check"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, rec.Body.String())
	}
	if !resp.OK || !resp.Check.Valid {
		t.Fatalf("规则应合法: %s", rec.Body.String())
	}
	if resp.Check.Hits < 1 {
		t.Errorf("样本里应命中手机号, hits=%d", resp.Check.Hits)
	}
	if resp.Check.Demo == nil {
		t.Fatal("应给出示例")
	}
	if resp.Check.Demo.Before != "13800138000" {
		t.Errorf("示例原文 = %q", resp.Check.Demo.Before)
	}
	if resp.Check.Demo.After != "***********" {
		t.Errorf("示例替换后 = %q，期望 11 个星号", resp.Check.Demo.After)
	}
}

func TestRuleCheckReportsBadRegex(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "POST", "/api/rule-check", map[string]any{
		"rule": map[string]any{"id": "x", "type": "regex", "pattern": "(", "replace": "{stars}"},
	}, true)

	var resp struct {
		OK    bool          `json:"ok"`
		Check ruleCheckResp `json:"check"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("校验接口本身应成功: %s", rec.Body.String())
	}
	if resp.Check.Valid {
		t.Fatal("非法正则不该判为合法")
	}
	if !strings.Contains(resp.Check.Error, "正则") {
		t.Errorf("错误信息应指明正则问题: %q", resp.Check.Error)
	}
}

func TestRuleCheckRejectsEmptyBody(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "POST", "/api/rule-check", map[string]any{}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("没带规则应 400, got %d", rec.Code)
	}
}

// 删除图片是结构型规则：校验接口应判合法，且不产生文本示例。
func TestRuleCheckAcceptsStripImages(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "POST", "/api/rule-check", map[string]any{
		"rule": map[string]any{"id": "noimg", "desc": "删除图片", "type": "strip_images", "enabled": true},
	}, true)
	var resp struct {
		OK    bool          `json:"ok"`
		Check ruleCheckResp `json:"check"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || !resp.Check.Valid {
		t.Fatalf("strip_images 应判合法: %s", rec.Body.String())
	}
	if resp.Check.Hits != 0 || resp.Check.Demo != nil {
		t.Errorf("结构型规则不该有文本示例: %+v", resp.Check)
	}
}

// 没命中时 rows 必须是 []，不能是 null：页面直接读 rows.length。
func TestPreviewNoHitsReturnsEmptyArray(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("干干净净\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := do(s, "POST", "/api/preview", map[string]any{
		"target": dir,
		"rules":  []map[string]any{{"id": "m", "type": "regex", "pattern": `1[3-9]\d{9}`, "replace": "{stars}"}},
	}, true)
	body := rec.Body.String()
	if strings.Contains(body, `"rows":null`) {
		t.Fatalf("没有命中时 rows 应为 []，实际是 null: %s", body)
	}
	if !strings.Contains(body, `"rows":[]`) {
		t.Errorf("没命中应返回空数组: %s", body)
	}
}

// /api/pick 的入参校验：别让错误的 mode 走到系统对话框那一步。
func TestPickValidatesMode(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "POST", "/api/pick", map[string]any{"mode": "whatever"}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(s, "GET", "/api/pick", nil, true)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405, got %d", rec.Code)
	}
}

// 页面结构是需求的一部分：4 步条、顶部目标条、底部常驻操作条、单条试跑都在模板里。
func TestPageHasNewLayout(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "GET", "/?t="+s.Token(), nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("页面应 200, got %d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{
		`id="targetPanel"`, `id="queuePanel"`, `id="paneReport"`,
		`id="stepBar"`, `id="step1"`, `id="step2"`, `id="step3"`, `id="step4"`,
		`id="actionBar"`, `id="taskCard"`, `id="taskState"`,
		`id="pickDirBtn"`, `id="pickFileBtn"`,
		`class="btn dry" id="runBtn"`, `class="btn danger" id="execBtn"`,
		`class="btn blue mini" id="clearBtn"`,
		`单条试跑`, `'checkLine'`, `'/api/rule-check'`, `'strip_images'`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("页面缺少 %s", want)
		}
	}
	if strings.Contains(page, `id="tabReport"`) {
		t.Error("标签页已换成步骤条，不该再留 tabReport")
	}
}
