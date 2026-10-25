package files

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RexKang/desensitize-cli/internal/rules"
	"github.com/xuri/excelize/v2"
)

// ────────────────────────────── 夹具 ──────────────────────────────

// writeZip 按给定文件表造一个 zip（docx / xlsx 都是 zip）。
func writeZip(t *testing.T, path string, files map[string]string) {
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
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func zipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析 zip 失败: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if _, err := b.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
		out[f.Name] = b.String()
	}
	return out
}

func stripRule() *rules.Rule {
	r := &rules.Rule{ID: "noimg", Desc: "删除图片", Type: rules.TypeStripImages}
	if err := r.Compile(); err != nil {
		panic(err)
	}
	return r
}

func mobileRule(t *testing.T) *rules.Rule {
	t.Helper()
	r := &rules.Rule{ID: "mobile", Desc: "手机号", Type: rules.TypeRegex, Pattern: `1[3-9]\d{9}`, Replace: "{stars}"}
	if err := r.Compile(); err != nil {
		t.Fatal(err)
	}
	return r
}

// docxWithImages 造一份带图片的 docx：
// 正文里两张图（一张真图片、一张图表），页眉里一张 VML 图片。
func docxWithImages(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "带图.docx")
	writeZip(t, path, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="w" xmlns:a="a" xmlns:pic="pic" xmlns:c="c" xmlns:r="r" xmlns:wp="wp"><w:body>` +
			`<w:p><w:r><w:t>联系人 13800138000</w:t></w:r></w:p>` +
			`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rId4"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>` +
			`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><c:chart r:id="rId9"/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>` +
			`</w:body></w:document>`,
		"word/header1.xml": `<?xml version="1.0"?><w:hdr xmlns:w="w" xmlns:v="v" xmlns:r="r"><w:p><w:r><w:pict><v:shape><v:imagedata r:id="rId1"/></v:shape></w:pict></w:r></w:p><w:p><w:r><w:t>页眉 13900139000</w:t></w:r></w:p></w:hdr>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>` +
			`<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="charts/chart1.xml"/>` +
			`</Relationships>`,
		"word/_rels/header1.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.jpeg"/>` +
			`</Relationships>`,
		"word/media/image1.png":  "fakepng",
		"word/media/image2.jpeg": "fakejpeg",
		"word/charts/chart1.xml": `<c:chartSpace xmlns:c="c"/>`,
		"word/styles.xml":        `<w:styles xmlns:w="w"/>`,
		"docProps/core.xml":      `<cp:coreProperties xmlns:cp="cp"/>`,
		"docProps/app.xml":       `<Properties xmlns="p"/>`,
	})
	return path
}

// ────────────────────────────── docx ──────────────────────────────

func TestDocxStripImagesRemovesBodyAndHeader(t *testing.T) {
	path := docxWithImages(t)
	stats := NewStats()
	passes := []Pass{{Rules: []*rules.Rule{stripRule(), mobileRule(t)}}}

	pd, err := ProcessDocxPasses(path, passes, stats, false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil {
		t.Fatal("有图片和手机号，应该有改动")
	}
	if pd.Hits != 4 { // 2 张图 + 正文 1 个手机号 + 页眉 1 个手机号
		t.Errorf("Hits = %d, 期望 4", pd.Hits)
	}
	if stats.RuleHits["noimg"] != 2 {
		t.Errorf("删除图片命中 = %d, 期望 2", stats.RuleHits["noimg"])
	}
	if stats.RuleFiles["noimg"] != 1 {
		t.Errorf("删除图片文件数 = %d, 期望 1", stats.RuleFiles["noimg"])
	}

	files := zipEntries(t, pd.Data)
	for name := range files {
		if strings.HasPrefix(name, "word/media/") {
			t.Errorf("媒体文件没删干净: %s", name)
		}
	}
	doc := files["word/document.xml"]
	if strings.Contains(doc, "<w:drawing>") && strings.Contains(doc, "a:blip") {
		t.Errorf("正文图片还在: %s", doc)
	}
	if !strings.Contains(doc, "c:chart") {
		t.Errorf("图表不该被删: %s", doc)
	}
	if !strings.Contains(doc, "***********") {
		t.Errorf("正文文字没脱敏: %s", doc)
	}
	hdr := files["word/header1.xml"]
	if strings.Contains(hdr, "v:imagedata") || strings.Contains(hdr, "w:pict") {
		t.Errorf("页眉图片还在: %s", hdr)
	}
	if !strings.Contains(hdr, "页眉") || !strings.Contains(hdr, "***********") {
		t.Errorf("页眉文字没保留/没脱敏: %s", hdr)
	}
	rels := files["word/_rels/document.xml.rels"]
	if strings.Contains(rels, "image1.png") {
		t.Errorf("图片关系没删: %s", rels)
	}
	if !strings.Contains(rels, "chart1.xml") {
		t.Errorf("图表关系不该删: %s", rels)
	}
	if !strings.Contains(files["word/styles.xml"], "w:styles") {
		t.Error("样式部件应原样保留")
	}
	if len(pd.Notes) == 0 || !strings.Contains(strings.Join(pd.Notes, "；"), "删除图片 2 张") {
		t.Errorf("提示里应说明删了几张图: %v", pd.Notes)
	}
}

func TestDocxStripImagesDryRun(t *testing.T) {
	path := docxWithImages(t)
	stats := NewStats()
	passes := []Pass{{Rules: []*rules.Rule{stripRule()}}}

	pd, err := ProcessDocxPasses(path, passes, stats, true)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Data != nil {
		t.Fatalf("空跑不该产出文件内容: %+v", pd)
	}
	if pd.Hits != 2 {
		t.Errorf("空跑应统计 2 张图, got %d", pd.Hits)
	}
	// 空跑不许动原文件
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// 只有图片、没有文字命中时也要写盘。
func TestDocxOnlyImagesStillWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "only.docx")
	writeZip(t, path, map[string]string{
		"word/document.xml":            `<?xml version="1.0"?><w:document xmlns:w="w" xmlns:a="a" xmlns:pic="pic" xmlns:r="r" xmlns:wp="wp"><w:body><w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><a:blip r:embed="rId1"/></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/logo.png"/></Relationships>`,
		"word/media/logo.png":          "png",
	})
	pd, err := ProcessDocxPasses(path, []Pass{{Rules: []*rules.Rule{stripRule()}}}, NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Data == nil || pd.Hits != 1 {
		t.Fatalf("只有图片也应产生改动: %+v", pd)
	}
	if _, ok := zipEntries(t, pd.Data)["word/media/logo.png"]; ok {
		t.Error("logo.png 还在")
	}
}

// 既没有图片也没文字命中：不碰文件。
func TestDocxStripImagesNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.docx")
	writeZip(t, path, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="w"><w:body><w:p><w:r><w:t>干干净净</w:t></w:r></w:p></w:body></w:document>`,
	})
	pd, err := ProcessDocxPasses(path, []Pass{{Rules: []*rules.Rule{stripRule()}}}, NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd != nil {
		t.Fatalf("没图没命中不该写盘: %+v", pd)
	}
}

// ────────────────────────────── xlsx ──────────────────────────────

// 造一个「单元格图片 + 页眉页脚图片」的最小 xlsx 包，专测 zip 收尾逻辑。
func syntheticXLSX(t *testing.T) []byte {
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
	add("xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="s" xmlns:r="r"><sheetData/><headerFooter><oddHeader>&amp;L&amp;G</oddHeader><oddFooter>&amp;C第 &amp;P 页</oddFooter></headerFooter><drawing r:id="rId1"/><legacyDrawing r:id="rId2"/></worksheet>`)
	add("xl/worksheets/_rels/sheet1.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing" Target="../drawings/drawing1.xml"/>`+
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/vmlDrawing" Target="../drawings/vmlDrawing1.vml"/>`+
		`</Relationships>`)
	add("xl/drawings/drawing1.xml", `<?xml version="1.0"?><xdr:wsDr xmlns:xdr="xdr" xmlns:a="a"><xdr:twoCellAnchor><xdr:pic><a:blip r:embed="rId1"/></xdr:pic></xdr:twoCellAnchor><xdr:twoCellAnchor><xdr:graphicFrame><a:graphic/></xdr:graphicFrame></xdr:twoCellAnchor></xdr:wsDr>`)
	add("xl/drawings/_rels/drawing1.xml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/image1.png"/></Relationships>`)
	add("xl/drawings/vmlDrawing1.vml", `<xml xmlns:v="v"><v:shapetype id="_x0000_t75"/><v:shape id="s1"><v:imagedata r:id="rId1"/></v:shape><v:shape id="s2"><v:textbox/></v:shape></xml>`)
	add("xl/drawings/_rels/vmlDrawing1.vml.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/image2.png"/></Relationships>`)
	add("xl/media/image1.png", "fake1")
	add("xl/media/image2.png", "fake2")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStripXLSXMediaRemovesCellAndHeaderPictures(t *testing.T) {
	out, pictures, names, err := stripXLSXMedia(syntheticXLSX(t))
	if err != nil {
		t.Fatal(err)
	}
	if pictures != 2 {
		t.Errorf("应删 2 张图（1 单元格 + 1 页眉）, got %d", pictures)
	}
	if len(names) != 2 {
		t.Errorf("应收集 2 个媒体文件名, got %v", names)
	}
	files := zipEntries(t, out)
	for name := range files {
		if strings.HasPrefix(name, "xl/media/") {
			t.Errorf("媒体没删干净: %s", name)
		}
	}
	sheet := files["xl/worksheets/sheet1.xml"]
	if strings.Contains(sheet, "&amp;G") {
		t.Errorf("页眉的 &G 占位没清: %s", sheet)
	}
	if !strings.Contains(sheet, "&amp;C第") {
		t.Errorf("其它页眉页脚代码不该动: %s", sheet)
	}
	if !strings.Contains(sheet, "legacyDrawing") {
		t.Errorf("legacyDrawing 关系应保留（批注还在用）: %s", sheet)
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
	sheetRels := files["xl/worksheets/_rels/sheet1.xml.rels"]
	if !strings.Contains(sheetRels, "vmlDrawing1.vml") {
		t.Errorf("drawing 关系不该被误删: %s", sheetRels)
	}
	drawingRels := files["xl/drawings/_rels/drawing1.xml.rels"]
	if strings.Contains(drawingRels, "image1.png") {
		t.Errorf("图片关系没删: %s", drawingRels)
	}
}

func pngFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dot.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestXLSXStripImagesIntegration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "带图.xlsx")
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

	stats := NewStats()
	passes := []Pass{{Rules: []*rules.Rule{stripRule(), mobileRule(t)}}}
	pd, err := ProcessXLSXPasses(path, passes, stats, false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Data == nil {
		t.Fatal("应产生写盘内容")
	}
	if pd.Hits != 2 { // 1 张图 + 1 个手机号
		t.Errorf("Hits = %d, 期望 2", pd.Hits)
	}
	if stats.RuleHits["noimg"] != 1 {
		t.Errorf("删除图片命中 = %d, 期望 1", stats.RuleHits["noimg"])
	}

	// 写回去再打开：图片没了、文字脱敏了
	out := filepath.Join(dir, "out.xlsx")
	if err := os.WriteFile(out, pd.Data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatal(err)
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
	v, _ := got.GetCellValue("Sheet1", "A1")
	if strings.Contains(v, "13800138000") {
		t.Errorf("文字没脱敏: %q", v)
	}
}

// 只有图片的 xlsx 也要写盘。
func TestXLSXOnlyImagesStillWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "only.xlsx")
	f := excelize.NewFile()
	if err := f.SetCellValue("Sheet1", "A1", "无敏感信息"); err != nil {
		t.Fatal(err)
	}
	if err := f.AddPicture("Sheet1", "B2", pngFixture(t), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()

	pd, err := ProcessXLSXPasses(path, []Pass{{Rules: []*rules.Rule{stripRule()}}}, NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd == nil || pd.Data == nil || pd.Hits != 1 {
		t.Fatalf("只有图片也应写盘: %+v", pd)
	}
}

// 没图没命中：原文件不碰。
func TestXLSXStripImagesNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.xlsx")
	f := excelize.NewFile()
	if err := f.SetCellValue("Sheet1", "A1", "干干净净"); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()

	pd, err := ProcessXLSXPasses(path, []Pass{{Rules: []*rules.Rule{stripRule()}}}, NewStats(), false)
	if err != nil {
		t.Fatal(err)
	}
	if pd != nil {
		t.Fatalf("没图没命中不该写盘: %+v", pd)
	}
}

// 文本文件遇到 strip_images 规则：没有文本命中，不该产生改动。
func TestTextFileIgnoresStripImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("联系人 13800138000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pd, err := ProcessTextPasses(path, []Pass{{Rules: []*rules.Rule{stripRule()}}}, NewStats())
	if err != nil {
		t.Fatal(err)
	}
	if pd != nil {
		t.Errorf("文本文件不该被 strip_images 改动: %+v", pd)
	}
}
