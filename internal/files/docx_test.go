package files

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// makeDocx 造一个最小但结构完整的 docx：parts 是 zip 条目 → 内容。
func makeDocx(t *testing.T, name string, parts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func docxParts(body string) map[string]string {
	return map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			body + `</w:body></w:document>`,
		"word/styles.xml": `<w:styles>保持原样</w:styles>`,
	}
}

func readZipEntry(t *testing.T, path, name string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			b, err := readEntry(f)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatalf("zip 里没有 %s", name)
	return nil
}

// docxText 取出所有 <w:t> 的文字，用来断言肉眼能读到的内容。
func docxText(xmlSrc []byte) string {
	var b strings.Builder
	rest := string(xmlSrc)
	for {
		i := strings.Index(rest, "<w:t")
		if i < 0 {
			break
		}
		gt := strings.Index(rest[i:], ">")
		if gt < 0 {
			break
		}
		start := i + gt + 1
		end := strings.Index(rest[start:], "</w:t>")
		if end < 0 {
			break
		}
		b.WriteString(xmlUnescape(rest[start : start+end]))
		rest = rest[start+end+len("</w:t>"):]
	}
	return b.String()
}

func TestProcessDocxSingleRun(t *testing.T) {
	body := `<w:p><w:r><w:t>联系人 13800138000 电话</w:t></w:r></w:p>`
	path := makeDocx(t, "a.docx", docxParts(body))
	before, _ := os.ReadFile(path)
	stats := NewStats()
	pd, err := ProcessDocx(path, testRules(t), stats, false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Hits != 1 {
		t.Fatalf("应当命中 1 处, got %+v", pd)
	}
	// 没调 Write 之前，磁盘上的文件必须原样不动
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, before) {
		t.Fatal("写盘前文件被改了")
	}
	if err := pd.Write(false); err != nil {
		t.Fatal(err)
	}
	got := docxText(readZipEntry(t, path, "word/document.xml"))
	if strings.Contains(got, "13800138000") {
		t.Fatalf("手机号没被替换: %q", got)
	}
	if !strings.Contains(got, "联系人") || !strings.Contains(got, "电话") {
		t.Fatalf("正文其余文字被破坏: %q", got)
	}
}

// Word 常把一句话拆进多个 run：命中横跨 run 时也要替换成功，且命中之外的格式不能丢。
func TestProcessDocxCrossRun(t *testing.T) {
	body := `<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>手机 13800138</w:t></w:r>` +
		`<w:r><w:rPr><w:i/></w:rPr><w:t>000 结束</w:t></w:r></w:p>`
	path := makeDocx(t, "b.docx", docxParts(body))
	pd, err := ProcessDocx(path, testRules(t), NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Hits != 1 {
		t.Fatalf("跨 run 也要命中 1 处, got %+v", pd)
	}
	if err := pd.Write(false); err != nil {
		t.Fatal(err)
	}
	xmlSrc := readZipEntry(t, path, "word/document.xml")
	got := docxText(xmlSrc)
	if strings.Contains(got, "13800138000") {
		t.Fatalf("跨 run 的手机号没被替换: %q", got)
	}
	if !strings.Contains(got, "结束") {
		t.Fatalf("命中之后的文字丢了: %q", got)
	}
	if !bytes.Contains(xmlSrc, []byte("<w:b/>")) || !bytes.Contains(xmlSrc, []byte("<w:i/>")) {
		t.Fatal("run 的格式标记被破坏了")
	}
}

func TestProcessDocxKeepsOtherEntries(t *testing.T) {
	parts := docxParts(`<w:p><w:r><w:t>13800138000</w:t></w:r></w:p>`)
	parts["word/media/image1.png"] = "\x89PNG\r\n\x1a\n二进制占位"
	parts["docProps/core.xml"] = `<cp:coreProperties/>`
	path := makeDocx(t, "c.docx", parts)

	pd, err := ProcessDocx(path, testRules(t), NewStats(), false)
	if err != nil || pd == nil {
		t.Fatalf("处理失败: %v", err)
	}
	before := readZipEntry(t, path, "word/media/image1.png")
	if err := pd.Write(false); err != nil {
		t.Fatal(err)
	}
	after := readZipEntry(t, path, "word/media/image1.png")
	if !bytes.Equal(before, after) {
		t.Fatal("图片条目被改动了")
	}
	if got := string(readZipEntry(t, path, "word/styles.xml")); got != `<w:styles>保持原样</w:styles>` {
		t.Fatalf("样式条目被改动: %q", got)
	}
	// 条目顺序要跟原来一致（Word 对此敏感）
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := []string{"[Content_Types].xml", "docProps/core.xml", "word/document.xml", "word/media/image1.png", "word/styles.xml"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("条目顺序变了: %v", names)
	}
}

func TestProcessDocxDryRunDoesNotTouchFile(t *testing.T) {
	path := makeDocx(t, "d.docx", docxParts(`<w:p><w:r><w:t>13800138000</w:t></w:r></w:p>`))
	before, _ := os.ReadFile(path)
	pd, err := ProcessDocx(path, testRules(t), NewStats(), true)
	if err != nil || pd == nil || pd.Hits != 1 {
		t.Fatalf("空跑应当命中但不再写: pd=%+v err=%v", pd, err)
	}
	if len(pd.Data) != 0 {
		t.Error("空跑不该生成新字节")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("空跑把文件改了")
	}
}

func TestProcessDocxHeaderAndNotes(t *testing.T) {
	parts := docxParts(`<w:p><w:r><w:t>正文无敏感信息</w:t></w:r></w:p>`)
	parts["word/header1.xml"] = `<w:hdr xmlns:w="x"><w:p><w:r><w:t>页眉 13800138000</w:t></w:r></w:p></w:hdr>`
	parts["word/footnotes.xml"] = `<w:footnotes><w:p><w:r><w:t>脚注 13800138001</w:t></w:r></w:p></w:footnotes>`
	path := makeDocx(t, "e.docx", parts)

	stats := NewStats()
	pd, err := ProcessDocx(path, testRules(t), stats, false)
	if err != nil || pd == nil {
		t.Fatalf("处理失败: %v", err)
	}
	if pd.Hits != 2 {
		t.Fatalf("页眉页脚各 1 处，共 2 处, got %d", pd.Hits)
	}
	var locs []string
	for _, d := range stats.Order {
		locs = append(locs, d.Loc)
	}
	joined := strings.Join(locs, " ")
	if !strings.Contains(joined, "页眉1") || !strings.Contains(joined, "脚注") {
		t.Errorf("位置标注不对: %v", locs)
	}
}

func TestProcessDocxNoHitReturnsNil(t *testing.T) {
	path := makeDocx(t, "f.docx", docxParts(`<w:p><w:r><w:t>干干净净</w:t></w:r></w:p>`))
	pd, err := ProcessDocx(path, testRules(t), NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd != nil {
		t.Fatalf("没有命中应当返回 nil, got %+v", pd)
	}
}

func TestProcessDocxEntities(t *testing.T) {
	// 正文里带 &amp; 和 &#x4E2D; 这类实体，替换后 XML 仍要合法
	body := `<w:p><w:r><w:t>A&amp;B &#x4E2D;13800138000</w:t></w:r></w:p>`
	path := makeDocx(t, "g.docx", docxParts(body))
	pd, err := ProcessDocx(path, testRules(t), NewStats(), false)
	if err != nil || pd == nil {
		t.Fatalf("处理失败: %v", err)
	}
	if err := pd.Write(false); err != nil {
		t.Fatal(err)
	}
	got := docxText(readZipEntry(t, path, "word/document.xml"))
	if !strings.Contains(got, "A&B") || !strings.Contains(got, "中") {
		t.Fatalf("实体被破坏: %q", got)
	}
	if strings.Contains(got, "13800138000") {
		t.Fatalf("没替换: %q", got)
	}
}

func TestDiscoverDocxAndDoc(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.docx", "b.doc", "c.xls", "d.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, _, legacy := Discover([]string{dir}, true)
	sort.Strings(got)
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	if strings.Join(names, ",") != "a.docx,d.txt" {
		t.Errorf("应收 .docx 和 .txt, got %v", names)
	}
	if len(legacy) != 2 {
		t.Errorf("应跳过 .doc 和 .xls, got %v", legacy)
	}
}
