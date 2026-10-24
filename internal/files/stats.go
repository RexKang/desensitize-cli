package files

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/RexKang/desensitize-cli/internal/mask"
)

// Detail 是一条去重后的明细。
type Detail struct {
	File   string
	RuleID string
	Label  string
	Loc    string
	Before string
	After  string
	Ctx    string
	Count  int
}

// Stats 汇总命中，供终端与报告使用。
type Stats struct {
	detailCap   int
	index       map[string]*Detail
	ruleFileSet map[string]map[string]bool
	Order       []*Detail

	RuleHits  map[string]int
	RuleFiles map[string]int
	Noop      map[string]int
}

// NewStats 建一个计数器。
func NewStats() *Stats {
	return &Stats{
		detailCap:   20000,
		index:       map[string]*Detail{},
		ruleFileSet: map[string]map[string]bool{},
		RuleHits:    map[string]int{},
		RuleFiles:   map[string]int{},
		Noop:        map[string]int{},
	}
}

// LocatorFunc 把字节偏移转成可读位置（行号或 sheet!单元格）。
type LocatorFunc func(int) string

// Add 记录一次命中。ctx 由原文与偏移现算，前缀类规则也能看到完整上下文。
func (s *Stats) Add(file string, h mask.Hit, text string, loc LocatorFunc) {
	if h.After == h.Original {
		s.Noop[h.Rule.ID]++
		return
	}
	position := loc(h.Start)
	s.RuleHits[h.Rule.ID]++
	if s.ruleFileSet[h.Rule.ID] == nil {
		s.ruleFileSet[h.Rule.ID] = map[string]bool{}
	}
	if !s.ruleFileSet[h.Rule.ID][file] {
		s.ruleFileSet[h.Rule.ID][file] = true
		s.RuleFiles[h.Rule.ID]++
	}
	key := strings.Join([]string{file, h.Rule.ID, position, h.Original, h.After}, "\x00")
	if d, ok := s.index[key]; ok {
		d.Count++
		return
	}
	if len(s.Order) >= s.detailCap {
		return
	}
	d := &Detail{
		File:   file,
		RuleID: h.Rule.ID,
		Label:  h.Rule.Label(),
		Loc:    position,
		Before: h.Original,
		After:  h.After,
		Ctx:    Context(text, h.Start, h.End, 40),
		Count:  1,
	}
	s.index[key] = d
	s.Order = append(s.Order, d)
}

// Context 取命中两侧若干 rune，命中片段用 ⟦⟧ 标出。
func Context(text string, start, end, radius int) string {
	pre := ""
	i := start
	for n := 0; n < radius && i > 0; n++ {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		pre = string(r) + pre
		i -= size
	}
	post := ""
	j := end
	for n := 0; n < radius && j < len(text); n++ {
		r, size := utf8.DecodeRuneInString(text[j:])
		post += string(r)
		j += size
	}
	lead, trail := "", ""
	if i > 0 {
		lead = "…"
	}
	if j < len(text) {
		trail = "…"
	}
	snip := lead + pre + "⟦" + text[start:end] + "⟧" + post + trail
	return sanitize(snip)
}

func sanitize(s string) string {
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	return r.Replace(s)
}

// FileHit 是单个文件的命中汇总。
type FileHit struct {
	Path  string
	Hits  int
	Notes []string
}

// Summary 是天级别的汇总数字。
type Summary struct {
	RuleHits  map[string]int
	RuleFiles map[string]int
	Noop      map[string]int
	Details   []*Detail
}

// Summary 导出快照。
func (s *Stats) Summary() Summary {
	return Summary{RuleHits: s.RuleHits, RuleFiles: s.RuleFiles, Noop: s.Noop, Details: s.Order}
}

// SortedRuleIDs 按命中数降序返回规则 id。
func SortedRuleIDs(hits map[string]int) []string {
	ids := make([]string, 0, len(hits))
	for id := range hits {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && hits[ids[j]] > hits[ids[j-1]]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

// TopValues 统计某规则最高频的原文。
func (s *Stats) TopValues(ruleID string, n int) []struct {
	Value string
	Count int
} {
	counts := map[string]int{}
	for _, d := range s.Order {
		if d.RuleID == ruleID {
			counts[d.Before] += d.Count
		}
	}
	type kv struct {
		Value string
		Count int
	}
	var all []kv
	for v, c := range counts {
		all = append(all, kv{v, c})
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].Count > all[j-1].Count; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	out := make([]struct {
		Value string
		Count int
	}, 0, len(all))
	for _, x := range all {
		out = append(out, struct {
			Value string
			Count int
		}{x.Value, x.Count})
	}
	return out
}

// Describe 给终端提示用。
func (s *Stats) Describe() string {
	return fmt.Sprintf("%d 条规则有命中", len(s.RuleHits))
}
