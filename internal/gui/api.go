package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/RexKang/desensitize-cli/internal/files"
	"github.com/RexKang/desensitize-cli/internal/presets"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

// ────────────────────────────── 状态 ──────────────────────────────

type stateResp struct {
	Version     string          `json:"version"`
	RulesPath   string          `json:"rulesPath"`
	RulesMtime  int64           `json:"rulesMtime"`
	Rules       json.RawMessage `json:"rules"`
	PresetVer   string          `json:"presetVersion"`
	PresetList  []presetBrief   `json:"presets"`
	DefaultOpen bool            `json:"defaultOpen"`
}

type presetBrief struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Desc   string          `json:"desc"`
	Tags   []string        `json:"tags"`
	Risk   string          `json:"risk"`
	Note   string          `json:"note"`
	Sample string          `json:"sample"`
	Rules  json.RawMessage `json:"rules"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := stateResp{Version: s.opts.Version, RulesPath: s.opts.RulesPath}

	data, err := os.ReadFile(s.opts.RulesPath)
	if err == nil {
		if info, e := os.Stat(s.opts.RulesPath); e == nil {
			resp.RulesMtime = info.ModTime().UnixNano()
		}
		rs, _, e := rules.Parse(data)
		if e != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": "规则文件解析失败: " + e.Error()})
			return
		}
		resp.Rules = marshal(rs)
	} else {
		resp.Rules = json.RawMessage("[]")
	}

	if list, e := presets.All(); e == nil {
		resp.PresetVer = presets.Version
		for _, p := range list {
			resp.PresetList = append(resp.PresetList, presetBrief{
				ID: p.ID, Name: p.Name, Desc: p.Desc, Tags: p.Tags, Risk: p.Risk,
				Note: p.Note, Sample: p.Sample, Rules: marshal(p.Rules),
			})
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "state": resp})
}

// ────────────────────────────── 预演 ──────────────────────────────

type previewReq struct {
	Target    string          `json:"target"`
	Rules     json.RawMessage `json:"rules"`
	Recursive bool            `json:"recursive"`
	Limit     int             `json:"limit"` // 最多扫多少个文件
}

type previewRow struct {
	File   string `json:"file"`
	Loc    string `json:"loc"`
	Rule   string `json:"rule"`
	Before string `json:"before"`
	After  string `json:"after"`
	Ctx    string `json:"ctx"`
	Count  int    `json:"count"`
}

type previewResp struct {
	Files     int          `json:"files"`
	Hits      int          `json:"hits"`
	Rows      []previewRow `json:"rows"`
	Problems  []string     `json:"problems"`
	Truncated bool         `json:"truncated"`
	ElapsedMS int64        `json:"elapsedMs"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req previewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
		return
	}
	if req.Target == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "还没选目标路径"})
		return
	}
	resp, err := s.preview(req)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": resp})
}

// preview 只读：不写任何文件，xlsx 连临时文件都不落。
func (s *Server) preview(req previewReq) (previewResp, error) {
	rs, _, err := rules.Parse(req.Rules)
	if err != nil {
		return previewResp{}, fmt.Errorf("规则有问题: %w", err)
	}

	start := time.Now()
	found, problems, _ := files.Discover([]string{req.Target}, req.Recursive)
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	resp := previewResp{Problems: problems}
	stats := files.NewStats()
	for i, f := range found {
		if i >= limit {
			resp.Truncated = true
			break
		}
		var err error
		switch {
		case files.IsXLSX(f):
			_, err = files.ProcessXLSX(f, rs, stats, true)
		case files.IsDocx(f):
			_, err = files.ProcessDocx(f, rs, stats, true)
		default:
			_, err = files.ProcessText(f, rs, stats)
		}
		if err != nil {
			resp.Problems = append(resp.Problems, filepath.Base(f)+": "+err.Error())
			continue
		}
		resp.Files++
	}
	for _, d := range stats.Order {
		resp.Rows = append(resp.Rows, previewRow{
			File: displayPath(d.File), Loc: d.Loc, Rule: d.Label,
			Before: d.Before, After: d.After, Ctx: d.Ctx, Count: d.Count,
		})
		resp.Hits += d.Count
	}
	resp.ElapsedMS = time.Since(start).Milliseconds()
	return resp, nil
}

// ────────────────────────────── 保存规则 ──────────────────────────────

type saveRulesReq struct {
	Rules     json.RawMessage `json:"rules"`
	BaseMtime int64           `json:"baseMtime"` // 页面加载/上次保存时拿到的 mtime
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(s.opts.RulesPath)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": "读不到规则文件: " + err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "raw": string(data)})
	case http.MethodPost:
		var req saveRulesReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
			return
		}
		if _, _, err := rules.Parse(req.Rules); err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": "规则不合法，未保存: " + err.Error()})
			return
		}
		backup, err := s.saveRules(req)
		if err != nil {
			var conflict *conflictError
			if errors.As(err, &conflict) {
				writeJSON(w, 409, map[string]any{
					"ok": false, "conflict": true, "mtime": conflict.mtime,
					"error": "规则文件在别处被改过了，先重新加载再保存",
				})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		info, _ := os.Stat(s.opts.RulesPath)
		mt := int64(0)
		if info != nil {
			mt = info.ModTime().UnixNano()
		}
		writeJSON(w, 200, map[string]any{"ok": true, "backup": backup, "mtime": mt})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type conflictError struct{ mtime int64 }

func (e *conflictError) Error() string { return "rules.json 已被外部修改" }

// saveRules 先留备份再落盘；外部改过就报冲突，不盲覆盖。
func (s *Server) saveRules(req saveRulesReq) (string, error) {
	path := s.opts.RulesPath
	backup := ""

	if info, err := os.Stat(path); err == nil {
		if req.BaseMtime != 0 && info.ModTime().UnixNano() != req.BaseMtime {
			return "", &conflictError{mtime: info.ModTime().UnixNano()}
		}
		if old, err := os.ReadFile(path); err == nil {
			bak := fmt.Sprintf("%s.%s.bak", path, time.Now().Format("20060102-150405"))
			if err := os.WriteFile(bak, old, 0o644); err != nil {
				return "", fmt.Errorf("写备份失败: %w", err)
			}
			backup = bak
		}
	}

	var pretty any
	if err := json.Unmarshal(req.Rules, &pretty); err != nil {
		return "", fmt.Errorf("规则 JSON 有问题: %w", err)
	}
	body, err := json.MarshalIndent(map[string]any{
		"_comment": "由 desensitize gui 保存；字段含义见 rules.sample.json",
		"rules":    pretty,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	body = append(body, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return backup, nil
}

// ────────────────────────────── 心跳与退出 ──────────────────────────────

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	s.lastPing.Store(time.Now().UnixMilli())
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
	go func() {
		time.Sleep(150 * time.Millisecond) // 让响应先发出去
		_ = s.Close()
	}()
}

// ────────────────────────────── 小工具 ──────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

// displayPath 用相对当前目录的路径，界面上短一点。
func displayPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return abs
	}
	return rel
}
