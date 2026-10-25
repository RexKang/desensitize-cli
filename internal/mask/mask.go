// Package mask 是脱敏引擎：把多条规则的命中合并成一次改写。
//
// 语义——从左到右扫描，同一位置按规则顺序取第一条，
// 被某个匹配消耗掉的文本不再参与后续匹配。
package mask

import (
	"sort"

	"github.com/RexKang/desensitize-cli/internal/rules"
)

// Hit 是一次实际替换。
type Hit struct {
	Rule     *rules.Rule
	Start    int
	End      int
	Original string
	After    string
	Groups   []string
}

type candidate struct {
	rule  *rules.Rule
	match rules.Match
}

// Apply 对 text 应用全部规则，返回新文本与命中列表（按位置升序）。
func Apply(text string, rs []*rules.Rule) (string, []Hit) {
	var cands []candidate
	for _, r := range rs {
		if !r.IsEnabled() {
			continue
		}
		for _, m := range r.FindAll(text) {
			cands = append(cands, candidate{rule: r, match: m})
		}
	}
	if len(cands) == 0 {
		return text, nil
	}

	// 左端点优先，其次规则次序；同一条规则内部不会重叠，所以这就等价于
	// 「单趟扫描 + 交替分支顺序」的结果。
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].match.Start != cands[j].match.Start {
			return cands[i].match.Start < cands[j].match.Start
		}
		return cands[i].rule.Index < cands[j].rule.Index
	})

	var hits []Hit
	lastEnd := 0
	for _, c := range cands {
		if c.match.Start < lastEnd {
			continue // 已被前面的匹配吃掉
		}
		original := text[c.match.Start:c.match.End]
		after := c.rule.Render(original, c.match.Groups)
		hits = append(hits, Hit{
			Rule:     c.rule,
			Start:    c.match.Start,
			End:      c.match.End,
			Original: original,
			After:    after,
			Groups:   c.match.Groups,
		})
		lastEnd = c.match.End
	}
	if len(hits) == 0 {
		return text, nil
	}

	var b []byte
	b = append(b, text[:hits[0].Start]...)
	for i, h := range hits {
		b = append(b, h.After...)
		next := len(text)
		if i+1 < len(hits) {
			next = hits[i+1].Start
		}
		b = append(b, text[h.End:next]...)
	}
	return string(b), hits
}

// CountOnly 判断文本里是否有命中（用于快速预筛，不做替换）。
func CountOnly(text string, rs []*rules.Rule) bool {
	for _, r := range rs {
		if !r.IsEnabled() {
			continue
		}
		if len(r.FindAll(text)) > 0 {
			return true
		}
	}
	return false
}
