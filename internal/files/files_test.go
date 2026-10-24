package files

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RexKang/desensitize-cli/internal/rules"
	"github.com/xuri/excelize/v2"
)

func testRules(t *testing.T) []*rules.Rule {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "r.json")
	js := `[
	 {"id":"mobile","type":"regex","pattern":"1[3-9]\\d{9}","digit_boundary":true,"replace":"{stars}"},
	 {"id":"mail","type":"regex","pattern":"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\\.[A-Za-z]{2,}","replace":"user@example.com"},
	 {"id":"ip","type":"ip_shift","first_octet":203,"offset":7,"source_cidrs":["10.0.0.0/8","192.168.0.0/16"]},
	 {"id":"t","type":"literal","find":"TCK-","replace":""},
	 {"id":"tag","type":"literal","find":"AP-","boundary":"word","replace":"SITE-"}
	]`
	if err := os.WriteFile(p, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, _, err := rules.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestDetectEncoding(t *testing.T) {
	cases := []struct {
		raw  []byte
		name string
	}{
		{[]byte("plain ascii"), "utf-8"},
		{append([]byte{0xEF, 0xBB, 0xBF}, []byte("中文")...), "utf-8"},
		{append([]byte{0xFF, 0xFE}, []byte("中\x00")...), "utf-16-le"},
		{append([]byte{0xFE, 0xFF}, []byte("\x00中")...), "utf-16-be"},
		{[]byte{0xD6, 0xD0, 0xCE, 0xC4}, "gb18030"}, // "中文" 的 GB 编码
	}
	for _, c := range cases {
		_, _, name, ok := DetectEncoding(c.raw)
		if !ok || name != c.name {
			t.Errorf("编码探测 %q: got %q ok=%v want %q", c.raw, name, ok, c.name)
		}
	}
}

func TestProcessTextKeepsEncoding(t *testing.T) {
	rs := testRules(t)
	dir := t.TempDir()

	// GB18030
	gb := filepath.Join(dir, "a.md")
	if err := os.WriteFile(gb, []byte{0xD6, 0xD0, 0xCE, 0xC4, ' ', '1', '3', '8', '0', '0', '1', '3', '8', '0', '0', '0'}, 0o644); err != nil {
		t.Fatal(err)
	}
	pd, err := ProcessText(gb, rs, NewStats())
	if err != nil || pd == nil {
		t.Fatalf("GB18030 处理失败: %v", err)
	}
	if _, _, name, _ := DetectEncoding(pd.Data); name != "gb18030" {
		t.Fatalf("编码未保持: %s", name)
	}

	// UTF-16 LE + BOM
	u16 := filepath.Join(dir, "b.txt")
	body := "\ufeff联系人 13800138000\n"
	var raw []byte
	raw = append(raw, 0xFF, 0xFE)
	for _, r := range body[1:] {
		raw = append(raw, byte(r), byte(r>>8))
	}
	if err := os.WriteFile(u16, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	pd, err = ProcessText(u16, rs, NewStats())
	if err != nil || pd == nil {
		t.Fatalf("UTF-16 处理失败: %v", err)
	}
	if !(len(pd.Data) > 2 && pd.Data[0] == 0xFF && pd.Data[1] == 0xFE) {
		t.Fatal("BOM 未保留")
	}
	if string(pd.Data[2:]) == string(raw[2:]) {
		t.Fatal("内容未改写")
	}
}

func TestProcessXLSX(t *testing.T) {
	rs := testRules(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.xlsx")

	f := excelize.NewFile()
	sheet := "工单"
	_ = f.SetSheetName("Sheet1", sheet)
	_ = f.SetCellValue(sheet, "A1", "单号")
	_ = f.SetCellValue(sheet, "A2", "TCK-0001")         // 文本 + 前导零
	_ = f.SetCellValue(sheet, "C2", 13800138000)        // 数字
	_ = f.SetCellValue(sheet, "D2", "user@example.org") // 文本
	_ = f.SetCellValue(sheet, "E2", "10.1.2.3")         // 文本 IP
	_ = f.SetCellValue(sheet, "F2", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	_ = f.SetCellValue(sheet, "G2", "联系 13800138000 或 AP-SRV01")
	_ = f.SetCellValue(sheet, "I2", "TCK-1-AP-2")
	_ = f.SetCellFormula(sheet, "H2", "=C2")
	dateRaw, _ := f.GetCellValue(sheet, "F2", excelize.Options{RawCellValue: true})
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	pd, err := ProcessXLSX(path, rs, NewStats(), false)
	if err != nil || pd == nil {
		t.Fatalf("xlsx 处理失败: %v", err)
	}
	if err := pd.Write(false); err != nil {
		t.Fatal(err)
	}

	got, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()

	if v, _ := got.GetCellValue(sheet, "A2"); v != "0001" {
		t.Errorf("前导零应保留为文本: %q", v)
	}
	if v, _ := got.GetCellValue(sheet, "C2"); v != "***********" {
		t.Errorf("数字格应换成星号: %q", v)
	}
	if v, _ := got.GetCellValue(sheet, "D2"); v != "user@example.com" {
		t.Errorf("邮箱: %q", v)
	}
	if v, _ := got.GetCellValue(sheet, "E2"); v != "203.8.9.10" {
		t.Errorf("IP: %q", v)
	}
	if v, _ := got.GetCellValue(sheet, "F2", excelize.Options{RawCellValue: true}); v != dateRaw {
		t.Errorf("日期应保持原值: got %q want %q", v, dateRaw)
	}
	if v, _ := got.GetCellFormula(sheet, "H2"); v != "=C2" {
		t.Errorf("公式应保留: %q", v)
	}
	if v, _ := got.GetCellValue(sheet, "G2"); v != "联系 *********** 或 SITE-SRV01" {
		t.Errorf("文本格内嵌替换: %q", v)
	}
	if v, _ := got.GetCellValue(sheet, "I2"); v != "1-SITE-2" {
		t.Errorf("同格多处: %q", v)
	}
}

func TestProcessTextNoHit(t *testing.T) {
	rs := testRules(t)
	path := filepath.Join(t.TempDir(), "clean.txt")
	_ = os.WriteFile(path, []byte("干净内容\n"), 0o644)
	pd, err := ProcessText(path, rs, NewStats())
	if err != nil || pd != nil {
		t.Fatalf("无命中应返回 nil: err=%v pd=%v", err, pd)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	must := func(p, body string) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "a.txt"), "x")
	must(filepath.Join(root, "n1", "n2", "deep.md"), "x")
	must(filepath.Join(root, ".hidden", "skip.txt"), "x")
	must(filepath.Join(root, "old.xls"), "x")
	must(filepath.Join(root, "~$lock.xlsx"), "x")

	got, problems, xls := Discover([]string{root}, true)
	names := map[string]bool{}
	for _, g := range got {
		names[filepath.Base(g)] = true
	}
	if !names["a.txt"] || !names["deep.md"] {
		t.Fatalf("递归发现异常: %v", names)
	}
	if names["skip.txt"] || names["old.xls"] || names["~$lock.xlsx"] {
		t.Fatalf("不该收录: %v", names)
	}
	if len(xls) != 1 {
		t.Fatalf(".xls 应单独统计, got %d", len(xls))
	}
	_ = problems

	shallow, _, _ := Discover([]string{root}, false)
	for _, g := range shallow {
		if filepath.Base(g) == "deep.md" {
			t.Fatal("--no-recursive 不该进子目录")
		}
	}
}

func TestBackupAndAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(path, []byte("old"), 0o644)

	pd := &Pending{Path: path, Data: []byte("new"), Hits: 1}
	if err := pd.Write(true); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(path); string(body) != "new" {
		t.Fatalf("写入失败: %q", body)
	}
	entries, _ := os.ReadDir(dir)
	bak := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			bak++
		}
	}
	if bak != 1 {
		t.Fatalf("应生成 1 个备份, 实际 %d", bak)
	}
	if _, err := os.Stat(path + ".desensitize.tmp"); err == nil {
		t.Fatal("临时文件应被清理")
	}
}

func TestStatsContext(t *testing.T) {
	text := "前缀 13800138000 后缀"
	start := len("前缀 ")
	ctx := Context(text, start, start+11, 3)
	if ctx != "缀⟦13800138000⟧ 后" && ctx != "缀⟦13800138000⟧ 后…" {
		t.Logf("ctx = %q", ctx)
	}
	if !containsAll(ctx, "⟦", "⟧") {
		t.Fatalf("上下文应带标记: %q", ctx)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
