// Package files 负责文件的读取、编码识别、脱敏写回与目录发现。
package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RexKang/desensitize-cli/internal/mask"
	"github.com/RexKang/desensitize-cli/internal/rules"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// 支持的扩展名。
var TextExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".log": true,
	".ini": true, ".conf": true, ".yaml": true, ".yml": true, ".json": true,
}

var XLSXExts = map[string]bool{".xlsx": true, ".xlsm": true}

var skipNames = map[string]bool{
	"desensitize": true, "desensitize.exe": true, "README.md": true,
	"CHANGELOG.md": true, "LICENSE": true, "rules.json": true, "rules.sample.json": true,
}

var skipDirs = map[string]bool{
	"node_modules": true, "__pycache__": true, "venv": true, ".venv": true,
	"site-packages": true, "$RECYCLE.BIN": true, "System Volume Information": true,
	".git": true,
}

// Pending 是待写回的文件：内存里已经改好，等确认后再落盘。
type Pending struct {
	Path  string
	Data  []byte // 文本文件用；xlsx 用 SaveAs 走自己的写路径
	Hits  int
	Notes []string
	write func() error
}

// Write 备份后原子写入。
func (p *Pending) Write(backup bool) error {
	if p.write != nil {
		return p.write()
	}
	if backup {
		if err := Backup(p.Path); err != nil {
			return err
		}
	}
	return atomicWrite(p.Path, p.Data)
}

// Backup 生成 原文件名.<时间戳>.bak。
func Backup(path string) error {
	stamp := time.Now().Format("20060102-150405")
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+"."+stamp+".bak", src, 0o644)
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".desensitize.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// DetectEncoding 返回编码、BOM 与名称。
func DetectEncoding(raw []byte) (enc encoding.Encoding, bom []byte, name string, ok bool) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return unicode.UTF8, []byte{0xEF, 0xBB, 0xBF}, "utf-8", true
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}):
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), []byte{0xFF, 0xFE}, "utf-16-le", true
	case bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), []byte{0xFE, 0xFF}, "utf-16-be", true
	}
	if utf8.Valid(raw) {
		return nil, nil, "utf-8", true
	}
	// 落到 GB18030：能无损往返就认
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err == nil {
		if back, err2 := simplifiedchinese.GB18030.NewEncoder().Bytes(decoded); err2 == nil && bytes.Equal(back, raw) {
			return simplifiedchinese.GB18030, nil, "gb18030", true
		}
	}
	return nil, nil, "", false
}

func decode(raw []byte, enc encoding.Encoding, bom []byte) (string, error) {
	body := raw[len(bom):]
	if enc == nil {
		return string(body), nil
	}
	out, _, err := transform.Bytes(enc.NewDecoder(), body)
	return string(out), err
}

func encode(text string, enc encoding.Encoding, bom []byte) ([]byte, error) {
	body := []byte(text)
	if enc != nil {
		out, _, err := transform.Bytes(enc.NewEncoder(), body)
		if err != nil {
			return nil, err
		}
		body = out
	}
	out := make([]byte, 0, len(bom)+len(body))
	out = append(out, bom...)
	out = append(out, body...)
	return out, nil
}

// ProcessText 处理文本文件。
func ProcessText(path string, rs []*rules.Rule, stats *Stats) (*Pending, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	enc, bom, encName, ok := DetectEncoding(raw)
	if !ok {
		return nil, ErrUnknownEncoding
	}
	text, err := decode(raw, enc, bom)
	if err != nil {
		return nil, fmt.Errorf("解码失败（%s）: %w", encName, err)
	}

	newText, hits := mask.Apply(text, rs)
	if len(hits) == 0 {
		return nil, nil
	}
	effective := 0
	lineStarts := LineStarts(text)
	for _, h := range hits {
		if h.After != h.Original {
			effective++
		}
		stats.Add(path, h, text, LocatorFunc(func(off int) string {
			return "L" + strconv.Itoa(LineOf(lineStarts, off))
		}))
	}

	data, err := encode(newText, enc, bom)
	if err != nil {
		return nil, fmt.Errorf("按 %s 编码回去失败: %w", encName, err)
	}
	return &Pending{Path: path, Data: data, Hits: effective}, nil
}

// ErrUnknownEncoding 表示无法识别文件编码。
var ErrUnknownEncoding = errors.New("无法识别编码（UTF-8 / UTF-16 / GB18030 都不匹配）")

// LineStarts 预计算行首偏移，命中定位用二分。
func LineStarts(s string) []int {
	starts := []int{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// LineOf 返回 1 起的行号。
func LineOf(starts []int, off int) int {
	return sort.SearchInts(starts, off+1)
}

// ProcessXLSX 处理 xlsx/xlsm。
func ProcessXLSX(path string, rs []*rules.Rule, stats *Stats) (*Pending, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hits int
	notes := []string{}
	skippedFormula, skippedDate, skippedBool := 0, 0, 0

	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return nil, err
		}
		for r := range rows {
			for c := 0; c < len(rows[r]); c++ {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					continue
				}
				if formula, _ := f.GetCellFormula(sheet, cell); formula != "" {
					skippedFormula++
					continue
				}
				if isDateCell(f, sheet, cell) {
					skippedDate++
					continue
				}
				raw, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
				if err != nil || raw == "" {
					continue
				}
				// 写回时保持原单元格类型：数字格写回数字，文本格写回文本
				ct, _ := f.GetCellType(sheet, cell)
				wasNumeric := ct == excelize.CellTypeNumber
				if strings.EqualFold(raw, "TRUE") || strings.EqualFold(raw, "FALSE") {
					skippedBool++
					continue
				}
				newVal, cellHits := mask.Apply(raw, rs)
				if len(cellHits) == 0 {
					continue
				}
				loc := sheet + "!" + cell
				for _, h := range cellHits {
					stats.Add(path, h, raw, LocatorFunc(func(int) string { return loc }))
				}
				for _, h := range cellHits {
					if h.After != h.Original {
						hits++
					}
				}
				if wasNumeric && isAllDigits(newVal) && !hasLeadingZero(newVal) {
					if n, err := strconv.ParseInt(newVal, 10, 64); err == nil {
						_ = f.SetCellInt(sheet, cell, n)
						continue
					}
				}
				_ = f.SetCellStr(sheet, cell, newVal)
			}
		}
	}
	if hits == 0 {
		return nil, nil
	}
	if skippedFormula > 0 {
		notes = append(notes, fmt.Sprintf("跳过公式单元格 %d 个（不改公式）", skippedFormula))
	}
	if skippedDate > 0 {
		notes = append(notes, fmt.Sprintf("跳过日期单元格 %d 个", skippedDate))
	}
	for _, w := range xlsxRisks(path) {
		notes = append(notes, w)
	}

	ext := filepath.Ext(path)
	tmp := strings.TrimSuffix(path, ext) + ".desensitize.tmp" + ext
	if err := f.SaveAs(tmp); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return nil, err
	}
	_ = os.Remove(tmp)

	return &Pending{Path: path, Data: data, Hits: hits, Notes: notes}, nil
}

// hasLeadingZero 判断 "0001" 这类值：写成数字会丢掉前导零，必须按文本写。
func hasLeadingZero(s string) bool {
	return len(s) > 1 && s[0] == '0' && isAllDigits(s)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var dateFmts = map[int]bool{
	14: true, 15: true, 16: true, 17: true, 18: true, 19: true, 20: true, 21: true, 22: true,
	27: true, 28: true, 29: true, 30: true, 31: true, 32: true, 33: true, 34: true, 35: true, 36: true,
	45: true, 46: true, 47: true,
	50: true, 51: true, 52: true, 53: true, 54: true, 55: true, 56: true, 57: true, 58: true,
}

func isDateCell(f *excelize.File, sheet, cell string) bool {
	id, err := f.GetCellStyle(sheet, cell)
	if err != nil {
		return false
	}
	st, err := f.GetStyle(id)
	if err != nil || st == nil {
		return false
	}
	if dateFmts[st.NumFmt] {
		return true
	}
	if st.CustomNumFmt != nil {
		low := strings.ToLower(*st.CustomNumFmt)
		if strings.ContainsAny(low, "yd") && !strings.Contains(low, "0.00") {
			return true
		}
	}
	return false
}

// xlsxRisks 检查 xlsx 里 openpyxl/excelize 回写会丢的东西。
func xlsxRisks(path string) []string {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer zr.Close()

	var charts, drawings, pivots, validations int
	for _, f := range zr.File {
		switch {
		case strings.HasPrefix(f.Name, "xl/charts/"):
			charts++
		case strings.HasPrefix(f.Name, "xl/drawings/"):
			drawings++
		case strings.HasPrefix(f.Name, "xl/pivotCache/"), strings.HasPrefix(f.Name, "xl/pivotTables/"):
			pivots++
		case strings.HasPrefix(f.Name, "xl/worksheets/sheet"):
			rc, err := f.Open()
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(rc, 8<<20))
			rc.Close()
			if bytes.Contains(body, []byte("<dataValidation")) {
				validations++
			}
		}
	}
	var out []string
	if charts > 0 {
		out = append(out, fmt.Sprintf("该文件含 %d 个图表，回写后会丢失", charts))
	}
	if drawings > 0 {
		out = append(out, fmt.Sprintf("该文件含 %d 个绘图对象（图片/形状），回写后会丢失", drawings))
	}
	if pivots > 0 {
		out = append(out, "该文件含数据透视表，回写后会丢失")
	}
	if validations > 0 {
		out = append(out, "该文件含数据校验规则，回写后会丢失")
	}
	return out
}

// Discover 展开目标为待处理文件列表。
func Discover(targets []string, recursive bool) (out []string, problems []string, xlsSkipped []string) {
	seen := map[string]bool{}
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		out = append(out, p)
	}

	for _, t := range targets {
		info, err := os.Stat(t)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s 不存在或不可读", t))
			continue
		}
		if info.IsDir() {
			_ = filepath.WalkDir(t, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				name := d.Name()
				if d.IsDir() {
					if p != t && (skipDirs[name] || strings.HasPrefix(name, ".")) {
						return filepath.SkipDir
					}
					if !recursive && p != t {
						return filepath.SkipDir
					}
					return nil
				}
				ext := strings.ToLower(filepath.Ext(name))
				switch {
				case skipFile(name):
					return nil
				case TextExts[ext], XLSXExts[ext]:
					add(p)
				case ext == ".xls":
					xlsSkipped = append(xlsSkipped, p)
				}
				return nil
			})
			continue
		}
		if skipFile(info.Name()) {
			problems = append(problems, fmt.Sprintf("%s 是临时/规则文件，跳过", t))
			continue
		}
		ext := strings.ToLower(filepath.Ext(t))
		switch {
		case TextExts[ext], XLSXExts[ext]:
			add(t)
		case ext == ".xls":
			xlsSkipped = append(xlsSkipped, t)
		default:
			problems = append(problems, fmt.Sprintf("%s 类型不支持（%s）", t, ext))
		}
	}
	sort.Strings(out)
	return out, problems, xlsSkipped
}

func skipFile(name string) bool {
	if strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".~") || strings.Contains(name, ".bak") {
		return true
	}
	if skipNames[name] {
		return true
	}
	return strings.HasPrefix(name, "rules") && strings.HasSuffix(name, ".json")
}

// IsXLSX 判断是否 Excel。
func IsXLSX(path string) bool { return XLSXExts[strings.ToLower(filepath.Ext(path))] }
