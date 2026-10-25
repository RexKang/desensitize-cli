package gui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/RexKang/desensitize-cli/internal/filedialog"
	"github.com/RexKang/desensitize-cli/internal/mask"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

// ────────────────────────────── 选目录 / 选文件 ──────────────────────────────

type pickReq struct {
	Mode    string `json:"mode"`    // dir | file
	Initial string `json:"initial"` // 打开时定位到的位置，可为空
}

// handlePick 调系统原生对话框选路径。
//
// 网页拿不到本机绝对路径，所以这一步必须走后端。Windows 上是 shell 的
// IFileOpenDialog（资源管理器同款）；别的平台返回 unsupported，页面回退到
// 自带的目录浏览器。
func (s *Server) handlePick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req pickReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
		return
	}

	var (
		path string
		err  error
	)
	switch req.Mode {
	case "dir":
		path, err = filedialog.PickDir(req.Initial)
	case "file":
		path, err = filedialog.PickFile(req.Initial)
	default:
		writeJSON(w, 400, map[string]any{"ok": false, "error": "mode 只能是 dir 或 file"})
		return
	}

	switch {
	case errors.Is(err, filedialog.ErrCanceled):
		writeJSON(w, 200, map[string]any{"ok": false, "canceled": true})
		return
	case errors.Is(err, filedialog.ErrUnsupported):
		writeJSON(w, 200, map[string]any{
			"ok": false, "unsupported": true,
			"error": "当前平台没有原生对话框，用「手选…」里的目录浏览器",
		})
		return
	case err != nil:
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	if info, statErr := os.Stat(path); statErr != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "选到的路径不可访问: " + statErr.Error()})
		return
	} else if req.Mode == "dir" && !info.IsDir() {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "选到的不是目录: " + path})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": path})
}

// ────────────────────────────── 单条规则检查 ──────────────────────────────

// demoText 是校验规则时用的样本文本：几类常见敏感数据各来一份，
// 让用户改完 replace 就能看到「会变成什么」，不用先空跑整个目录。
const demoText = "2026-10-24 变更单 TCK-2026-0001\n" +
	"联系人 13800138000，座机 010-88886666，邮箱 deep@example.com\n" +
	"身份证 110101199001011234，内网 10.1.2.3，主机 host-web01\n" +
	"车牌 京A12345，工号 00012345，密钥 password=Passw0rd!"

type ruleCheckReq struct {
	Rule json.RawMessage `json:"rule"`
}

type demoHit struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type ruleCheckResp struct {
	Valid bool     `json:"valid"`
	Error string   `json:"error"`
	Demo  *demoHit `json:"demo"`
	Hits  int      `json:"hits"`
}

// handleRuleCheck 只校验单条规则，不扫目录：编辑框里每敲几个字就调一次，
// 必须是纯内存操作。
func (s *Server) handleRuleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ruleCheckReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "请求格式错误: " + err.Error()})
		return
	}
	if len(req.Rule) == 0 {
		writeJSON(w, 400, map[string]any{"ok": false, "error": "没带规则"})
		return
	}

	rs, _, err := rules.Parse([]byte("[" + string(req.Rule) + "]"))
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": true, "check": ruleCheckResp{Error: err.Error()}})
		return
	}
	check := ruleCheckResp{Valid: true}
	// 单条规则套在样本上：命中的话给一条「原文 → 替换后」示例
	_, hits := mask.Apply(demoText, rs)
	for _, h := range hits {
		if h.After == h.Original {
			continue
		}
		check.Hits++
		if check.Demo == nil {
			check.Demo = &demoHit{Before: h.Original, After: h.After}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "check": check})
}
