package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/RexKang/desensitize-cli/internal/browser"
	"github.com/RexKang/desensitize-cli/internal/files"
	"github.com/RexKang/desensitize-cli/internal/report"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// stepReq 是页面传来的一条待执行策略。
type stepReq struct {
	Name  string          `json:"name"`
	Rules json.RawMessage `json:"rules"`
}

// passes 把请求里的策略列表解析成可执行的形式。
// 兼容老请求：只给 rules 不给 steps 时，当成一条策略。
func (req *runReq) passes() ([]files.Pass, error) {
	steps := req.Steps
	if len(steps) == 0 && len(req.Rules) > 0 {
		steps = []stepReq{{Name: "当前规则库", Rules: req.Rules}}
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("待执行队列是空的：先把编辑好的策略加进队列")
	}
	var out []files.Pass
	for i, st := range steps {
		rs, _, err := rules.Parse(st.Rules)
		if err != nil {
			name := st.Name
			if name == "" {
				name = fmt.Sprintf("策略%d", i+1)
			}
			return nil, fmt.Errorf("%s 的规则有问题: %w", name, err)
		}
		name := st.Name
		if name == "" {
			name = fmt.Sprintf("策略%d", i+1)
		}
		out = append(out, files.Pass{Name: name, Rules: rs})
	}
	return out, nil
}

type runReq struct {
	Target    string          `json:"target"`
	Steps     []stepReq       `json:"steps"`
	Rules     json.RawMessage `json:"rules"` // 兼容旧请求
	Recursive bool            `json:"recursive"`
	Limit     int             `json:"limit"`
	Backup    string          `json:"backup"` // zip | file | none
	Report    string          `json:"report"` // 报告路径，空则不导出
	Dry       bool            `json:"dry"`    // true=空跑（默认），false=真写
	Open      bool            `json:"open"`   // 执行后打开报告
}

type stepResult struct {
	Name   string `json:"name"`
	Rules  int    `json:"rules"`
	Active int    `json:"active"`
	Hits   int    `json:"hits"`
	Files  int    `json:"files"`
}

type runResp struct {
	Dry       bool         `json:"dry"`
	Files     int          `json:"files"`
	HitFiles  int          `json:"hitFiles"` // 有命中的文件数（和 rows 里的文件去重一致）
	Hits      int          `json:"hits"`
	Written   int          `json:"written"`
	Failed    int          `json:"failed"`
	Backup    string       `json:"backup"`
	Report    string       `json:"report"`
	Steps     []stepResult `json:"steps"`
	Problems  []string     `json:"problems"`
	Truncated bool         `json:"truncated"`
	ElapsedMS int64        `json:"elapsedMs"`
	Rows      []previewRow `json:"rows"`
}

// handleRun 执行（或空跑）整条队列。
//
// dry=true 时不碰任何文件；dry=false 时按 backup 设置备份后原地写盘。
// 页面只有在确认弹窗点过「执行」之后才会发 dry=false。
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
		return
	}
	if req.Target == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "还没选目标路径"})
		return
	}
	if info, err := os.Stat(req.Target); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "目标路径不存在: " + req.Target})
		return
	} else if !info.IsDir() && !info.Mode().IsRegular() {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "目标既不是文件也不是目录"})
		return
	}

	passes, err := req.passes()
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	mode := req.Backup
	switch mode {
	case files.BackupModeZip, files.BackupModeFile, files.BackupModeNone:
	case "":
		mode = files.BackupModeFile
	default:
		writeJSON(w, 400, map[string]any{"ok": false, "error": "--backup 只能是 zip / file / none"})
		return
	}

	resp, err := s.run(req, passes, mode)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": resp})
}

func (s *Server) run(req runReq, passes []files.Pass, mode string) (runResp, error) {
	start := time.Now()
	found, problems, _ := files.Discover([]string{req.Target}, req.Recursive)
	limit := req.Limit
	if limit <= 0 || limit > 5000 {
		limit = 200
	}

	resp := runResp{Dry: req.Dry, Problems: problems, Steps: []stepResult{}, Rows: []previewRow{}}
	for _, ps := range passes {
		active := 0
		for _, r := range ps.Rules {
			if r.IsEnabled() {
				active++
			}
		}
		resp.Steps = append(resp.Steps, stepResult{Name: ps.Name, Rules: len(ps.Rules), Active: active})
	}

	stats := files.NewStats()
	var pending []*files.Pending
	var records []report.FileRecord

	for i, f := range found {
		if i >= limit {
			resp.Truncated = true
			break
		}
		var pd *files.Pending
		var err error
		switch {
		case files.IsXLSX(f):
			pd, err = files.ProcessXLSXPasses(f, passes, stats, req.Dry)
		case files.IsDocx(f):
			pd, err = files.ProcessDocxPasses(f, passes, stats, req.Dry)
		default:
			pd, err = files.ProcessTextPasses(f, passes, stats)
		}
		if err != nil {
			resp.Problems = append(resp.Problems, filepath.Base(f)+": "+err.Error())
			continue
		}
		resp.Files++
		hits := 0
		var notes []string
		if pd != nil {
			hits = pd.Hits
			notes = pd.Notes
			if !req.Dry {
				pending = append(pending, pd)
			}
		}
		records = append(records, report.FileRecord{
			Path: absPath(f), Name: displayPath(f), Hits: hits, Notes: notes,
		})
	}

	for _, d := range stats.Order {
		resp.Rows = append(resp.Rows, previewRow{
			File: displayPath(d.File), Loc: d.Loc, Rule: d.Label,
			Before: d.Before, After: d.After, Ctx: d.Ctx, Count: d.Count,
		})
		resp.Hits += d.Count
	}
	// 每条策略各自命中多少处、涉及多少文件
	hitFiles := map[string]struct{}{}
	byStageHits := map[string]int{}
	byStageFiles := map[string]map[string]struct{}{}
	for _, d := range stats.Order {
		hitFiles[d.File] = struct{}{}
		if d.Stage == "" {
			continue
		}
		byStageHits[d.Stage] += d.Count
		if byStageFiles[d.Stage] == nil {
			byStageFiles[d.Stage] = map[string]struct{}{}
		}
		byStageFiles[d.Stage][d.File] = struct{}{}
	}
	resp.HitFiles = len(hitFiles)
	if len(passes) > 1 {
		for i := range resp.Steps {
			resp.Steps[i].Hits = byStageHits[resp.Steps[i].Name]
			resp.Steps[i].Files = len(byStageFiles[resp.Steps[i].Name])
		}
	} else if len(resp.Steps) == 1 {
		resp.Steps[0].Hits = resp.Hits
		resp.Steps[0].Files = len(hitFiles)
	}

	if !req.Dry && len(pending) > 0 {
		if mode == files.BackupModeZip {
			zp, err := files.BackupZip(req.Target)
			if err != nil {
				return resp, fmt.Errorf("整体备份失败，已中止，未写入任何文件: %w", err)
			}
			resp.Backup = zp
		}
		for _, pd := range pending {
			if err := pd.Write(mode == files.BackupModeFile); err != nil {
				resp.Problems = append(resp.Problems, "写入失败 "+filepath.Base(pd.Path)+": "+err.Error())
				resp.Failed++
				continue
			}
			resp.Written++
		}
	}

	// 报告：空跑也导出，方便先看后确认
	reportPath := req.Report
	if reportPath == "" && req.Open {
		reportPath = filepath.Join(os.TempDir(), "desensitize-"+time.Now().Format("20060102-150405")+".html")
	}
	if reportPath != "" {
		var all []*rules.Rule
		for _, ps := range passes {
			all = append(all, ps.Rules...)
		}
		if err := report.Write(reportPath, report.Options{
			Stamp:     time.Now().Format("2006-01-02 15:04:05"),
			ScanFiles: len(found),
			Rules:     all,
			Files:     records,
			Stats:     stats,
		}); err != nil {
			resp.Problems = append(resp.Problems, "报告导出失败: "+err.Error())
		} else {
			resp.Report = reportPath
			// 空跑也可以打开报告：命令行版在 --dry-run 时同样会开
			if req.Open {
				_ = browser.OpenFile(reportPath)
			}
		}
	}

	resp.ElapsedMS = time.Since(start).Milliseconds()
	return resp, nil
}
