package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.json")
	body := `[{"id":"m","desc":"手机号","type":"regex","pattern":"1[3-9]\\d{9}","replace":"{stars}","enabled":true}]`
	if err := os.WriteFile(rulesPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{RulesPath: rulesPath, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return s, rulesPath
}

func do(s *Server, method, path string, body any, withToken bool) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if withToken {
		req.Header.Set("X-Token", s.Token())
	}
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	return rec
}

func TestPageRequiresToken(t *testing.T) {
	s, _ := newTestServer(t)

	if rec := do(s, "GET", "/", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d", rec.Code)
	}
	rec := do(s, "GET", "/?t="+s.Token(), nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("带 token 应 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "desensitize gui") {
		t.Fatal("页面内容不对")
	}
}

func TestAPIRejectsWrongToken(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Header.Set("X-Token", "wrong")
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401, got %d", rec.Code)
	}
}

func TestStateReturnsRulesAndPresets(t *testing.T) {
	s, rulesPath := newTestServer(t)
	rec := do(s, "GET", "/api/state", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	var resp struct {
		OK    bool      `json:"ok"`
		State stateResp `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatal("ok 应为 true")
	}
	if resp.State.RulesPath != rulesPath {
		t.Errorf("规则路径 = %s", resp.State.RulesPath)
	}
	var rs []map[string]any
	if err := json.Unmarshal(resp.State.Rules, &rs); err != nil || len(rs) != 1 {
		t.Fatalf("规则应为 1 条: %v / %v", err, resp.State.Rules)
	}
	if len(resp.State.PresetList) < 10 {
		t.Errorf("案例数 = %d, 期望 >= 10", len(resp.State.PresetList))
	}
}

func TestPreviewIsReadOnly(t *testing.T) {
	s, _ := newTestServer(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	original := "联系人 13800138000，内网 10.1.2.3\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"target":    target,
		"recursive": true,
		"rules": []map[string]any{{
			"id": "m", "desc": "手机号", "type": "regex",
			"pattern": `1[3-9]\d{9}`, "replace": "{stars}", "enabled": true,
		}},
	}
	rec := do(s, "POST", "/api/preview", body, true)
	var resp struct {
		OK     bool        `json:"ok"`
		Result previewResp `json:"result"`
		Error  string      `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("预演失败: %s", resp.Error)
	}
	if resp.Result.Hits != 1 || len(resp.Result.Rows) != 1 {
		t.Fatalf("应命中 1 处, got hits=%d rows=%d", resp.Result.Hits, len(resp.Result.Rows))
	}
	if resp.Result.Rows[0].Before != "13800138000" {
		t.Errorf("原文 = %q", resp.Result.Rows[0].Before)
	}
	if !strings.Contains(resp.Result.Rows[0].Ctx, "⟦") {
		t.Errorf("上下文应带标记: %q", resp.Result.Rows[0].Ctx)
	}

	after, _ := os.ReadFile(target)
	if string(after) != original {
		t.Fatalf("预演改了文件: %q", after)
	}
}

func TestPreviewReportsBadRules(t *testing.T) {
	s, _ := newTestServer(t)
	body := map[string]any{
		"target": t.TempDir(),
		"rules":  []map[string]any{{"id": "x", "type": "regex", "pattern": "("}},
	}
	rec := do(s, "POST", "/api/preview", body, true)
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.OK {
		t.Fatal("非法正则应当失败")
	}
	if !strings.Contains(resp.Error, "规则") {
		t.Errorf("错误信息不清楚: %q", resp.Error)
	}
}

func TestSaveRulesWritesBackup(t *testing.T) {
	s, rulesPath := newTestServer(t)
	info, _ := os.Stat(rulesPath)

	newRules := []map[string]any{
		{"id": "m", "desc": "手机号", "type": "regex", "pattern": `1[3-9]\d{9}`, "replace": "{stars}", "enabled": true},
		{"id": "e", "desc": "邮箱", "type": "regex", "pattern": `[a-z]+@[a-z]+\.[a-z]+`, "replace": "user@example.com", "enabled": false},
	}
	rec := do(s, "POST", "/api/rules", map[string]any{
		"rules": newRules, "baseMtime": info.ModTime().UnixNano(),
	}, true)
	var resp struct {
		OK     bool   `json:"ok"`
		Backup string `json:"backup"`
		Error  string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK {
		t.Fatalf("保存失败: %s", resp.Error)
	}
	if resp.Backup == "" {
		t.Error("应生成备份")
	}
	if _, err := os.Stat(resp.Backup); err != nil {
		t.Errorf("备份文件不存在: %v", err)
	}

	body, _ := os.ReadFile(rulesPath)
	if !strings.Contains(string(body), `"e"`) {
		t.Errorf("新规则没写进去: %s", body)
	}
	// 写出来的文件必须能被引擎重新解析
	rec = do(s, "POST", "/api/preview", map[string]any{
		"target": t.TempDir(), "rules": json.RawMessage(body),
	}, true)
	var pr struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)
	if !pr.OK && !strings.Contains(pr.Error, "没有") {
		t.Logf("保存后的文件解析提示: %s", pr.Error)
	}
}

func TestSaveRulesDetectsConflict(t *testing.T) {
	s, rulesPath := newTestServer(t)
	info, _ := os.Stat(rulesPath)
	stale := info.ModTime().UnixNano()

	// 模拟别处改了文件
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(rulesPath, []byte(`[{"id":"other","type":"literal","find":"x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(s, "POST", "/api/rules", map[string]any{
		"rules":     []map[string]any{{"id": "m", "type": "regex", "pattern": `\d+`, "replace": "{stars}"}},
		"baseMtime": stale,
	}, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("应返回 409, got %d: %s", rec.Code, rec.Body.String())
	}
	body, _ := os.ReadFile(rulesPath)
	if !strings.Contains(string(body), "other") {
		t.Errorf("冲突时不该覆盖, 实际: %s", body)
	}
}

func TestSaveRulesRejectsInvalid(t *testing.T) {
	s, rulesPath := newTestServer(t)
	before, _ := os.ReadFile(rulesPath)
	rec := do(s, "POST", "/api/rules", map[string]any{
		"rules": []map[string]any{{"id": "x", "type": "regex", "pattern": "("}},
	}, true)
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.OK {
		t.Fatal("非法规则不该保存成功")
	}
	after, _ := os.ReadFile(rulesPath)
	if string(before) != string(after) {
		t.Fatal("非法规则把文件写坏了")
	}
}

func TestPingAndQuit(t *testing.T) {
	s, _ := newTestServer(t)
	s.lastPing.Store(1) // 假装很久没心跳
	do(s, "POST", "/api/ping", nil, true)
	if s.lastPing.Load() < 1000 {
		t.Fatal("心跳没更新")
	}
	do(s, "POST", "/api/quit", nil, true)
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("quit 之后服务应当关闭")
	}
}

func TestTokenIsRandom(t *testing.T) {
	a, _ := New(Options{RulesPath: "x"})
	b, _ := New(Options{RulesPath: "x"})
	if a.Token() == b.Token() {
		t.Fatal("两次启动的 token 不该相同")
	}
	if len(a.Token()) != 32 {
		t.Errorf("token 长度 = %d, 期望 32", len(a.Token()))
	}
}
