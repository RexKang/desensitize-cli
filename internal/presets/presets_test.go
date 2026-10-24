package presets

import (
	"strings"
	"testing"

	"github.com/RexKang/desensitize-cli/internal/mask"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

func applyPreset(t *testing.T, p *Preset, forceEnable bool) (string, int) {
	t.Helper()
	rs := rules.Clone(p.Rules)
	if forceEnable {
		for _, r := range rs {
			on := true
			r.Enabled = &on
		}
	}
	out, hits := mask.Apply(p.Sample, rs)
	return out, len(hits)
}

func TestPresetsLoad(t *testing.T) {
	list, err := All()
	if err != nil {
		t.Fatalf("案例库加载失败: %v", err)
	}
	if len(list) < 10 {
		t.Fatalf("案例太少: %d 个，至少要 10 个", len(list))
	}
	if Version == "" {
		t.Error("案例库版本为空")
	}
	seen := map[string]bool{}
	for _, p := range list {
		if seen[p.ID] {
			t.Errorf("id 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" || p.Desc == "" || p.Sample == "" || p.Note == "" {
			t.Errorf("%s 字段不全（name/desc/sample/note 都要有）", p.ID)
		}
		switch p.Risk {
		case "低", "中", "高":
		default:
			t.Errorf("%s 的 risk 只能是 低/中/高，实际 %q", p.ID, p.Risk)
		}
		if len(p.Rules) == 0 {
			t.Errorf("%s 没有规则", p.ID)
		}
		for _, r := range p.Rules {
			if r.Desc == "" {
				t.Errorf("%s 的规则 %s 缺少 desc", p.ID, r.ID)
			}
		}
		if len(p.Tags) == 0 {
			t.Errorf("%s 没有标签", p.ID)
		}
	}
}

// 每个案例的示例文本都必须真的被这套规则改掉，否则案例是死的。
func TestEveryPresetChangesItsSample(t *testing.T) {
	list, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		allOff := true
		for _, r := range p.Rules {
			if r.IsEnabled() {
				allOff = false
				break
			}
		}
		out, hits := applyPreset(t, p, allOff) // 全停用的案例强制打开再测
		if hits == 0 {
			t.Errorf("%s 的示例文本没有任何命中，案例无效", p.ID)
			continue
		}
		if out == p.Sample {
			t.Errorf("%s 命中 %d 处但文本没变", p.ID, hits)
		}
	}
}

func TestPresetExpectations(t *testing.T) {
	cases := []struct {
		id        string
		force     bool
		mustHave  []string
		mustNotIn []string
	}{
		{
			id:        "ticket",
			mustHave:  []string{"2026-0001", "user@example.com", "***********"},
			mustNotIn: []string{"TCK-", "13800138000", "host-web01"},
		},
		{
			id:        "excel-form",
			mustHave:  []string{"******************", "203.8.9.10", "user@example.com"},
			mustNotIn: []string{"13800138000", "110101199001011234", "10.1.2.3"},
		},
		{
			id:        "network",
			mustHave:  []string{"203."},
			mustNotIn: []string{"10.1.2.3", "192.168.1.20", "host-core01"},
		},
		{
			id:        "logs",
			mustHave:  []string{"00000000-0000-0000-0000-000000000000"},
			mustNotIn: []string{"3f2504e0-4f89-11d3-9a0c-0305e82c3301", "13800138000"},
		},
		{
			id:        "api-json",
			mustHave:  []string{`"card":"0123"`, "user@example.com"},
			mustNotIn: []string{"6222021234567890123", "110101199001011234"},
		},
		{
			id:        "contact",
			mustHave:  []string{"6666", "user@example.com"},
			mustNotIn: []string{"13800138000", "010-88886666"},
		},
		{
			id:        "config",
			mustHave:  []string{"password = ", "***", "203."},
			mustNotIn: []string{"P@ssw0rd123", "abc123456789", "10.1.2.3"},
		},
		{
			id:        "audit",
			mustHave:  []string{"admin", "203."},
			mustNotIn: []string{"13800138000", "10.1.2.3", "host-web01"},
		},
		{
			id:        "plate",
			mustHave:  []string{"京A*****"},
			mustNotIn: []string{"京A12345", "粤B8888D"},
		},
		{
			id:        "serial",
			force:     true,
			mustHave:  []string{"0086"},
			mustNotIn: []string{"10000086"},
		},
	}
	list, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var p *Preset
		for _, x := range list {
			if x.ID == c.id {
				p = x
				break
			}
		}
		if p == nil {
			t.Errorf("案例不存在: %s", c.id)
			continue
		}
		out, _ := applyPreset(t, p, c.force)
		for _, want := range c.mustHave {
			if !strings.Contains(out, want) {
				t.Errorf("%s: 结果里应有 %q\n实际: %s", c.id, want, out)
			}
		}
		for _, bad := range c.mustNotIn {
			if strings.Contains(out, bad) {
				t.Errorf("%s: 结果里不该再有 %q\n实际: %s", c.id, bad, out)
			}
		}
	}
}

// 高危案例必须默认关闭，避免用户一键把编号全打掉。
func TestHighRiskPresetDefaultsOff(t *testing.T) {
	p, ok := Get("serial")
	if !ok {
		t.Fatal("serial 案例缺失")
	}
	for _, r := range p.Rules {
		if r.IsEnabled() {
			t.Fatalf("%s 的规则 %s 默认应当是停用状态", p.ID, r.ID)
		}
	}
	if p.Risk != "高" {
		t.Errorf("serial 的 risk 应为 高，实际 %q", p.Risk)
	}
}

func TestGetAndIDs(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("没有案例 id")
	}
	if _, ok := Get(ids[0]); !ok {
		t.Fatalf("按 id 取不到 %s", ids[0])
	}
	if _, ok := Get("不存在的案例"); ok {
		t.Fatal("不存在的 id 不该返回 true")
	}
}
