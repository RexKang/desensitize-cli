package files

import (
	"github.com/RexKang/desensitize-cli/internal/mask"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

// Pass 是一条待执行策略：一个名字 + 一组规则。
//
// 界面上「编辑好的策略加入待执行队列」就是这个东西。多条 Pass 按顺序作用在
// 同一份内容的每个单元上（一个文本文件、一个单元格、一个段落），
// 最后只写一次盘——既满足「顺序执行」的直觉，又不会让后面的策略重复脱敏
// 前一步的产物（ip_shift / hash 重跑结果会变，串行写盘是危险的）。
type Pass struct {
	Name  string        `json:"name"`
	Rules []*rules.Rule `json:"rules"`
}

// SinglePass 把单组规则包成一条策略，兼容旧调用。
func SinglePass(rs []*rules.Rule) []Pass { return []Pass{{Rules: rs}} }

// ApplyPasses 依次套用多条策略，返回最终文本与每段的结果。
//
// 每段用「上一段的输出」作为输入，所以命中位置、上下文都按当段的文本来算。
func ApplyPasses(text string, passes []Pass) (string, [][]mask.Hit) {
	cur := text
	var out [][]mask.Hit
	for _, p := range passes {
		next, hits := mask.Apply(cur, p.Rules)
		out = append(out, hits)
		cur = next
	}
	return cur, out
}
