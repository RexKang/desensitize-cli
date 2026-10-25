package files

// 结构型规则「删除图片」的实现。
//
// 文本规则走 mask.Apply 逐字替换；图片不是文字，只能按包结构处理：
//   - .docx：图片放在 word/media/ 下，正文、页眉页脚、脚注尾注批注里用
//     <w:drawing>（DrawingML）或 <w:pict>（VML）引用；删掉这两个元素、
//     再删掉指向图片的关系项与媒体文件，图片才算真的没了
//   - .xlsx：单元格图片交给 excelize 的 DeletePicture（它顺带清关系与媒体）；
//     页眉页脚里的图片藏在 xl/drawings/vmlDrawing*.vml + 页眉页脚代码 &G 里，
//     只能打包后按 zip 结构收尾
//
// 只删图片：图表、SmartArt、文本框、嵌入对象（OLE）都不动。

import (
	"archive/zip"
	"bytes"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/RexKang/desensitize-cli/internal/rules"
)

// imageExts 是当成「图片」的扩展名。media 目录里也可能放音频视频，那些不碰。
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".jpe": true, ".jfif": true,
	".gif": true, ".bmp": true, ".tif": true, ".tiff": true, ".ico": true,
	".emf": true, ".wmf": true, ".svg": true, ".webp": true, ".heic": true,
}

func isImagePart(name string) bool { return imageExts[strings.ToLower(path.Ext(name))] }

// mediaBase 取包内路径的文件名（zip 用 / 分隔，不能用 filepath）。
func mediaBase(name string) string { return path.Base(name) }

// activeStripRule 返回第一条启用的「删除图片」规则；没有就返回 nil。
func activeStripRule(passes []Pass) *rules.Rule {
	for _, p := range passes {
		for _, r := range p.Rules {
			if r.IsStripImages() && r.IsEnabled() {
				return r
			}
		}
	}
	return nil
}

// stageOfRule 找到规则属于哪一套策略：多条策略时用来标注命中来源。
func stageOfRule(passes []Pass, target *rules.Rule) string {
	for i, p := range passes {
		for _, r := range p.Rules {
			if r == target {
				return stageLabel(passes, i)
			}
		}
	}
	return ""
}

// ────────────────────────────── docx ──────────────────────────────

// stripDocxImages 删掉一个 XML 部件里的图片元素，返回新 XML 与被删张数。
// 图表（<c:chart>）、文本框、形状没有 <a:blip>/<pic:pic>，不会被误删。
func stripDocxImages(raw []byte) ([]byte, int) {
	out, n1 := removeBlocks(raw, "w:drawing", []string{"a:blip", "pic:pic"})
	out, n2 := removeBlocks(out, "w:pict", []string{"v:imagedata"})
	return out, n1 + n2
}

var (
	// 两种写法都要认：Excel 写 <Relationship .../>，excelize 回写后是
	// <Relationship ...></Relationship>，只认前者会漏掉它写出来的关系项。
	relElemRE   = regexp.MustCompile(`<Relationship\b[^>]*?(?:/>|>\s*</Relationship>)`)
	relTargetRE = regexp.MustCompile(`(?i)\bTarget="([^"]*)"`)
	relTypeRE   = regexp.MustCompile(`(?i)\bType="([^"]*)"`)
	relAbsRE    = regexp.MustCompile(`(?i)\bTarget="/`)
	relExtMode  = regexp.MustCompile(`(?i)\bTargetMode="External"`)
)

// dropImageRelationships 删掉 .rels 里指向图片的关系项，返回新内容与
// 这些关系指向的包内路径（已解析成 zip 里的名字）。
func dropImageRelationships(relsPath string, raw []byte) ([]byte, []string) {
	matches := relElemRE.FindAllIndex(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}
	var (
		out     bytes.Buffer
		targets []string
		last    int
	)
	for _, m := range matches {
		elem := raw[m[0]:m[1]]
		t := relTypeRE.FindSubmatch(elem)
		if t == nil || !strings.HasSuffix(strings.ToLower(string(t[1])), "/image") {
			continue
		}
		if tg := relTargetRE.FindSubmatch(elem); tg != nil {
			targets = append(targets, resolveRelTarget(relsPath, string(tg[1])))
		}
		out.Write(raw[last:m[0]])
		last = m[1]
	}
	if last == 0 {
		return raw, nil
	}
	out.Write(raw[last:])
	return out.Bytes(), targets
}

// resolveRelTarget 把关系里的相对 Target 解析成 zip 包内路径。
// 例：xl/drawings/_rels/vmlDrawing1.vml.rels + ../media/image1.png → xl/media/image1.png
func resolveRelTarget(relsPath, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	dir := path.Dir(path.Dir(relsPath)) // 去掉 "_rels/xxx.rels" 两层
	return path.Clean(path.Join(dir, target))
}

// ───────────────────────── 关系目标归一化（绝对路径 → 相对） ─────────────────────────

// normalizeAbsRelTargets 把包里所有 .rels 中以 "/" 开头的内部 Target 改写成相对路径。
// 返回新包与是否改动；没改动时原样返回，调用方继续走常规路径。
//
// 为什么必须归一化：openpyxl / WPS 会把目标写成 "/xl/drawings/drawing1.xml"，
// 而 excelize 的 DeletePicture 用 strings.ReplaceAll(target, "..", "xl") 定位部件，
// 遇到绝对写法就查不到包内部件、静默不删；保存时它还会按绝对路径另写一份
// "/xl/..." 条目，与真实部件重名，Excel 打开会提示「部分内容有问题」。
func normalizeAbsRelTargets(data []byte) ([]byte, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return data, false, err
	}
	changed := false
	replaced := map[string][]byte{}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".rels") {
			continue
		}
		raw, err := readEntry(f)
		if err != nil {
			return data, false, err
		}
		out, ok := rewriteAbsTargets(f.Name, raw)
		if !ok {
			continue
		}
		replaced[f.Name] = out
		changed = true
	}
	if !changed {
		return data, false, nil
	}
	out, err := rebuildZip(zr, replaced)
	if err != nil {
		return data, false, err
	}
	return out, true, nil
}

// rewriteAbsTargets 逐个关系项改写绝对 Target，外部链接（TargetMode="External"）不动。
func rewriteAbsTargets(relsPath string, raw []byte) ([]byte, bool) {
	matches := relElemRE.FindAllIndex(raw, -1)
	if len(matches) == 0 {
		return raw, false
	}
	var out bytes.Buffer
	last, changed := 0, false
	for _, m := range matches {
		elem := raw[m[0]:m[1]]
		if !relAbsRE.Match(elem) || relExtMode.Match(elem) {
			continue
		}
		tg := relTargetRE.FindSubmatchIndex(elem)
		if tg == nil {
			continue
		}
		rel := relToDir(path.Dir(path.Dir(relsPath)), string(elem[tg[2]:tg[3]]))
		if rel == "" {
			continue
		}
		out.Write(raw[last:m[0]])
		out.Write(elem[:tg[2]])
		out.WriteString(rel)
		out.Write(elem[tg[3]:])
		last = m[1]
		changed = true
	}
	if !changed {
		return raw, false
	}
	out.Write(raw[last:])
	return out.Bytes(), true
}

// relToDir 把 "/xl/media/a.png" 这样的包内绝对路径换算成相对 base 目录的写法。
// base 为 "." 表示包根。
func relToDir(base, target string) string {
	target = strings.TrimPrefix(target, "/")
	if target == "" || strings.ContainsAny(target, "?#:") {
		return ""
	}
	b, t := splitZipPath(base), splitZipPath(target)
	i := 0
	for i < len(b) && i < len(t) && b[i] == t[i] {
		i++
	}
	parts := make([]string, 0, len(b)-i+len(t)-i)
	for j := i; j < len(b); j++ {
		parts = append(parts, "..")
	}
	parts = append(parts, t[i:]...)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/")
}

// splitZipPath 按 "/" 切段，忽略空段与 "."。
func splitZipPath(p string) []string {
	if p == "" || p == "." || p == "/" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

// referencedParts 收集包里所有 .rels 仍然指向的部件（zip 内名字，去掉前导 "/"）。
func referencedParts(zr *zip.Reader, replaced map[string][]byte) map[string]bool {
	ref := map[string]bool{}
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		if !strings.HasSuffix(strings.ToLower(name), ".rels") {
			continue
		}
		raw, ok := replaced[name]
		if !ok {
			var err error
			raw, err = readEntry(f)
			if err != nil {
				continue
			}
		}
		for _, m := range relTargetRE.FindAllSubmatch(raw, -1) {
			t := string(m[1])
			if t == "" || strings.Contains(t, ":") || strings.HasPrefix(t, "#") {
				continue // 外部链接、锚点不参与
			}
			ref[resolveRelTarget(name, t)] = true
			// Target 里的空格等字符会写成 %20，包内条目名是原文；两种都算引用
			if u, err := url.PathUnescape(t); err == nil && u != t {
				ref[resolveRelTarget(name, u)] = true
			}
		}
	}
	return ref
}

// dropUnreferencedImages 把 mediaPrefix 下再没有任何关系指向的图片标记为删除，
// 返回被标记的条目名。删完关系后，页眉页脚 logo 这类已经没人引用的字节
// 只有靠这一步才会真的从包里消失。
func dropUnreferencedImages(zr *zip.Reader, replaced map[string][]byte, drop map[string]bool, mediaPrefix string) []string {
	ref := referencedParts(zr, replaced)
	var out []string
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		if drop[name] || !strings.HasPrefix(name, mediaPrefix) || !isImagePart(name) {
			continue
		}
		if ref[name] {
			continue
		}
		drop[name] = true
		out = append(out, name)
	}
	return out
}

// ────────────────────────────── xlsx ──────────────────────────────

// hasXLSXHeaderFooterPictures 判断工作簿里有没有页眉页脚图片（VML 里的 v:imagedata）。
// 用它决定「只删图片」要不要把包重写一遍：没有可删的就不动文件。
func hasXLSXHeaderFooterPictures(path string) bool {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		if !strings.HasPrefix(lower, "xl/drawings/") || !strings.HasSuffix(lower, ".vml") {
			continue
		}
		raw, err := readEntry(f)
		if err != nil {
			continue
		}
		if findOpenTag(raw, 0, "v:imagedata") >= 0 {
			return true
		}
	}
	return false
}

// stripXLSXMedia 对 excelize 写出来的包做收尾：
// 删掉页眉页脚图片（VML 里的 v:shape/v:imagedata + &G 代码）、绘图锚点里的图片、
// 指向图片的关系项，最后把没人再引用的媒体文件一并丢掉。
func stripXLSXMedia(data []byte) ([]byte, int, []string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, 0, nil, err
	}
	replaced := map[string][]byte{}
	drop := map[string]bool{}
	var names []string
	pictures, mediaDropped := 0, 0

	for _, f := range zr.File {
		// excelize 遇到绝对 Target 会另写一份 "/xl/drawings/..." 条目，先按真实名字判断
		name := strings.TrimPrefix(f.Name, "/")
		lower := strings.ToLower(name)
		switch {
		case isXLImageRel(name):
			raw, err := readEntry(f)
			if err != nil {
				return nil, 0, nil, err
			}
			out, targets := dropImageRelationships(name, raw)
			if len(targets) == 0 {
				continue
			}
			replaced[name] = out
			for _, t := range targets {
				if !isImagePart(t) || drop[t] {
					continue
				}
				drop[t] = true
				mediaDropped++
				names = append(names, path.Base(t))
			}
		case strings.HasPrefix(lower, "xl/drawings/") && strings.HasSuffix(lower, ".vml"):
			raw, err := readEntry(f)
			if err != nil {
				return nil, 0, nil, err
			}
			out, n := removeBlocks(raw, "v:shape", []string{"v:imagedata"})
			if n > 0 {
				replaced[name] = out
				pictures += n
			}
		case strings.HasPrefix(lower, "xl/drawings/") && strings.HasSuffix(lower, ".xml"):
			raw, err := readEntry(f)
			if err != nil {
				return nil, 0, nil, err
			}
			out, n := stripDrawingPictures(raw)
			if n > 0 {
				replaced[name] = out
				pictures += n
			}
		case strings.HasPrefix(lower, "xl/worksheets/") && strings.HasSuffix(lower, ".xml"):
			raw, err := readEntry(f)
			if err != nil {
				return nil, 0, nil, err
			}
			if out, changed := stripHeaderFooterCodes(raw); changed {
				replaced[name] = out
			}
		}
	}
	// 关系删完后，没人引用的图片（页眉页脚 logo、历史遗留的孤立媒体）一并丢掉
	for _, n := range dropUnreferencedImages(zr, replaced, drop, "xl/media/") {
		mediaDropped++
		names = append(names, path.Base(n))
	}
	names = uniqStrings(names)
	// 图片张数以「删掉的图形」为准；只删到 media 没删到形状时用媒体数兜底
	if pictures == 0 && mediaDropped > 0 {
		pictures = mediaDropped
	}
	out, err := rebuildZipNormalized(zr, replaced, drop)
	if err != nil {
		return nil, 0, nil, err
	}
	return out, pictures, names, nil
}

// uniqStrings 去重并保留顺序（同一个 media 可能被多个关系指到）。
func uniqStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// rebuildZipNormalized 按原顺序重打包，顺带把条目名归一化：
// 去掉前导 "/"、同名条目只留第一份。excelize 用绝对 Target 时会写出
// "/xl/drawings/drawing1.xml"，与真实部件逻辑上重名，留着会让 Excel 报修复。
func rebuildZipNormalized(zr *zip.Reader, replaced map[string][]byte, drop map[string]bool) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		if drop[name] || seen[name] {
			continue
		}
		seen[name] = true
		hdr := f.FileHeader
		hdr.Name = name
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			return nil, err
		}
		if data, ok := replaced[name]; ok {
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

// isXLImageRel 只处理工作簿里承载图形的 rels：
// 单元格图片在 drawings/_rels，页眉页脚图片在 drawings/_rels/vmlDrawing*.rels，
// 表关系在 worksheets/_rels。richData（「放在单元格里」的图片）不碰，避免留下坏引用。
func isXLImageRel(name string) bool {
	return strings.HasSuffix(name, ".rels") &&
		(strings.HasPrefix(name, "xl/drawings/_rels/") || strings.HasPrefix(name, "xl/worksheets/_rels/"))
}

// stripDrawingPictures 删掉 xdr:pic 所在的锚点。
// 只删锚点里的 pic，图表（graphicFrame）所在的锚点保留。
func stripDrawingPictures(raw []byte) ([]byte, int) {
	total := 0
	for _, tag := range []string{"xdr:oneCellAnchor", "xdr:twoCellAnchor", "xdr:absoluteAnchor"} {
		var n int
		raw, n = removeBlocks(raw, tag, []string{"xdr:pic"})
		total += n
	}
	return raw, total
}

// stripHeaderFooterCodes 去掉页眉页脚里的 &G（图片占位码），
// 否则图片删了、打印时还留着一个空占位。
func stripHeaderFooterCodes(raw []byte) ([]byte, bool) {
	var out bytes.Buffer
	pos, changed := 0, false
	for {
		p := findOpenTag(raw, pos, "headerFooter")
		if p < 0 {
			break
		}
		gt := bytes.IndexByte(raw[p:], '>')
		if gt < 0 {
			break
		}
		openEnd := p + gt + 1
		if selfClosing(raw, openEnd) {
			pos = openEnd
			continue
		}
		rel := bytes.Index(raw[openEnd:], []byte("</headerFooter>"))
		if rel < 0 {
			break
		}
		end := openEnd + rel + len("</headerFooter>")
		block := raw[p:end]
		nb := bytes.ReplaceAll(block, []byte("&amp;G"), nil)
		nb = bytes.ReplaceAll(nb, []byte("&G"), nil)
		if !bytes.Equal(nb, block) {
			changed = true
		}
		out.Write(raw[pos:p])
		out.Write(nb)
		pos = end
	}
	if !changed {
		return raw, false
	}
	out.Write(raw[pos:])
	return out.Bytes(), true
}

// ────────────────────────────── 公共小工具 ──────────────────────────────

// removeBlocks 删掉 data 里所有「包含任一 markers 的 <tag>...</tag> 元素」，
// 不含 markers 的同类元素原样保留（图表、文本框、普通形状靠这个放过）。
func removeBlocks(data []byte, tag string, markers []string) ([]byte, int) {
	var out bytes.Buffer
	pos, removed := 0, 0
	for {
		p := findOpenTag(data, pos, tag)
		if p < 0 {
			break
		}
		gt := bytes.IndexByte(data[p:], '>')
		if gt < 0 {
			break
		}
		openEnd := p + gt + 1
		if selfClosing(data, openEnd) {
			pos = openEnd
			continue
		}
		closeIdx := bytes.Index(data[openEnd:], []byte("</"+tag+">"))
		if closeIdx < 0 {
			break
		}
		end := openEnd + closeIdx + len("</"+tag+">")
		block := data[p:end]
		hit := false
		for _, m := range markers {
			if findOpenTag(block, 0, m) >= 0 {
				hit = true
				break
			}
		}
		out.Write(data[pos:p])
		if !hit {
			out.Write(block)
		} else {
			removed++
		}
		pos = end
	}
	if removed == 0 {
		return data, 0
	}
	out.Write(data[pos:])
	return out.Bytes(), removed
}
