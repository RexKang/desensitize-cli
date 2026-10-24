// Command desensitize 是测试数据脱敏命令行工具。
//
// 输入：.txt / .md / .csv / .xlsx / .xlsm（旧版 .xls 不支持）
// 流程：扫描 -> 汇总 -> 确认 -> 备份 -> 原地覆盖 -> 可自动打开 HTML 报告（空跑只到汇总）
//
// 规则放在 rules.json（首次运行会自动从 rules.sample.json 复制一份）。
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RexKang/desensitize-cli/internal/browser"
	"github.com/RexKang/desensitize-cli/internal/files"
	"github.com/RexKang/desensitize-cli/internal/presets"
	"github.com/RexKang/desensitize-cli/internal/report"
	"github.com/RexKang/desensitize-cli/internal/rules"
	"golang.org/x/text/width"
)

// version 由 -ldflags "-X main.version=v0.2.0" 注入。
var version = "dev"

const line = "════════════════════════════════════════════════════════════════════════"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stdin))
}

type options struct {
	targets    []string
	rulesPath  string
	recursive  bool
	dryRun     bool
	yes        bool
	backupMode string
	reportPath string
	open       bool
	noOpen     bool
	samples    int
	listFiles  int
	all        bool
	top        int
	dumpRules  bool
	preset     string
	listPreset bool
}

func run(args []string, out io.Writer, in io.Reader) int {
	p := &printer{w: out}
	opts, code := parseArgs(args, p)
	if code >= 0 {
		return code
	}

	if opts.listPreset {
		listPresets(p)
		return 0
	}

	var rs []*rules.Rule
	if opts.preset != "" {
		ps, ok := presets.Get(opts.preset)
		if !ok {
			p.line("没有这个内置案例：%s（用 --list-presets 看可选值）", opts.preset)
			return 1
		}
		rs = rules.Clone(ps.Rules)
		p.line("使用内置案例「%s」%d 条规则（%s）", ps.Name, len(ps.Rules), ps.Risk+"风险")
	} else {
		rulesPath, autoCopy, err := resolveRules(opts.rulesPath)
		if err != nil {
			p.line("%v", err)
			return 1
		}
		var err2 error
		rs, _, err2 = loadRules(rulesPath, autoCopy, p)
		if err2 != nil {
			p.line("%v", err2)
			return 1
		}
	}
	if opts.dumpRules {
		dumpRules(rs, p)
		return 0
	}

	found, problems, xlsSkipped := files.Discover(opts.targets, opts.recursive)
	if len(found) == 0 {
		p.line("没有找到可处理的文件。")
		for _, x := range problems {
			p.line("  · %s", x)
		}
		return 1
	}

	base := relBase(opts.targets)
	stats := files.NewStats()
	var pending []*files.Pending
	var records []report.FileRecord
	for _, f := range found {
		var pd *files.Pending
		var err error
		if files.IsXLSX(f) {
			pd, err = files.ProcessXLSX(f, rs, stats)
		} else {
			pd, err = files.ProcessText(f, rs, stats)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		name := displayName(base, f)
		hits := 0
		if pd != nil {
			pending = append(pending, pd)
			hits = pd.Hits
		}
		records = append(records, report.FileRecord{Path: absPath(f), Name: name, Hits: hits, Notes: notesOf(pd)})
	}

	totalHits := 0
	for _, pd := range pending {
		totalHits += pd.Hits
	}

	stamp := time.Now().Format("2006-01-02 15:04:05")
	printSummary(p, summarise{
		opts: opts, rules: rs, stats: stats, records: records, base: base,
		totalHits: totalHits, scanned: len(found), problems: problems, xls: xlsSkipped,
	})

	// 报告先落盘：空跑阶段就能打开看，确认前不必写业务文件
	reportPath := opts.reportPath
	if reportPath == "" && opts.open {
		reportPath = filepath.Join(os.TempDir(), "desensitize-"+time.Now().Format("20060102-150405")+".html")
	}
	if reportPath != "" {
		if err := report.Write(reportPath, report.Options{
			Stamp: stamp, ScanFiles: len(found), Rules: rs, Files: records, Stats: stats,
		}); err != nil {
			p.line("报告导出失败: %v", err)
		} else {
			p.line("明细已导出：%s（%d 行去重明细）", reportPath, len(stats.Order))
			openReport(reportPath, opts, p)
		}
	}

	if totalHits == 0 {
		p.line("没有命中，未写入任何文件。")
		return 4
	}

	if opts.dryRun {
		p.line("空跑模式（--dry-run），未写入任何文件。")
		return 0
	}

	if !opts.yes {
		if !isInteractive() {
			p.line("非交互环境且未给 --yes，拒绝写入。")
			return 3
		}
		fmt.Fprintf(p.w, "\n确认覆盖这些文件？备份会留在原地 [y/N] ")
		reader := bufio.NewReader(in)
		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			p.line("已取消，未写入。")
			return 3
		}
	}

	if opts.backupMode == files.BackupModeZip {
		for _, t := range opts.targets {
			zp, err := files.BackupZip(t)
			if err != nil {
				p.line("整体备份失败，已中止，未写入任何文件：%v", err)
				return 1
			}
			size := int64(0)
			if info, err := os.Stat(zp); err == nil {
				size = info.Size()
			}
			p.line("整体备份：%s（%s）", zp, files.HumanSize(size))
		}
	}

	written, failed := 0, 0
	for _, pd := range pending {
		if err := pd.Write(opts.backupMode == files.BackupModeFile); err != nil {
			p.line("  写入失败 %s: %v", pd.Path, err)
			failed++
			continue
		}
		written++
	}
	switch opts.backupMode {
	case files.BackupModeFile:
		p.line("已写入 %d 个文件（%d 处替换），备份后缀 .<时间戳>.bak。", written, totalHits)
	case files.BackupModeZip:
		p.line("已写入 %d 个文件（%d 处替换），原件在上层目录的 .bak.zip 里。", written, totalHits)
	default:
		p.line("已写入 %d 个文件（%d 处替换），未做备份。", written, totalHits)
	}
	if failed > 0 {
		p.line("有 %d 个文件写入失败，原文件未改动。", failed)
		return 1
	}
	return 0
}

func notesOf(pd *files.Pending) []string {
	if pd == nil {
		return nil
	}
	return pd.Notes
}

func parseArgs(args []string, p *printer) (options, int) {
	var o options
	fs := flag.NewFlagSet("desensitize", flag.ContinueOnError)
	var usage bytes.Buffer
	fs.SetOutput(&usage)
	fs.StringVar(&o.rulesPath, "rules", "", "规则文件（默认同目录 rules.json）")
	fs.BoolVar(&o.recursive, "recursive", true, "递归子目录（默认开启）")
	noRec := fs.Bool("no-recursive", false, "只看目标目录第一层")
	fs.BoolVar(&o.dryRun, "n", false, "空跑：只算不写，不碰任何文件")
	fs.BoolVar(&o.dryRun, "dry-run", false, "空跑：只算不写，不碰任何文件")
	fs.BoolVar(&o.yes, "y", false, "跳过确认直接写入")
	fs.BoolVar(&o.yes, "yes", false, "跳过确认直接写入")
	fs.StringVar(&o.backupMode, "backup", files.BackupModeFile, "备份方式：zip 整体打包到上层目录 / file 逐文件 .bak / none 不备份")
	noBackup := fs.Bool("no-backup", false, "等价于 --backup=none（不建议）")
	fs.StringVar(&o.reportPath, "report", "", "导出明细：.html / .csv / 其它存 JSON")
	fs.BoolVar(&o.open, "open", false, "处理完后自动打开 HTML 报告（无报告时生成临时报告）")
	fs.BoolVar(&o.noOpen, "no-open", false, "不自动打开浏览器")
	fs.IntVar(&o.samples, "samples", 3, "终端明细里每文件每规则最多显示几条")
	fs.IntVar(&o.listFiles, "list-files", 12, "终端明细里最多展开几个文件；0 只看汇总")
	fs.BoolVar(&o.all, "all", false, "终端里展开全部文件与目录")
	fs.IntVar(&o.top, "top", 0, "额外列出每规则最高频命中值前 N 个")
	fs.BoolVar(&o.dumpRules, "dump-rules", false, "打印生效规则后退出")
	fs.StringVar(&o.preset, "preset", "", "用内置案例的规则（见 --list-presets），忽略 --rules")
	fs.BoolVar(&o.listPreset, "list-presets", false, "列出内置案例后退出")
	showVersion := fs.Bool("version", false, "打印版本")

	fs.Usage = func() {
		fmt.Fprint(&usage, `用法: desensitize [选项] <文件或目录>...

示例:
  desensitize ./测试数据 --dry-run
  desensitize ./测试数据 --report 明细.html
  desensitize ./hosts --preset network --backup zip --yes

选项:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(permute(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			p.w.Write(usage.Bytes())
			return o, 0
		}
		p.line("参数错误：%v", err)
		p.w.Write(usage.Bytes())
		return o, 2
	}

	if *noRec {
		o.recursive = false
	}
	if *noBackup {
		o.backupMode = files.BackupModeNone
	}
	switch o.backupMode {
	case files.BackupModeFile, files.BackupModeZip, files.BackupModeNone:
	default:
		p.line("--backup 只能是 file / zip / none，收到 %q", o.backupMode)
		return o, 2
	}
	if *showVersion {
		p.line("desensitize %s", version)
		return o, 0
	}
	o.targets = fs.Args()
	if len(o.targets) == 0 && !o.listPreset && !o.dumpRules {
		p.line("至少给一个文件或目录。用法: desensitize [选项] <文件或目录>...")
		return o, 2
	}
	return o, -1
}

// resolveRules 定位规则文件：显式路径 > 可执行文件同目录 > 当前目录。
func resolveRules(explicit string) (path string, autoCopy bool, err error) {
	if explicit != "" {
		return explicit, false, nil
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "rules.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "rules.json"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, false, nil
		}
	}
	// 还没有 rules.json：优先挑一个旁边真有 rules.sample.json 的目录，首次运行才复制得出来
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(filepath.Dir(c), "rules.sample.json")); err == nil {
			return c, true, nil
		}
	}
	if len(candidates) > 0 {
		return candidates[0], true, nil
	}
	return "rules.json", true, nil
}

// permute 让开关可以写在目标后面（desensitize a.txt --dry-run），
// 标准库 flag 遇到第一个非开关参数就会停止解析，这里先重排再用。
func permute(args []string) []string {
	valued := map[string]bool{
		"rules": true, "report": true, "samples": true, "list-files": true, "top": true,
		"preset": true, "backup": true,
	}
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' && a != "--" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				continue
			}
			if valued[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

func loadRules(path string, autoCopy bool, p *printer) ([]*rules.Rule, rules.Docs, error) {
	if _, err := os.Stat(path); err != nil {
		sample := filepath.Join(filepath.Dir(path), "rules.sample.json")
		if autoCopy {
			if data, e := os.ReadFile(sample); e == nil {
				if werr := os.WriteFile(path, data, 0o644); werr == nil {
					p.line("没找到 %s，已从 rules.sample.json 复制一份；改完规则重跑即可。", filepath.Base(path))
				}
			}
		}
	}
	return rules.Load(path)
}

func listPresets(p *printer) {
	list, err := presets.All()
	if err != nil {
		p.line("%v", err)
		return
	}
	p.line("内置案例库 v%s · %d 个（--preset <id> 直接使用）", presets.Version, len(list))
	for _, x := range list {
		enabled := 0
		for _, r := range x.Rules {
			if r.IsEnabled() {
				enabled++
			}
		}
		p.line("  %-12s %-18s [%s风险] %d 规则（启用 %d）  %s", x.ID, x.Name, x.Risk, len(x.Rules), enabled, x.Desc)
	}
}

func dumpRules(rs []*rules.Rule, p *printer) {
	for _, r := range rs {
		flagText := "启用"
		if !r.IsEnabled() {
			flagText = "停用"
		}
		detail := r.Pattern
		if r.Type == rules.TypeLiteral {
			detail = "find=" + r.Find
		}
		if r.Type == rules.TypeIPShift {
			detail = fmt.Sprintf("first=%v offset=%v cidrs=%v", ptr(r.FirstOctet), ptr(r.Offset), r.SourceCIDRs)
		}
		p.line("[%s] %-14s %-14s %-8s %-12s %s", flagText, r.ID, r.Label(), r.Type, r.Replace, detail)
	}
}

func ptr(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// relBase 决定相对路径的基准：单目录目标用它的父目录，其余用当前目录。
// 这样 desensitize ./测试数据 显示的是 测试数据/a.txt，而不是一长串绝对路径。
func relBase(targets []string) string {
	if len(targets) == 1 {
		// 单目录用父目录、单文件用所在目录，显示才是「测试数据.txt」这种
		return filepath.Dir(absPath(targets[0]))
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// displayName 相对基准目录展示，跨盘符等场景退回绝对路径。
func displayName(base, p string) string {
	abs := absPath(p)
	if base != "" {
		if rel, err := filepath.Rel(base, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return abs
}

func isInteractive() bool {
	st, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// openMode 是「能不能打开 HTML」的结论。
type openMode int

const (
	openDisabled  openMode = iota // --no-open 或环境变量显式关闭
	openHeadless                  // 没有交互终端（管道、CI、服务），只打印路径
	openGraphical                 // 交互会话，交给系统默认浏览器
)

// decideOpen 判断当前能不能真的拉起浏览器。
// 判断依据：显式开关 > 环境变量 > 是否交互终端；图形会话的探测交给平台实现。
func decideOpen(opts options) openMode {
	if opts.noOpen || os.Getenv("DESENSITIZE_NO_BROWSER") != "" {
		return openDisabled
	}
	if !isInteractive() {
		return openHeadless
	}
	return openGraphical
}

// openReport 处理「能不能打开 HTML」的判断：非交互、无图形会话、显式关闭都不强开。
func openReport(path string, opts options, p *printer) {
	if !report.IsHTML(path) {
		return
	}
	switch decideOpen(opts) {
	case openDisabled:
		p.line("报告地址：%s（已禁用自动打开）", path)
	case openHeadless:
		p.line("报告地址：%s（非交互环境，未自动打开）", path)
	default:
		if err := browser.OpenFile(path); err != nil {
			p.line("未自动打开浏览器，请手动打开 %s（%v）", path, err)
			return
		}
		p.line("已在浏览器打开 %s", path)
	}
}

// ────────────────────────────── 终端输出 ──────────────────────────────

type printer struct{ w io.Writer }

func (p *printer) line(format string, a ...any) {
	fmt.Fprintf(p.w, format+"\n", a...)
}

type summarise struct {
	opts      options
	base      string
	rules     []*rules.Rule
	stats     *files.Stats
	records   []report.FileRecord
	totalHits int
	scanned   int
	problems  []string
	xls       []string
}

func printSummary(p *printer, s summarise) {
	active, disabled := 0, 0
	for _, r := range s.rules {
		if r.IsEnabled() {
			active++
		} else {
			disabled++
		}
	}
	hitFiles := 0
	for _, r := range s.records {
		if r.Hits > 0 {
			hitFiles++
		}
	}
	scope := strings.Join(s.opts.targets, " ")

	p.line("%s", line)
	if s.opts.dryRun {
		p.line("空跑 · 只算不写，未碰任何文件")
	} else {
		p.line("脱敏 · 确认后才会写入")
	}
	p.line("范围  %s", scope)
	extra := ""
	if disabled > 0 {
		extra = fmt.Sprintf("，停用 %d 条", disabled)
	}
	p.line("扫描  %d 个文件 → %d 个有命中；启用规则 %d 条%s", s.scanned, hitFiles, active, extra)
	p.line("%s", line)

	// 一、规则汇总
	p.line("")
	p.line("【一】规则汇总")
	rdw := 8
	for _, r := range s.rules {
		if w := dispWidth(r.Label()) + 2; w > rdw {
			rdw = w
		}
	}
	p.line("%s%s%s%s", pad("规则", rdw), pad("命中", 7), pad("文件", 6), "首例")
	type rr struct {
		label      string
		hits, file int
		first      *files.Detail
	}
	var rows []rr
	for _, r := range s.rules {
		if !r.IsEnabled() {
			continue
		}
		hits := s.stats.RuleHits[r.ID]
		if hits == 0 {
			continue
		}
		var first *files.Detail
		for _, d := range s.stats.Order {
			if d.RuleID == r.ID {
				first = d
				break
			}
		}
		rows = append(rows, rr{r.Label(), hits, s.stats.RuleFiles[r.ID], first})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].hits > rows[j].hits })
	for _, r := range rows {
		first := ""
		if r.first != nil {
			first = fmt.Sprintf("%s  →  %s", clip(r.first.Before, 26), orEmptyText(r.first.After))
		}
		p.line("%s%s%s%s", pad(r.label, rdw), pad(fmt.Sprint(r.hits), 7), pad(fmt.Sprint(r.file), 6), first)
	}
	p.line("%s%s%s", pad("合计", rdw), pad(fmt.Sprint(s.totalHits), 7), pad(fmt.Sprint(hitFiles), 6))

	// 提示行：已达标 / 不可重跑 / 停用
	var bits []string
	noop := 0
	for _, n := range s.stats.Noop {
		noop += n
	}
	if noop > 0 {
		bits = append(bits, fmt.Sprintf("已是目标值 %d 处", noop))
	}
	var heavy []string
	for _, r := range s.rules {
		if r.IsEnabled() && (r.Type == rules.TypeIPShift || r.Type == rules.TypeHash) && s.stats.RuleHits[r.ID] > 0 {
			heavy = append(heavy, r.Label())
		}
	}
	if len(heavy) > 0 {
		bits = append(bits, "位移/哈希规则不可重跑："+strings.Join(heavy, "、")+" 再跑一次结果会变")
	}
	if disabled > 0 {
		var names []string
		for _, r := range s.rules {
			if !r.IsEnabled() {
				names = append(names, r.ID)
			}
		}
		bits = append(bits, "停用 "+strings.Join(names, "、"))
	}
	if len(bits) > 0 {
		p.line("   " + strings.Join(bits, "；"))
	}

	// 一附、Top 命中值
	if s.opts.top > 0 {
		p.line("")
		p.line("【一附】命中值 Top（每规则前 %d 个，用来判断有没有误伤）", s.opts.top)
		for _, r := range s.rules {
			if !r.IsEnabled() || s.stats.RuleHits[r.ID] == 0 {
				continue
			}
			var parts []string
			for _, t := range s.stats.TopValues(r.ID, s.opts.top) {
				parts = append(parts, fmt.Sprintf("%s ×%d", clip(t.Value, 22), t.Count))
			}
			p.line("%s%s", pad(r.Label(), rdw), strings.Join(parts, "  "))
		}
	}

	// 二、目录分布
	if hitFiles > 0 {
		p.line("")
		p.line("【二】目录分布")
		type grp struct {
			files, hits int
		}
		groups := map[string]*grp{}
		var order []string
		for _, r := range s.records {
			if r.Hits == 0 {
				continue
			}
			key := dirKey(r.Name)
			if groups[key] == nil {
				groups[key] = &grp{}
				order = append(order, key)
			}
			groups[key].files++
			groups[key].hits += r.Hits
		}
		sort.SliceStable(order, func(i, j int) bool { return groups[order[i]].hits > groups[order[j]].hits })
		dw := 10
		for _, k := range order {
			if w := dispWidth(k) + 2; w > dw {
				dw = w
			}
		}
		p.line("%s%s%s", pad("目录", dw), pad("文件", 6), "命中")
		maxRows := len(order)
		if !s.opts.all && maxRows > 12 {
			maxRows = 12
		}
		for _, k := range order[:maxRows] {
			name := k
			if name == "." {
				name = "(当前目录)"
			}
			p.line("%s%s%s", pad(clip(filepath.FromSlash(name), 60), dw),
				pad(fmt.Sprint(groups[k].files), 6), fmt.Sprint(groups[k].hits))
		}
		if maxRows < len(order) {
			p.line("… 另有 %d 个目录（--all 展开）", len(order)-maxRows)
		}
	}

	// 三、明细
	shown := 0
	if s.opts.listFiles > 0 && hitFiles > 0 {
		p.line("")
		limit := s.opts.listFiles
		if s.opts.all {
			limit = hitFiles
		}
		if limit > hitFiles {
			limit = hitFiles
		}
		p.line("【三】明细（按命中数降序，显示 %d/%d 个文件；每文件每规则最多 %d 条）",
			limit, hitFiles, s.opts.samples)

		withHits := make([]report.FileRecord, 0, hitFiles)
		for _, r := range s.records {
			if r.Hits > 0 {
				withHits = append(withHits, r)
			}
		}
		sort.SliceStable(withHits, func(i, j int) bool { return withHits[i].Hits > withHits[j].Hits })
		for _, rec := range withHits[:limit] {
			p.line("")
			p.line("  %s    共 %d 处", rec.Name, rec.Hits)
			perRule := map[string]int{}
			hidden := 0
			for _, d := range s.stats.Order {
				if displayName(s.base, d.File) != rec.Name && d.File != rec.Name {
					continue
				}
				if perRule[d.RuleID] >= s.opts.samples {
					hidden++
					continue
				}
				perRule[d.RuleID]++
				count := ""
				if d.Count > 1 {
					count = fmt.Sprintf(" ×%d", d.Count)
				}
				p.line("    %s%s%s  →  %s%s", pad(d.Label, rdw), pad(d.Loc, 14),
					clip(d.Before, 30), orEmptyText(d.After), count)
			}
			if hidden > 0 {
				p.line("    … 另有 %d 处未显示（--samples 调大，或 --report 导出全集）", hidden)
			}
			for _, n := range rec.Notes {
				p.line("    · %s", n)
			}
			shown++
		}
		if shown < hitFiles {
			p.line("")
			p.line("  … 另有 %d 个有命中的文件未展开（--list-files 调大，或 --all）", hitFiles-shown)
		}
	}

	// 四、提示
	var tips []string
	if len(s.xls) > 0 {
		tips = append(tips, fmt.Sprintf("跳过 %d 个 .xls（旧格式不支持，请先另存为 .xlsx）", len(s.xls)))
	}
	tips = append(tips, s.problems...)
	if len(tips) > 0 {
		p.line("")
		p.line("【四】提示")
		for _, t := range tips {
			p.line("  · %s", t)
		}
	}

	p.line("%s", line)
	if s.totalHits > 0 {
		p.line("待写入 %d 个文件 / %d 处替换。", countPending(s.records), s.totalHits)
	}
}

func countPending(recs []report.FileRecord) int {
	n := 0
	for _, r := range recs {
		if r.Hits > 0 {
			n++
		}
	}
	return n
}

// dirKey 取路径的前三段做目录分组，和 Python 版一致。
func dirKey(name string) string {
	parts := strings.Split(filepath.ToSlash(name), "/")
	if len(parts) > 3 {
		return strings.Join(parts[:3], "/")
	}
	dir := filepath.Dir(name)
	if dir == "." {
		return "."
	}
	return dir
}

func orEmptyText(s string) string {
	if s == "" {
		return "（空）"
	}
	return s
}

func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			w += 2
		default:
			w++
		}
	}
	return w
}

func pad(s string, w int) string {
	diff := w - dispWidth(s)
	if diff <= 0 {
		return s + " "
	}
	return s + strings.Repeat(" ", diff)
}

func clip(s string, w int) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\t", " ").Replace(s)
	if dispWidth(s) <= w {
		return s
	}
	out := ""
	for _, r := range s {
		if dispWidth(out+string(r)) > w-1 {
			break
		}
		out += string(r)
	}
	return out + "…"
}
