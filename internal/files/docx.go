package files

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RexKang/desensitize-cli/internal/mask"
	"github.com/RexKang/desensitize-cli/internal/rules"
)

// DocxExts 是能就地处理的 Word 扩展名。.doc 是二进制老格式，只提示跳过。
var DocxExts = map[string]bool{".docx": true}

// IsDocx 判断是否 Word。
func IsDocx(path string) bool { return DocxExts[strings.ToLower(filepath.Ext(path))] }

// docx 的文字散在若干个 XML 部件里，只碰这几类，其余（样式、图片、关系）原样搬。
func isDocxTextPart(name string) bool {
	if !strings.HasPrefix(name, "word/") || !strings.HasSuffix(name, ".xml") {
		return false
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, "word/"), ".xml")
	switch base {
	case "document", "footnotes", "endnotes", "comments":
		return true
	}
	return strings.HasPrefix(base, "header") || strings.HasPrefix(base, "footer")
}

// partLabel 给统计用的位置前缀。
func partLabel(name string) string {
	base := strings.TrimSuffix(strings.TrimPrefix(name, "word/"), ".xml")
	switch {
	case base == "document":
		return "正文"
	case base == "footnotes":
		return "脚注"
	case base == "endnotes":
		return "尾注"
	case base == "comments":
		return "批注"
	case strings.HasPrefix(base, "header"):
		return "页眉" + strings.TrimPrefix(base, "header")
	case strings.HasPrefix(base, "footer"):
		return "页脚" + strings.TrimPrefix(base, "footer")
	}
	return base
}

// docxTextPartOfRels 把 "word/_rels/header1.xml.rels" 映射回它描述的文字部件。
// 只认承载文字的部件：图表、嵌入对象（OLE）的 rels 不在 word/_rels/ 下，
// 它们指向的图片也不能删——删了会在 chart XML 里留下指向不存在关系的 r:embed。
func docxTextPartOfRels(relsPath string) (string, bool) {
	if !strings.HasPrefix(relsPath, "word/_rels/") || !strings.HasSuffix(relsPath, ".rels") {
		return "", false
	}
	part := "word/" + strings.TrimSuffix(strings.TrimPrefix(relsPath, "word/_rels/"), ".rels")
	if !isDocxTextPart(part) {
		return "", false
	}
	return part, true
}

// ProcessDocx 就地处理 .docx（单组规则）。
func ProcessDocx(path string, rs []*rules.Rule, stats *Stats, dry bool) (*Pending, error) {
	return ProcessDocxPasses(path, SinglePass(rs), stats, dry)
}

// ProcessDocxPasses 就地处理 .docx：只改 word/ 下承载文字的 XML 部件，
// 其余 zip 条目按原顺序、原压缩方式搬过去，样式不受影响。
// 有启用的 strip_images 规则时，正文 / 页眉页脚 / 脚注尾注批注里的图片一并删掉。
// dry 为真时只算不落盘（界面预演用）。
func ProcessDocxPasses(path string, passes []Pass, stats *Stats, dry bool) (*Pending, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	strip := activeStripRule(passes)
	stripStage := ""
	if strip != nil {
		stripStage = stageOfRule(passes, strip)
	}

	replaced := map[string][]byte{}
	drop := map[string]bool{}
	partMedia := map[string][]string{} // 每个部件引用到的图片文件名

	// 先收图片关系：只碰承载文字的部件，把关系项删掉并记下这些媒体
	if strip != nil {
		for _, f := range zr.File {
			part, ok := docxTextPartOfRels(f.Name)
			if !ok {
				continue
			}
			raw, err := readEntry(f)
			if err != nil {
				return nil, err
			}
			out, targets := dropImageRelationships(f.Name, raw)
			if len(targets) == 0 {
				continue
			}
			replaced[f.Name] = out
			for _, t := range targets {
				if !isImagePart(t) || drop[t] {
					continue
				}
				drop[t] = true
				partMedia[part] = append(partMedia[part], mediaBase(t))
			}
		}
	}

	var notes, imgParts []string
	total, images := 0, 0
	hasMedia, hasChart, hasTracked, hasMacro := false, false, false, false

	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "word/media/"):
			hasMedia = true
		case strings.HasPrefix(f.Name, "word/charts/"):
			hasChart = true
		case strings.HasSuffix(f.Name, "vbaProject.bin"):
			hasMacro = true
		}
		if !isDocxTextPart(f.Name) {
			continue
		}
		raw, err := readEntry(f)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte("<w:del ")) || bytes.Contains(raw, []byte("<w:ins ")) {
			hasTracked = true
		}
		out, hits := rewriteDocxPart(raw, f.Name, passes, stats, path)
		removed := 0
		if strip != nil {
			out, removed = stripDocxImages(out)
			if removed > 0 {
				stats.SetStage(stripStage)
				stats.AddMedia(path, strip.ID, strip.Label(), partLabel(f.Name), partMedia[f.Name], removed)
				images += removed
				imgParts = append(imgParts, partLabel(f.Name)+" "+itoa(removed))
			}
		}
		total += hits
		if hits > 0 || removed > 0 {
			replaced[f.Name] = out
		}
	}
	if strip != nil {
		// 关系删完后，word/media 里没人引用的图片（页眉 logo、历史孤立文件）一并丢掉
		dropUnreferencedImages(&zr.Reader, replaced, drop, "word/media/")
	}
	if total == 0 && images == 0 {
		return nil, nil
	}

	if images > 0 {
		notes = append(notes, "删除图片 "+itoa(images)+" 张（"+strings.Join(imgParts, "、")+"）")
	}
	if hasMedia && strip == nil {
		notes = append(notes, "图片/图表里的文字是像素，不会被处理")
	}
	if hasChart {
		notes = append(notes, "图表里的文字仍是像素，不会被处理")
	}
	if hasTracked {
		notes = append(notes, "修订记录里被标记删除的文字不处理")
	}
	if hasMacro {
		notes = append(notes, "文档带宏（vbaProject.bin），原样保留")
	}

	if dry {
		return &Pending{Path: path, Hits: total + images, Notes: notes}, nil
	}
	data, err := rebuildZipWithDrop(&zr.Reader, replaced, func(n string) bool { return drop[n] })
	if err != nil {
		return nil, err
	}
	return &Pending{Path: path, Data: data, Hits: total + images, Notes: notes}, nil
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// rebuildZip 按原条目顺序重打包，只替换 map 里给出的那几个部件。
func rebuildZip(zr *zip.Reader, replaced map[string][]byte) ([]byte, error) {
	return rebuildZipWithDrop(zr, replaced, nil)
}

// rebuildZipWithDrop 在替换的基础上支持删除条目（drop 返回 true 的条目不再写入）。
// docx / xlsx 删图片时要同时删掉 media 文件，走这个。
func rebuildZipWithDrop(zr *zip.Reader, replaced map[string][]byte, drop func(string) bool) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if drop != nil && drop(f.Name) {
			continue
		}
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, err
		}
		if data, ok := replaced[f.Name]; ok {
			if _, err := w.Write(data); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			return nil, err
		}
		rc.Close()
	}
	if err := zw.SetComment(zr.Comment); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// textNode 是段落里的一个 <w:t>：记下 XML 字节区间，文本内容单独存。
type textNode struct {
	contentStart, contentEnd int
}

// rewriteDocxPart 逐段改写一个 XML 部件，返回新字节与实际改动数。
func rewriteDocxPart(raw []byte, part string, passes []Pass, stats *Stats, path string) ([]byte, int) {
	var out bytes.Buffer
	total, para, pos := 0, 0, 0
	for {
		p := findOpenTag(raw, pos, "w:p")
		if p < 0 {
			break
		}
		// <w:p/> 是 Word 里的空行，自带闭合，不能跟后面的段落拼成一个区域
		gt := bytes.IndexByte(raw[p:], '>')
		if gt < 0 {
			break
		}
		openEnd := p + gt + 1
		if selfClosing(raw, openEnd) {
			para++
			pos = openEnd
			continue
		}
		rel := bytes.Index(raw[p:], []byte("</w:p>"))
		if rel < 0 {
			break
		}
		pEnd := p + rel + len("</w:p>")
		para++
		newRegion, hits := rewriteParagraph(raw[p:pEnd], part, para, passes, stats, path)
		total += hits
		out.Write(raw[pos:p])
		out.Write(newRegion)
		pos = pEnd
	}
	out.Write(raw[pos:])
	return out.Bytes(), total
}

// rewriteParagraph 处理单个段落。
//
// Word 会把一句话切成多个 <w:t>（改过格式、拼写检查、修订都会切），命中可能横跨
// 两个 run。做法：把整段文字拼起来按策略顺序跑，再把结果按「字符原本属于哪个
// <w:t>」分回去——替换值落在命中起点所在的 run，命中之外的格式不会被抹掉。
func rewriteParagraph(region []byte, part string, para int, passes []Pass, stats *Stats, path string) ([]byte, int) {
	var nodes []textNode
	var texts []string
	search := 0
	for {
		o := findOpenTag(region, search, "w:t")
		if o < 0 {
			break
		}
		gt := bytes.IndexByte(region[o:], '>')
		if gt < 0 {
			break
		}
		openEnd := o + gt + 1
		if selfClosing(region, openEnd) { // <w:t/> 空节点
			search = openEnd
			continue
		}
		rel := bytes.Index(region[openEnd:], []byte("</w:t>"))
		if rel < 0 {
			break
		}
		contentStart := openEnd
		contentEnd := openEnd + rel
		nodes = append(nodes, textNode{contentStart: contentStart, contentEnd: contentEnd})
		texts = append(texts, xmlUnescape(string(region[contentStart:contentEnd])))
		search = contentEnd + len("</w:t>")
	}
	if len(nodes) == 0 {
		return region, 0
	}

	loc := partLabel(part) + " 段" + itoa(para)
	changed := 0
	for pi, ps := range passes {
		stats.SetStage(stageLabel(passes, pi))
		cur := strings.Join(texts, "")
		if cur == "" {
			continue
		}
		next, hits := mask.Apply(cur, ps.Rules)
		if len(hits) == 0 {
			continue
		}
		for _, h := range hits {
			if h.After != h.Original {
				changed++
			}
			stats.Add(path, h, cur, LocatorFunc(func(int) string { return loc }))
		}
		texts = distribute(cur, texts, next, hits)
	}
	if changed == 0 {
		return region, 0
	}

	var b bytes.Buffer
	last := 0
	for i, n := range nodes {
		b.Write(region[last:n.contentStart])
		if texts[i] != "" {
			b.WriteString(xmlEscape(texts[i]))
		}
		last = n.contentEnd
	}
	b.Write(region[last:])
	return b.Bytes(), changed
}

// distribute 把替换后的整段文字按字符归属分回各个 <w:t>：
// 命中片段整体落在命中起点所在的 run，命中之外的字符留在原来的 run。
func distribute(oldText string, texts []string, newText string, hits []mask.Hit) []string {
	starts := make([]int, len(texts))
	ends := make([]int, len(texts))
	acc := 0
	for i, t := range texts {
		starts[i] = acc
		acc += len(t)
		ends[i] = acc
	}
	nodeAt := func(off int) int {
		i := sort.Search(len(starts), func(i int) bool { return starts[i] > off }) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(texts) {
			i = len(texts) - 1
		}
		return i
	}
	outs := make([]strings.Builder, len(texts))
	copyRange := func(a, b int) {
		for i := nodeAt(a); i < len(texts) && a < b; i++ {
			from, to := a, b
			if from < starts[i] {
				from = starts[i]
			}
			if to > ends[i] {
				to = ends[i]
			}
			if from < to && from >= 0 && to <= len(oldText) {
				outs[i].WriteString(oldText[from:to])
			}
			if ends[i] >= b {
				break
			}
		}
	}
	pos := 0
	for _, h := range hits {
		copyRange(pos, h.Start)
		outs[nodeAt(h.Start)].WriteString(h.After)
		pos = h.End
	}
	copyRange(pos, len(oldText))

	out := make([]string, len(texts))
	for i := range outs {
		out[i] = outs[i].String()
	}
	return out
}

// selfClosing 判断 openEnd 前是不是自闭合的 “/>”，容忍多余空格。
func selfClosing(data []byte, openEnd int) bool {
	j := openEnd - 2
	for j > 0 && isSpaceByte(data[j]) {
		j--
	}
	return j >= 0 && data[j] == '/'
}

func isSpaceByte(b byte) bool {
	return b == 0x20 || b == 0x09 || b == 0x0D || b == 0x0A || b == 0x0B || b == 0x0C
}

// findOpenTag 找 <name 或 <name>，要求标签边界完整（避免 <w:p 命中 <w:pPr）。
func findOpenTag(data []byte, from int, name string) int {
	needle := []byte("<" + name)
	for i := from; i < len(data); {
		j := bytes.Index(data[i:], needle)
		if j < 0 {
			return -1
		}
		p := i + j
		after := p + len(needle)
		if after >= len(data) {
			return -1
		}
		switch data[after] {
		case '>', ' ', '\t', '\r', '\n', '/':
			return p
		}
		i = after
	}
	return -1
}

// xmlUnescape 解 XML 实体（&amp;、&#x4E2D; 之类）。
func xmlUnescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	d := xml.NewDecoder(strings.NewReader("<t>" + s + "</t>"))
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if _, done := tok.(xml.EndElement); done {
			break
		}
		if cd, ok := tok.(xml.CharData); ok {
			b.Write(cd)
		}
	}
	return b.String()
}

// xmlEscape 转义回去，保证 XML 仍然合法。
func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// itoa 避免为一个整数引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
