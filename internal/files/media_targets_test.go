package files

// 真实世界文件形态的回归：openpyxl / WPS 写绝对关系目标，excelize 回写用成对标签。
// 这两条都和「文件看着能打开、图片还在包里」有关，不能用合成夹具蒙混过去。

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RexKang/desensitize-cli/internal/rules"
	"github.com/xuri/excelize/v2"
)

// ────────────────────────── 关系项解析 ──────────────────────────

func TestDropImageRelationshipsHandlesPairedTags(t *testing.T) {
	rels := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Target="media/a.png" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"></Relationship>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/xl/media/b.png" />` +
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="charts/c.xml"></Relationship>` +
		`</Relationships>`

	out, targets := dropImageRelationships("word/_rels/document.xml.rels", []byte(rels))
	if len(targets) != 2 {
		t.Fatalf("应认出 2 个图片关系，got %v", targets)
	}
	if targets[0] != "word/media/a.png" || targets[1] != "xl/media/b.png" {
		t.Errorf("target 解析错了: %v", targets)
	}
	got := string(out)
	for _, gone := range []string{"rId1", "rId2", "media/a.png", "xl/media/b.png"} {
		if strings.Contains(got, gone) {
			t.Errorf("图片关系没删: %s\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "rId3") || !strings.Contains(got, "charts/c.xml") {
		t.Errorf("图表关系不该动: %s", got)
	}
	if !xmlWellFormed(t, out) {
		t.Error("删完关系后 XML 不合法")
	}
}

func xmlWellFormed(t *testing.T, raw []byte) bool {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			t.Logf("XML 解析失败: %v", err)
			return false
		}
	}
}

// ────────────────────────── 绝对 Target 归一化 ──────────────────────────

func TestNormalizeAbsRelTargets(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parts := map[string]string{
		"_rels/.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="/word/document.xml"/>` +
			`</Relationships>`,
		"xl/worksheets/_rels/sheet1.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing" Target="/xl/drawings/drawing1.xml"></Relationship>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com/a.png" TargetMode="External"/>` +
			`</Relationships>`,
		"xl/drawings/_rels/drawing1.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/xl/media/image1.png"/>` +
			`</Relationships>`,
	}
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	out, changed, err := normalizeAbsRelTargets(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("有绝对 Target 却报告没改动")
	}
	files := zipEntries(t, out)
	want := map[string]string{
		"_rels/.rels":                         `Target="word/document.xml"`,
		"xl/worksheets/_rels/sheet1.xml.rels": `Target="../drawings/drawing1.xml"`,
		"xl/drawings/_rels/drawing1.xml.rels": `Target="../media/image1.png"`,
	}
	for name, sub := range want {
		if !strings.Contains(files[name], sub) {
			t.Errorf("%s 应含 %s，实际 %s", name, sub, files[name])
		}
		if strings.Contains(files[name], `Target="/`) {
			t.Errorf("%s 还留着绝对 Target: %s", name, files[name])
		}
	}
	if !strings.Contains(files["xl/worksheets/_rels/sheet1.xml.rels"], `Target="https://example.com/a.png"`) {
		t.Error("外部链接不该被改写")
	}

	// 已经是相对路径的包：不该报告改动
	if _, changed2, err := normalizeAbsRelTargets(out); err != nil || changed2 {
		t.Errorf("相对路径的包不该再动: changed=%v err=%v", changed2, err)
	}
}

// ────────────────────────── xlsx：绝对 Target 也要真删图 ──────────────────────────

// absolutizeRels 把包里所有内部关系目标改成 "/xl/..." 这种绝对写法，
// 模拟 openpyxl / WPS 的产物（Excel 自己写的是相对路径）。
func absolutizeRels(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	replaced := map[string][]byte{}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".rels") {
			continue
		}
		body, err := readEntry(f)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		last, changed := 0, false
		for _, m := range relElemRE.FindAllIndex(body, -1) {
			elem := body[m[0]:m[1]]
			if relExtMode.Match(elem) {
				continue
			}
			tg := relTargetRE.FindSubmatchIndex(elem)
			if tg == nil || strings.HasPrefix(string(elem[tg[2]:tg[3]]), "/") {
				continue
			}
			out.Write(body[last:m[0]])
			out.Write(elem[:tg[2]])
			out.WriteString("/" + resolveRelTarget(f.Name, string(elem[tg[2]:tg[3]])))
			out.Write(elem[tg[3]:])
			last = m[1]
			changed = true
		}
		if !changed {
			continue
		}
		out.Write(body[last:])
		replaced[f.Name] = out.Bytes()
	}
	if len(replaced) == 0 {
		t.Fatal("没有关系项可改，夹具不对")
	}
	data, err := rebuildZip(zr, replaced)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestXLSXAbsoluteTargetsStillStripImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openpyxl风格.xlsx")
	f := excelize.NewFile()
	if err := f.SetCellValue("Sheet1", "A1", "联系人 13800138000"); err != nil {
		t.Fatal(err)
	}
	if err := f.AddPicture("Sheet1", "C3", pngFixture(t), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()
	absolutizeRels(t, path) // 换成 openpyxl 的绝对写法

	pd, err := ProcessXLSXPasses(path, []Pass{{Rules: []*rules.Rule{stripRule(), mobileRule(t)}}}, NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Data == nil {
		t.Fatal("绝对 Target 的文件应产生写盘内容")
	}

	zr, err := zip.NewReader(bytes.NewReader(pd.Data), int64(len(pd.Data)))
	if err != nil {
		t.Fatalf("产物不是合法 zip: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range zr.File {
		if strings.HasPrefix(e.Name, "/") {
			t.Errorf("产物里有绝对路径条目: %s", e.Name)
		}
		if seen[e.Name] {
			t.Errorf("产物里有重名条目: %s", e.Name)
		}
		seen[e.Name] = true
		if strings.HasPrefix(e.Name, "xl/media/") {
			t.Errorf("图片字节还在包里: %s", e.Name)
		}
	}

	out := filepath.Join(dir, "out.xlsx")
	if err := os.WriteFile(out, pd.Data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("产物打不开: %v", err)
	}
	defer got.Close()
	for _, sheet := range got.GetSheetList() {
		cells, err := got.GetPictureCells(sheet)
		if err != nil {
			t.Fatal(err)
		}
		if len(cells) != 0 {
			t.Errorf("%s 还有图片: %v", sheet, cells)
		}
	}
	if v, _ := got.GetCellValue("Sheet1", "A1"); strings.Contains(v, "13800138000") {
		t.Errorf("文字没脱敏: %q", v)
	}
}

// ────────────────────────── xlsx：成对标签 + 页眉 logo 字节 ──────────────────────────

// excelizeStyleXLSX 造一个「excelize 写出来的形态」的包：成对 Relationship 标签、
// 绝对 Target、页眉 VML 图片，外加一个图表锚点（不该被删）。
func excelizeStyleXLSX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("xl/workbook.xml", `<workbook/>`)
	add("xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="s" xmlns:r="r"><sheetData/>`+
		`<headerFooter><oddHeader>&amp;L&amp;G</oddHeader><oddFooter>&amp;C第 &amp;P 页</oddFooter></headerFooter>`+
		`<drawing r:id="rId1"/><legacyDrawing r:id="rId2"/></worksheet>`)
	add("xl/worksheets/_rels/sheet1.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Target="/xl/drawings/drawing1.xml" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing"></Relationship>`+
		`<Relationship Id="rId2" Target="/xl/drawings/vmlDrawing1.vml" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/vmlDrawing"></Relationship>`+
		`</Relationships>`)
	add("xl/drawings/drawing1.xml", `<?xml version="1.0"?><xdr:wsDr xmlns:xdr="xdr" xmlns:a="a">`+
		`<xdr:twoCellAnchor><xdr:pic><a:blip r:embed="rId1"/></xdr:pic></xdr:twoCellAnchor>`+
		`<xdr:twoCellAnchor><xdr:graphicFrame/></xdr:twoCellAnchor></xdr:wsDr>`)
	add("xl/drawings/_rels/drawing1.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Target="/xl/media/image1.png" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"></Relationship>`+
		`</Relationships>`)
	add("xl/drawings/vmlDrawing1.vml", `<xml xmlns:v="v"><v:shapetype id="_x0000_t75"/>`+
		`<v:shape id="s1"><v:imagedata r:id="rId1"/></v:shape><v:shape id="s2"><v:textbox/></v:shape></xml>`)
	add("xl/drawings/_rels/vmlDrawing1.vml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Target="/xl/media/logo.png" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"></Relationship>`+
		`</Relationships>`)
	add("xl/media/image1.png", "fake-image")
	add("xl/media/logo.png", "fake-logo")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStripXLSXMediaPairedRelsDropsLogoBytes(t *testing.T) {
	out, pictures, names, err := stripXLSXMedia(excelizeStyleXLSX(t))
	if err != nil {
		t.Fatal(err)
	}
	if pictures != 2 {
		t.Errorf("应删 2 张图（单元格 + 页眉）, got %d", pictures)
	}
	if len(names) != 2 {
		t.Errorf("应报 2 个媒体文件名, got %v", names)
	}
	files := zipEntries(t, out)
	for name := range files {
		if strings.HasPrefix(name, "/") {
			t.Errorf("产物里有绝对路径条目: %s", name)
		}
		if strings.HasPrefix(name, "xl/media/") {
			t.Errorf("图片字节还在包里: %s", name)
		}
	}
	if strings.Contains(files["xl/worksheets/sheet1.xml"], "&amp;G") {
		t.Error("页眉的 &G 占位没清")
	}
	if !strings.Contains(files["xl/worksheets/sheet1.xml"], "&amp;C第") {
		t.Error("其它页眉页脚代码不该动")
	}
	drawing := files["xl/drawings/drawing1.xml"]
	if strings.Contains(drawing, "xdr:pic") {
		t.Errorf("单元格图片还在: %s", drawing)
	}
	if !strings.Contains(drawing, "graphicFrame") {
		t.Errorf("图表锚点不该删: %s", drawing)
	}
	vml := files["xl/drawings/vmlDrawing1.vml"]
	if strings.Contains(vml, "imagedata") {
		t.Errorf("页眉图片还在: %s", vml)
	}
	if !strings.Contains(vml, "textbox") {
		t.Errorf("非图片形状不该删: %s", vml)
	}
	// 关系与媒体都要清干净
	if strings.Contains(files["xl/drawings/_rels/drawing1.xml.rels"], "image1.png") {
		t.Error("单元格图片的关系没删")
	}
	if strings.Contains(files["xl/drawings/_rels/vmlDrawing1.vml.rels"], "logo.png") {
		t.Error("页眉 logo 的关系没删")
	}
}

// ────────────────────────── 媒体清扫：只删没人引用的 ──────────────────────────

// zipReaderOf 把文件表打成 *zip.Reader，专供清扫逻辑的单元测试。
func zipReaderOf(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestDropUnreferencedImagesKeepsEncodedTargets(t *testing.T) {
	// Target 里的空格会写成 %20，包内条目名是原文；按名字直接比会误判成「没人引用」
	zr := zipReaderOf(t, map[string]string{
		"word/charts/_rels/chart 1.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/chart%20fill.png"/>` +
			`</Relationships>`,
		"word/media/chart fill.png": "keep-me",
		"word/media/orphan.png":     "drop-me",
	})
	drop := map[string]bool{}
	got := dropUnreferencedImages(zr, nil, drop, "word/media/")

	if drop["word/media/chart fill.png"] {
		t.Error("被 %20 引用着的图片被误删了")
	}
	if !drop["word/media/orphan.png"] {
		t.Error("没人引用的孤立图片该删")
	}
	if len(got) != 1 || got[0] != "word/media/orphan.png" {
		t.Errorf("清扫结果 = %v，期望只剩孤立图", got)
	}
}

func TestDocxStripKeepsChartImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "图表带图.docx")
	writeZip(t, path, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="w" xmlns:a="a" xmlns:pic="pic" xmlns:r="r" xmlns:wp="wp" xmlns:c="c"><w:body>` +
			`<w:p><w:r><w:t>联系人 13800138000</w:t></w:r></w:p>` +
			`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><a:blip r:embed="rId4"/></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>` +
			`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><c:chart r:id="rId9"/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>` +
			`</w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/photo.png"></Relationship>` +
			`<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="charts/chart1.xml"/>` +
			`</Relationships>`,
		"word/charts/chart1.xml": `<c:chartSpace xmlns:c="c"><c:spPr><a:blipFill xmlns:a="a"><a:blip r:embed="rId1" xmlns:r="r"/></a:blipFill></c:spPr></c:chartSpace>`,
		"word/charts/_rels/chart1.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/chartfill.png"></Relationship>` +
			`</Relationships>`,
		"word/media/photo.png":     "photo",
		"word/media/chartfill.png": "chart-fill",
	})

	pd, err := ProcessDocxPasses(path, []Pass{{Rules: []*rules.Rule{stripRule(), mobileRule(t)}}}, NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Data == nil {
		t.Fatal("应产生写盘内容")
	}
	files := zipEntries(t, pd.Data)
	if _, ok := files["word/media/photo.png"]; ok {
		t.Error("正文图片没删")
	}
	if got, ok := files["word/media/chartfill.png"]; !ok || got != "chart-fill" {
		t.Errorf("图表自带的图片被误删: ok=%v", ok)
	}
	if !strings.Contains(files["word/charts/_rels/chart1.xml.rels"], "chartfill.png") {
		t.Error("图表关系被误删，chart XML 会留下坏引用")
	}
	if strings.Contains(files["word/document.xml"], "a:blip") {
		t.Error("正文图片元素还在")
	}
	if !strings.Contains(files["word/document.xml"], "c:chart") {
		t.Error("图表元素不该删")
	}
}
