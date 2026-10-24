// Package presets 提供内置的脱敏案例：一组规则 + 一段示例文本。
//
// 案例随二进制发布（go:embed），用户可以「点开看看这段脏数据会变成什么」，
// 再决定要不要把这套规则放进待执行队列。规则的字段与 rules.json 完全一致，
// 所以任何一个案例都能直接导出成规则文件，也能被 CLI 直接使用。
package presets

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/RexKang/desensitize-cli/internal/rules"
)

//go:embed presets.json
var raw []byte

// Preset 是一个案例。
type Preset struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Desc   string        `json:"desc"`
	Tags   []string      `json:"tags"`
	Risk   string        `json:"risk"` // 低 / 中 / 高
	Note   string        `json:"note"`
	Sample string        `json:"sample"`
	Rules  []*rules.Rule `json:"rules"`
}

type file struct {
	Version string    `json:"version"`
	Presets []*Preset `json:"presets"`
}

// Version 是案例库的版本，跟着二进制走。
var Version string

var (
	once  sync.Once
	cache []*Preset
	err   error
)

// All 返回全部案例（已编译好规则，按文件中的顺序）。
func All() ([]*Preset, error) {
	once.Do(func() {
		var f file
		if e := json.Unmarshal(raw, &f); e != nil {
			err = fmt.Errorf("内置案例库解析失败: %w", e)
			return
		}
		Version = f.Version
		seen := map[string]bool{}
		for _, p := range f.Presets {
			if p.ID == "" {
				err = fmt.Errorf("案例缺少 id")
				return
			}
			if seen[p.ID] {
				err = fmt.Errorf("案例 id 重复: %s", p.ID)
				return
			}
			seen[p.ID] = true
			for i, r := range p.Rules {
				r.Index = i
				if e := r.Compile(); e != nil {
					err = fmt.Errorf("案例 %s 的规则 %s 不合法: %w", p.ID, r.ID, e)
					return
				}
			}
			cache = append(cache, p)
		}
		if err != nil {
			cache = nil
		}
	})
	return cache, err
}

// Get 按 id 取案例。
func Get(id string) (*Preset, bool) {
	list, e := All()
	if e != nil {
		return nil, false
	}
	for _, p := range list {
		if p.ID == id {
			return p, true
		}
	}
	return nil, false
}

// IDs 返回全部案例 id，按内置顺序。
func IDs() ([]string, error) {
	list, e := All()
	if e != nil {
		return nil, e
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.ID)
	}
	return out, nil
}
