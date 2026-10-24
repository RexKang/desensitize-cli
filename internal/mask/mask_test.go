package mask

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RexKang/desensitize-cli/internal/rules"
)

func load(t *testing.T, js string) []*rules.Rule {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	if err := os.WriteFile(p, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, _, err := rules.Load(p)
	if err != nil {
		t.Fatalf("规则加载失败: %v", err)
	}
	return rs
}

func TestApplyBasic(t *testing.T) {
	rs := load(t, `[
		{"id":"mobile","type":"regex","pattern":"1[3-9]\\d{9}","digit_boundary":true,"replace":"{stars}"},
		{"id":"mail","type":"regex","pattern":"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}","replace":"user@example.com"}
	]`)
	in := "联系 13800138000 或 user@example.org"
	out, hits := Apply(in, rs)
	if out != "联系 *********** 或 user@example.com" {
		t.Fatalf("输出 = %q", out)
	}
	if len(hits) != 2 {
		t.Fatalf("命中 = %d, 期望 2", len(hits))
	}
}

func TestPriorityEarlierRuleWins(t *testing.T) {
	// 邮箱在前时，手机号规则不该先吃掉 13800138000@example.com 里的数字段
	rs := load(t, `[
		{"id":"mail","type":"regex","pattern":"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}","replace":"MAIL"},
		{"id":"mobile","type":"regex","pattern":"1[3-9]\\d{9}","replace":"{stars}"}
	]`)
	out, _ := Apply("13800138000@example.com", rs)
	if out != "MAIL" {
		t.Fatalf("邮箱应优先整段命中，实际 %q", out)
	}
}

func TestNoOverlapReplacement(t *testing.T) {
	rs := load(t, `[{"id":"tag","type":"literal","find":"AP-","boundary":"word","replace":"SITE-"}]`)
	out, hits := Apply("AP-SRV01 MAP-01", rs)
	if out != "SITE-SRV01 MAP-01" {
		t.Fatalf("输出 = %q", out)
	}
	if len(hits) != 1 {
		t.Fatalf("命中 = %d", len(hits))
	}
}

func TestIdempotent(t *testing.T) {
	rs := load(t, `[{"id":"m","type":"regex","pattern":"1[3-9]\\d{9}","replace":"{stars}"}]`)
	once, _ := Apply("电话 13800138000", rs)
	twice, hits := Apply(once, rs)
	if twice != once || len(hits) != 0 {
		t.Fatalf("普通规则应幂等: %q -> %q (hits=%d)", once, twice, len(hits))
	}
}

func TestDeleteWholeCell(t *testing.T) {
	rs := load(t, `[{"id":"t","type":"literal","find":"TCK-","replace":""}]`)
	out, _ := Apply("TCK-0001", rs)
	if out != "0001" {
		t.Fatalf("删除前缀 = %q", out)
	}
}

func TestMultiHitInOneCell(t *testing.T) {
	rs := load(t, `[
		{"id":"t","type":"literal","find":"TCK-","replace":""},
		{"id":"tag","type":"literal","find":"AP-","boundary":"word","replace":"SITE-"}
	]`)
	out, _ := Apply("TCK-1-AP-2", rs)
	if out != "1-SITE-2" {
		t.Fatalf("同格多处替换 = %q", out)
	}
}
