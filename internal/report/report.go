// Package report 导出脱敏明细：HTML（左目录右内容）/ CSV / JSON。
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RexKang/desensitize-cli/internal/files"
	"github.com/RexKang/desensitize-cli/internal/fileurl"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

// FileRecord 是参与报告的一个文件。
type FileRecord struct {
	Path  string // 绝对路径，用于 file:// 链接
	Name  string // 展示名（相对路径）
	Hits  int
	Notes []string
}

// Options 报告入参。
type Options struct {
	Stamp     string
	ScanFiles int
	Rules     []*rules.Rule
	Files     []FileRecord
	Stats     *files.Stats
}

type row struct {
	Rule   string
	Loc    string
	Before string
	After  string
	Ctx    string
	Count  int
}

// Write 按扩展名导出。
func Write(path string, o Options) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		return os.WriteFile(path, []byte(buildHTML(o)), 0o644)
	case ".csv":
		return writeCSV(path, o)
	default:
		return writeJSON(path, o)
	}
}

// IsHTML 判断报告是否需要浏览器打开。
func IsHTML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".html" || ext == ".htm"
}

func rowsByFile(o Options) (map[string][]row, []row) {
	display := make(map[string]string, len(o.Files))
	for _, f := range o.Files {
		display[f.Path] = f.Name
	}
	byFile := map[string][]row{}
	var flat []row

	ordered := append([]*files.Detail(nil), o.Stats.Order...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := display[ordered[i].File], display[ordered[j].File]
		if a != b {
			return a < b
		}
		return ordered[i].Loc < ordered[j].Loc
	})
	for _, d := range ordered {
		r := row{Rule: d.Label, Loc: d.Loc, Before: d.Before, After: d.After, Ctx: d.Ctx, Count: d.Count}
		name := display[d.File]
		byFile[name] = append(byFile[name], r)
		flat = append(flat, r)
	}
	return byFile, flat
}

func writeCSV(path string, o Options) error {
	_, flat := rowsByFile(o)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil { // Excel 认 BOM
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"文件", "规则", "位置", "原文", "替换后", "上下文", "次数"})
	display := make(map[string]string, len(o.Files))
	for _, x := range o.Files {
		display[x.Path] = x.Name
	}
	for _, d := range o.Stats.Order {
		_ = w.Write([]string{
			display[d.File], d.Label, d.Loc, d.Before, d.After, d.Ctx,
			fmt.Sprintf("%d", d.Count),
		})
	}
	_ = flat
	w.Flush()
	return w.Error()
}

func writeJSON(path string, o Options) error {
	type item struct {
		File   string `json:"file"`
		Rule   string `json:"rule"`
		Loc    string `json:"loc"`
		Before string `json:"before"`
		After  string `json:"after"`
		Ctx    string `json:"ctx"`
		Count  int    `json:"count"`
	}
	display := make(map[string]string, len(o.Files))
	for _, x := range o.Files {
		display[x.Path] = x.Name
	}
	items := make([]item, 0, len(o.Stats.Order))
	for _, d := range o.Stats.Order {
		items = append(items, item{display[d.File], d.Label, d.Loc, d.Before, d.After, d.Ctx, d.Count})
	}
	doc := map[string]any{
		"generated_at": o.Stamp,
		"scan_files":   o.ScanFiles,
		"files":        o.Files,
		"items":        items,
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func fileURI(p string) string { return fileurl.Of(p) }

func highlight(ctx string) string {
	s := html.EscapeString(ctx)
	s = strings.ReplaceAll(s, "⟦", "<mark>")
	s = strings.ReplaceAll(s, "⟧", "</mark>")
	return s
}

func buildHTML(o Options) string {
	byFile, _ := rowsByFile(o)

	// 按文件所在目录分组
	groups := map[string][]FileRecord{}
	for _, f := range o.Files {
		if f.Hits == 0 {
			continue
		}
		dir := filepath.Dir(f.Name)
		groups[dir] = append(groups[dir], f)
	}
	dirs := make([]string, 0, len(groups))
	for d := range groups {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	totalHits := 0
	for _, f := range o.Files {
		totalHits += f.Hits
	}
	fileCount := 0
	for _, f := range o.Files {
		if f.Hits > 0 {
			fileCount++
		}
	}

	var secs, tocs strings.Builder
	for i, d := range dirs {
		here := groups[d]
		sort.Slice(here, func(a, b int) bool { return here[a].Name < here[b].Name })
		secID := fmt.Sprintf("sec%d", i)
		hits := 0
		hay := []string{d}
		var body strings.Builder
		for _, f := range here {
			hits += f.Hits
			hay = append(hay, f.Name)
			rows := byFile[f.Name]
			name := filepath.Base(f.Name)
			link := ""
			if uri := fileURI(f.Path); uri != "" {
				link = fmt.Sprintf(`<a class="open" href="%s" target="_blank" title="%s">%s</a>`,
					html.EscapeString(uri), html.EscapeString(f.Path), html.EscapeString(name))
			} else {
				link = html.EscapeString(name)
			}
			var tb strings.Builder
			for _, r := range rows {
				hay = append(hay, r.Rule+" "+r.Loc+" "+r.Before+" "+r.After)
				fmt.Fprintf(&tb,
					`<tr><td class="rule">%s</td><td class="loc">%s</td>`+
						`<td class="val">%s</td><td class="val out">%s</td>`+
						`<td class="ctx">%s</td><td class="num">%d</td></tr>`,
					html.EscapeString(r.Rule), html.EscapeString(r.Loc),
					html.EscapeString(orEmpty(r.Before)), html.EscapeString(orEmpty(r.After)),
					highlight(r.Ctx), r.Count)
			}
			fmt.Fprintf(&body,
				`<div class="file"><h3>%s<span>共 %d 处</span></h3>`+
					`<table class="d"><thead><tr><th>规则</th><th>位置</th><th>原文</th>`+
					`<th>替换后</th><th>上下文（⟦⟧ 内是被替换的那一段）</th><th class="num">次数</th>`+
					`</tr></thead><tbody>%s</tbody></table></div>`,
				link, f.Hits, tb.String())
		}
		dirLink := ""
		if len(here) > 0 {
			if uri := fileURI(filepath.Dir(here[0].Path)); uri != "" {
				dirLink = fmt.Sprintf(`<a class="open dir" href="%s" title="打开这个文件夹">打开文件夹</a>`,
					html.EscapeString(uri))
			}
		}
		fmt.Fprintf(&secs,
			`<section class="sec" id="%s" data-s="%s"><h2>%s %s</h2>`+
				`<div class="dsum">%d 文件 · %d 处</div>%s</section>`,
			secID, html.EscapeString(strings.ToLower(strings.Join(hay, " "))),
			html.EscapeString(d), dirLink, len(here), hits, body.String())

		parent := filepath.Dir(d)
		if parent == "." {
			parent = ""
		}
		fmt.Fprintf(&tocs,
			`<a href="#%s" data-sec="%s" title="%s"><span class="nm">%s<span class="cn">%d 处 · %d 文件</span></span>`+
				`<span class="pa">%s</span></a>`,
			secID, secID, html.EscapeString(d), html.EscapeString(filepath.Base(d)), hits, len(here), html.EscapeString(parent))
	}

	var mini strings.Builder
	for _, r := range o.Rules {
		if !r.IsEnabled() {
			continue
		}
		n := o.Stats.RuleHits[r.ID]
		if n == 0 {
			continue
		}
		fmt.Fprintf(&mini, `<tr><td>%s</td><td class="n">%d</td><td class="n">%d</td></tr>`,
			html.EscapeString(r.Label()), n, o.Stats.RuleFiles[r.ID])
	}

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh"><head><meta charset="utf-8">`)
	fmt.Fprintf(&b, `<title>脱敏明细 %s</title><style>%s</style></head><body>`, html.EscapeString(o.Stamp), reportCSS)
	b.WriteString(`<nav id="nav"><div class="tl">脱敏明细</div>`)
	fmt.Fprintf(&b, `<div class="tsub">%s</div>`, html.EscapeString(o.Stamp))
	fmt.Fprintf(&b, `<div class="tstat"><b>%d</b> 处替换 · %d 文件 · %d 目录</div>`, totalHits, fileCount, len(dirs))
	b.WriteString(`<div class="mini-h">规则汇总</div><table class="mini"><thead><tr><th>规则</th><th class="n">命中</th><th class="n">文件</th></tr></thead><tbody>`)
	b.WriteString(mini.String())
	b.WriteString(`</tbody></table><div class="mini-h">扫描</div>`)
	fmt.Fprintf(&b, `<div class="tstat" style="margin:-4px 0 10px">%d 个文件 → 有命中 %d 个</div>`, o.ScanFiles, fileCount)
	b.WriteString(`<input id="q" placeholder="筛选：目录 / 文件名 / 原文 / 规则"><span id="hint"></span>`)
	b.WriteString(`<div class="toc">`)
	b.WriteString(tocs.String())
	b.WriteString(`</div></nav><main id="main">`)
	b.WriteString(`<div class="warn">报告里含脱敏前的原始内容，仅供本地核对，不要提交仓库或发公开渠道</div>`)
	b.WriteString(secs.String())
	b.WriteString(`</main>`)
	b.WriteString(reportJS)
	b.WriteString(`</body></html>`)
	return b.String()
}

func orEmpty(s string) string {
	if s == "" {
		return "（空）"
	}
	return s
}
