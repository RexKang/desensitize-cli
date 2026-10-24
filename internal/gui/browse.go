package gui

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// dirEntry 是目录浏览器里的一项。
type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type lsResp struct {
	Path   string     `json:"path"`
	Parent string     `json:"parent"`
	Dirs   []dirEntry `json:"dirs"`
	Note   string     `json:"note"`
}

// handleLs 列目录，供页面上的「浏览…」用。
// 网页里拿不到本地绝对路径（<input type=file> 只给相对路径），只能由后端代劳。
func (s *Server) handleLs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	if path == "" {
		resp := lsResp{Note: "选一个盘符或根目录"}
		for _, root := range roots() {
			resp.Dirs = append(resp.Dirs, dirEntry{Name: root, Path: root})
		}
		writeJSON(w, 200, map[string]any{"ok": true, "ls": resp})
		return
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "路径不合法: " + err.Error()})
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": "打不开这个目录: " + err.Error()})
		return
	}

	resp := lsResp{Path: filepath.ToSlash(abs)}
	if parent := filepath.Dir(abs); parent != abs {
		resp.Parent = filepath.ToSlash(parent)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
			continue
		}
		resp.Dirs = append(resp.Dirs, dirEntry{
			Name: name,
			Path: filepath.ToSlash(filepath.Join(abs, name)),
		})
		if len(resp.Dirs) >= 500 {
			resp.Note = "目录太多，只显示前 500 个"
			break
		}
	}
	sort.Slice(resp.Dirs, func(i, j int) bool {
		return strings.ToLower(resp.Dirs[i].Name) < strings.ToLower(resp.Dirs[j].Name)
	})
	writeJSON(w, 200, map[string]any{"ok": true, "ls": resp})
}

// roots 列出可选根目录：Windows 是各个存在的盘符，其它平台只有 /。
func roots() []string {
	if runtime.GOOS != "windows" {
		return []string{"/"}
	}
	var out []string
	for c := 'A'; c <= 'Z'; c++ {
		p := string(c) + ":\\"
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
