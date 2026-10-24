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

// ProcessDocx 就地处理 .docx：只改 word/ 下承载文字的 XML 部件，
// 其余 zip 条目按原顺序、原压缩方式搬过去，样式和图片不受影响。
// dry 为真时只算不落盘（界面预演用）。
func ProcessDocx(path string, rs []*rules.Rule, stats *Stats, dry bool) (*Pending, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	replaced := map[string][]byte{}
	var notes []string
	total := 0
	hasMedia, hasTracked, hasMacro := false, false, false

	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "word/media/"):
			hasMedia = true
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
		out, hits := rewriteDocxPart(raw, f.Name, rs, stats, path)
		if hits > 0 {
			replaced[f.Name] = out
			total += hits
		}
	}
	if total == 0 {
		return nil, nil
	}

	if hasMedia {
		notes = append(notes, "图片/图表里的文字是像素，不会被处理")
	}
	if hasTracked {
		notes = append(notes, "修订记录里被标记删除的文字不处理")
	}
	if hasMacro {
		notes = append(notes, "文档带宏（vbaProject.bin），原样保留")
	}

	if dry {
		return &Pending{Path: path, Hits: total, Notes: notes}, nil
	}
	data, err := rebuildZip(&zr.Reader, replaced)
	if err != nil {
		return nil, err
	}
	return &Pending{Path: path, Data: data, Hits: total, Notes: notes}, nil
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
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
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

// textNode 是段落里的一个 <w:t>，同时记下它在段落纯文本中的区间。
type textNode struct {
	contentStart, contentEnd int // 在 XML 字节里的内容区间
	start, end               int // 在拼接文本里的区间
}

// rewriteDocxPart 逐段改写一个 XML 部件，返回新字节与实际改动数。
func rewriteDocxPart(raw []byte, part string, rs []*rules.Rule, stats *Stats, path string) ([]byte, int) {
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
		newRegion, hits := rewriteParagraph(raw[p:pEnd], part, para, rs, stats, path)
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
// Word 会把一句话切成多个 <w:t>（改过格式、拼写检查、修订都会切），
// 命中可能横跨两个 run。做法：先把整段文字拼起来跑引擎，再把结果按
// 字符归属分回各个 <w:t>——替换值落在命中起点所在的那个 run，
// 这样命中之外的格式（加粗、颜色）不会被抹掉。
func rewriteParagraph(region []byte, part string, para int, rs []*rules.Rule, stats *Stats, path string) ([]byte, int) {
	var nodes []textNode
	var concat strings.Builder
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
		txt := xmlUnescape(string(region[contentStart:contentEnd]))
		start := concat.Len()
		concat.WriteString(txt)
		nodes = append(nodes, textNode{
			contentStart: contentStart,
			contentEnd:   contentEnd,
			start:        start,
			end:          start + len(txt),
		})
		search = contentEnd + len("</w:t>")
	}
	if len(nodes) == 0 {
		return region, 0
	}

	text := concat.String()
	_, hits := mask.Apply(text, rs)
	if len(hits) == 0 {
		return region, 0
	}
	changed := 0
	for _, h := range hits {
		if h.After != h.Original {
			changed++
		}
	}
	loc := partLabel(part) + " 段" + itoa(para)
	for _, h := range hits {
		stats.Add(path, h, text, LocatorFunc(func(int) string { return loc }))
	}
	if changed == 0 {
		return region, 0
	}

	// 把新文本按字符归属分回各个 <w:t>
	outs := make([]strings.Builder, len(nodes))
	nodeAt := func(off int) int {
		i := sort.Search(len(nodes), func(i int) bool { return nodes[i].start > off }) - 1
		if i < 0 {
			i = 0
		}
		return i
	}
	copyRange := func(a, b int) {
		for i := nodeAt(a); i < len(nodes) && a < b; i++ {
			from, to := a, b
			if from < nodes[i].start {
				from = nodes[i].start
			}
			if to > nodes[i].end {
				to = nodes[i].end
			}
			if from < to {
				outs[i].WriteString(text[from:to])
			}
		}
	}
	pos := 0
	for _, h := range hits {
		copyRange(pos, h.Start)
		outs[nodeAt(h.Start)].WriteString(h.After)
		pos = h.End
	}
	copyRange(pos, len(text))

	var b bytes.Buffer
	last := 0
	for i, n := range nodes {
		b.Write(region[last:n.contentStart])
		if outs[i].Len() > 0 {
			b.WriteString(xmlEscape(outs[i].String()))
		}
		last = n.contentEnd
	}
	b.Write(region[last:])
	return b.Bytes(), changed
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
