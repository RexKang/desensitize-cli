package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func mustRule(t *testing.T, js string) *Rule {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	if err := os.WriteFile(p, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, _, err := Load(p)
	if err != nil {
		t.Fatalf("加载规则失败: %v", err)
	}
	return rs[0]
}

func TestExpandTemplates(t *testing.T) {
	cases := []struct {
		tpl, want string
	}{
		{"00000000000", "00000000000"},
		{"{stars}", "***********"},
		{"{keep:4}", "8000"},
		{"{head:3}00000000", "13800000000"},
		{"{len}", "11"},
		{"", ""},
	}
	for _, c := range cases {
		r := &Rule{Type: TypeLiteral, Replace: c.tpl, Find: "x"}
		if got := r.Expand("13800138000", nil); got != c.want {
			t.Errorf("模板 %q: got %q want %q", c.tpl, got, c.want)
		}
	}
}

func TestExpandCaptureGroup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	_ = os.WriteFile(p, []byte(`[{"id":"land","type":"regex","pattern":"(0\\d{2,3}[- ]?\\d{4})\\d{4}","replace":"{g1}1234"}]`), 0o644)
	rs, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	got := rs[0].Render("010-88886666", []string{"010-8888"})
	if got != "010-88881234" {
		t.Fatalf("捕获组模板: %s", got)
	}
}

func TestLiteralWordBoundary(t *testing.T) {
	r := mustRule(t, `[{"id":"tag","type":"literal","find":"AP-","boundary":"word","replace":"SITE-"}]`)
	in := "AP-SRV01 MAP-01 xAP-9"
	got := r.FindAll(in)
	if len(got) != 1 {
		t.Fatalf("word 边界命中数 = %d, 期望 1（MAP- 和 xAP- 都不算）", len(got))
	}
	for _, m := range got {
		if in[m.Start:m.End] != "AP-" {
			t.Fatalf("命中内容异常: %q", in[m.Start:m.End])
		}
	}
}

func TestDigitsBoundary(t *testing.T) {
	r := mustRule(t, `[{"id":"n","type":"digits","length":8,"replace":"{stars}"}]`)
	for _, in := range []string{"12345678901234567890", "1234567890123"} {
		if got := r.FindAll(in); len(got) != 0 {
			t.Errorf("%s 不该命中长数字串，命中 %d 处", in, len(got))
		}
	}
	if got := r.FindAll("编号 12345678 结尾"); len(got) != 1 {
		t.Errorf("独立 8 位数字应命中，实际 %d", len(got))
	}
}

func TestSkipValidDate(t *testing.T) {
	r := mustRule(t, `[{"id":"n","type":"digits","length":8,"skip_valid_date":true,"replace":"{stars}"}]`)
	if got := r.FindAll("20260101"); len(got) != 0 {
		t.Error("合法日期应跳过")
	}
	if got := r.FindAll("20261301"); len(got) != 1 {
		t.Error("非法日期应命中")
	}
}

func TestIPShift(t *testing.T) {
	r := mustRule(t, `[{"id":"ip","type":"ip_shift","first_octet":203,"offset":7,"source_cidrs":["10.0.0.0/8","192.168.0.0/16"]}]`)
	cases := map[string]string{
		"10.1.2.3":       "203.8.9.10",
		"192.168.1.10":   "203.175.8.17",
		"10.255.255.255": "203.6.6.6",
	}
	for in, want := range cases {
		if got := r.ShiftIP(in); got != want {
			t.Errorf("shift(%s) = %s, 期望 %s", in, got, want)
		}
	}
	// 源网段外不动
	if got := r.FindAll("8.8.8.8"); len(got) != 0 {
		t.Error("非源网段不该命中")
	}
	// 完整四段才算 IP
	if got := r.FindAll("10.1.2-188"); len(got) != 0 {
		t.Error("三段数字不该当 IP")
	}
	if got := r.FindAll("10.1.2.3-188"); len(got) != 1 {
		t.Error("四段带后缀应当命中")
	}
	if got := r.FindAll("1.10.0.1"); len(got) != 0 {
		t.Error("1.10.0.1 不在 10/8 内，不该命中")
	}
}

func TestIPShiftPerOctet(t *testing.T) {
	r := mustRule(t, `[{"id":"ip","type":"ip_shift","first_octet":203,"offsets":[null,1,2,3]}]`)
	if got := r.ShiftIP("10.1.2.3"); got != "203.2.4.6" {
		t.Fatalf("逐段偏移: %s", got)
	}
}

func TestHashToken(t *testing.T) {
	r := mustRule(t, `[{"id":"h","type":"hash","pattern":"host-[a-z]+\\d+","keep_tail":2}]`)
	got := r.HashToken("host-web01")
	if len(got) != len("host-web01") {
		t.Fatalf("长度应保持: %q", got)
	}
	if got[len(got)-2:] != "01" {
		t.Fatalf("尾部应保留: %q", got)
	}
	if got != r.HashToken("host-web01") {
		t.Fatal("同输入应稳定")
	}
	if got == r.HashToken("host-db02") {
		t.Fatal("不同输入应不同")
	}
	salted := mustRule(t, `[{"id":"h","type":"hash","pattern":"host-[a-z]+\\d+","keep_tail":2,"salt":"p1"}]`)
	if salted.HashToken("host-web01") == got {
		t.Fatal("加盐后映射应不同")
	}
}

func TestLoadRejectsBadRule(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	_ = os.WriteFile(p, []byte(`[{"id":"x","type":"nope"}]`), 0o644)
	if _, _, err := Load(p); err == nil {
		t.Fatal("未知 type 应当报错")
	}
}

func TestLoadAcceptsBothShapes(t *testing.T) {
	dir := t.TempDir()
	arr := filepath.Join(dir, "a.json")
	obj := filepath.Join(dir, "b.json")
	_ = os.WriteFile(arr, []byte(`[{"id":"a","type":"literal","find":"x","replace":"y"}]`), 0o644)
	_ = os.WriteFile(obj, []byte(`{"version":"0.2.0","rules":[{"id":"a","type":"literal","find":"x","replace":"y"}]}`), 0o644)
	for _, p := range []string{arr, obj} {
		rs, _, err := Load(p)
		if err != nil || len(rs) != 1 {
			t.Fatalf("%s 加载失败: %v", p, err)
		}
	}
}

func TestDefaultEnabled(t *testing.T) {
	r := mustRule(t, `[{"id":"a","type":"literal","find":"x"}]`)
	if !r.IsEnabled() {
		t.Fatal("缺省应视为启用")
	}
}
