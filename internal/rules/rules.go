// Package rules 定义脱敏规则模型：加载 JSON、编译匹配器、展开替换模板。
//
// 与 Python 版 rules.json 保持同一 schema，规则文件两边通用。
// Go 的 regexp 是 RE2，不支持 lookbehind/lookahead，因此 digit/word/ip_prefix
// 这三类边界改成「匹配后断言」——先拿到匹配区间，再看前后字符是否合规。
package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 规则类型。
const (
	TypeLiteral = "literal"  // 字面替换/删除
	TypeRegex   = "regex"    // 正则
	TypeDigits  = "digits"   // 定长数字
	TypeIPShift = "ip_shift" // IPv4 按段位移
	TypeHash    = "hash"     // 定长 SHA-256
)

// 字面规则的边界模式。
const (
	BoundaryNone     = "none"
	BoundaryDigit    = "digit"
	BoundaryWord     = "word"
	BoundaryIPPrefix = "ip_prefix"
)

// Rule 是一条脱敏规则。字段与 rules.json 一一对应。
type Rule struct {
	ID            string `json:"id"`
	Desc          string `json:"desc"`
	Type          string `json:"type"`
	Enabled       *bool  `json:"enabled"`
	Replace       string `json:"replace"`
	Note          string `json:"note"`
	Find          string `json:"find"`
	Pattern       string `json:"pattern"`
	Length        int    `json:"length"`
	Boundary      string `json:"boundary"`
	DigitBoundary bool   `json:"digit_boundary"`
	IgnoreCase    bool   `json:"ignore_case"`
	SkipValidDate bool   `json:"skip_valid_date"`

	// ip_shift
	FirstOctet  *int     `json:"first_octet"`
	Offset      *int     `json:"offset"`
	Offsets     []*int   `json:"offsets"`
	SourceCIDRs []string `json:"source_cidrs"`

	// hash
	KeepTail int    `json:"keep_tail"`
	Salt     string `json:"salt"`

	Index int `json:"-"` // 规则在文件中的次序，用于同起点时的优先级

	re    *regexp.Regexp
	cidrs []*net.IPNet
}

// IsEnabled 缺省视为启用。
func (r *Rule) IsEnabled() bool { return r.Enabled == nil || *r.Enabled }

// Label 终端与报告里显示的名字。
func (r *Rule) Label() string {
	if r.Desc != "" {
		return r.Desc
	}
	return r.ID
}

// Docs 是规则文件的元信息（_comment / version 等）。
type Docs map[string]any

// Load 读取规则文件。既接受顶层数组，也接受 {"rules": [...]} 形式。
func Load(path string) ([]*Rule, Docs, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读取规则文件失败: %w", err)
	}
	return Parse(raw)
}

// Parse 从内存里的 JSON 解析规则，供 embed 数据与测试使用。
func Parse(raw []byte) ([]*Rule, Docs, error) {
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))

	var arr []*Rule
	if err := json.Unmarshal(raw, &arr); err != nil {
		var obj struct {
			Rules []*Rule `json:"rules"`
		}
		if err2 := json.Unmarshal(raw, &obj); err2 != nil {
			return nil, nil, fmt.Errorf("规则文件格式错误（既不是数组也不是 {rules:[]}）: %w", err)
		}
		arr = obj.Rules
	}
	if len(arr) == 0 {
		return nil, nil, fmt.Errorf("规则里没有任何条目")
	}

	var meta Docs
	var holder map[string]any
	if err := json.Unmarshal(raw, &holder); err == nil {
		delete(holder, "rules")
		meta = holder
	}

	for i, r := range arr {
		r.Index = i
		if err := r.Compile(); err != nil {
			name := r.ID
			if name == "" {
				name = fmt.Sprintf("#%d", i+1)
			}
			return nil, nil, fmt.Errorf("规则 %s 有问题: %w", name, err)
		}
	}
	return arr, meta, nil
}

// Compile 编译规则的正则/网段等内部状态。Load 会自动调用；外部（如内置预设）自行调用。
func (r *Rule) Compile() error {
	switch r.Type {
	case TypeLiteral:
		if r.Find == "" {
			return fmt.Errorf("literal 规则缺少 find")
		}
		expr := regexp.QuoteMeta(r.Find)
		if r.IgnoreCase {
			expr = "(?i:" + expr + ")"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return err
		}
		r.re = re
		if r.Boundary == "" {
			r.Boundary = BoundaryNone
		}
		switch r.Boundary {
		case BoundaryNone, BoundaryDigit, BoundaryWord, BoundaryIPPrefix:
		default:
			return fmt.Errorf("未知 boundary: %s", r.Boundary)
		}
	case TypeRegex, TypeHash:
		if r.Pattern == "" {
			return fmt.Errorf("%s 规则缺少 pattern", r.Type)
		}
		expr := r.Pattern
		if r.IgnoreCase {
			expr = "(?i:" + expr + ")"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return fmt.Errorf("正则不合法: %w", err)
		}
		r.re = re
	case TypeDigits:
		if r.Length <= 0 {
			return fmt.Errorf("digits 规则缺少 length")
		}
		re, err := regexp.Compile(`\d{` + strconv.Itoa(r.Length) + `}`)
		if err != nil {
			return err
		}
		r.re = re
	case TypeIPShift:
		re, err := regexp.Compile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
		if err != nil {
			return err
		}
		r.re = re
		if r.FirstOctet == nil && r.Offset == nil && len(r.Offsets) == 0 {
			return fmt.Errorf("ip_shift 至少给 first_octet / offset / offsets 之一")
		}
		if r.FirstOctet != nil && (*r.FirstOctet < 0 || *r.FirstOctet > 255) {
			return fmt.Errorf("first_octet 超出 0-255")
		}
		for _, c := range r.SourceCIDRs {
			_, n, err := net.ParseCIDR(c)
			if err != nil {
				return fmt.Errorf("source_cidrs 里的 %q 不是合法网段: %w", c, err)
			}
			r.cidrs = append(r.cidrs, n)
		}
	default:
		return fmt.Errorf("未知 type: %q（可选 literal/regex/digits/ip_shift/hash）", r.Type)
	}
	return nil
}

// Clone 浅拷贝规则切片（正则与网段指针共享，本身不可变）。
// 界面里编辑「规则库副本」或给案例临时开关时用它，避免改到共享对象。
func Clone(rs []*Rule) []*Rule {
	out := make([]*Rule, len(rs))
	for i, r := range rs {
		c := *r
		if r.Offsets != nil {
			c.Offsets = append([]*int(nil), r.Offsets...)
		}
		out[i] = &c
	}
	return out
}

// Match 是一次命中：字节区间 + 捕获组文本。
type Match struct {
	Start, End int
	Groups     []string
}

// FindAll 返回该规则在 text 里的全部命中（已按规则自身的边界过滤）。
func (r *Rule) FindAll(text string) []Match {
	if r.re == nil {
		return nil
	}
	idx := r.re.FindAllStringSubmatchIndex(text, -1)
	out := make([]Match, 0, len(idx))
	for _, m := range idx {
		s, e := m[0], m[1]
		if s < 0 || e <= s {
			continue
		}
		if !r.accept(text, s, e) {
			continue
		}
		var groups []string
		for g := 2; g+1 < len(m); g += 2 {
			if m[g] < 0 {
				groups = append(groups, "")
				continue
			}
			groups = append(groups, text[m[g]:m[g+1]])
		}
		out = append(out, Match{Start: s, End: e, Groups: groups})
	}
	return out
}

// accept 处理 RE2 不支持的边界断言。
func (r *Rule) accept(text string, s, e int) bool {
	prev, hasPrev := runeBefore(text, s)
	next, hasNext := runeAfter(text, e)

	switch r.Type {
	case TypeIPShift:
		// 前后都不能紧挨数字或点：1.2.3.4.5 里不该命中任何四段
		if hasPrev && (prev == '.' || unicode.IsDigit(prev)) {
			return false
		}
		if hasNext && (next == '.' || unicode.IsDigit(next)) {
			return false
		}
		ip := text[s:e]
		if !validIPv4(ip) {
			return false
		}
		return r.inSource(ip)

	case TypeDigits:
		if hasPrev && unicode.IsDigit(prev) {
			return false
		}
		if hasNext && unicode.IsDigit(next) {
			return false
		}
		if r.SkipValidDate && isYMD(text[s:e]) {
			return false
		}
		return true
	}

	if r.DigitBoundary {
		if hasPrev && unicode.IsDigit(prev) {
			return false
		}
		if hasNext && unicode.IsDigit(next) {
			return false
		}
	}

	switch r.Boundary {
	case BoundaryDigit:
		if hasPrev && unicode.IsDigit(prev) {
			return false
		}
		if hasNext && unicode.IsDigit(next) {
			return false
		}
	case BoundaryWord:
		// 左边不能是字母或数字：词中间的片段不算
		if hasPrev && (prev < 128 && (unicode.IsLetter(prev) || unicode.IsDigit(prev))) {
			return false
		}
	case BoundaryIPPrefix:
		// 左边不能是数字或点
		if hasPrev && (prev == '.' || unicode.IsDigit(prev)) {
			return false
		}
		// 右边必须再跟三段点分数字，且后面不再接数字/点
		rest := text[e:]
		if !hasThreeOctets(rest) {
			return false
		}
	}
	return true
}

func runeBefore(s string, i int) (rune, bool) {
	if i <= 0 {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r, true
}

func runeAfter(s string, i int) (rune, bool) {
	if i >= len(s) {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r, true
}

// hasThreeOctets 判断 rest 是否以 a.b.c 开头且其后不接数字/点。
func hasThreeOctets(rest string) bool {
	re := regexp.MustCompile(`^(\d{1,3})\.(\d{1,3})\.(\d{1,3})`)
	m := re.FindStringSubmatch(rest)
	if m == nil {
		return false
	}
	end := len(m[0])
	if end < len(rest) {
		ch := rest[end]
		if ch == '.' || (ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}

func validIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return false
		}
	}
	return true
}

func isYMD(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	y, _ := strconv.Atoi(s[:4])
	m, _ := strconv.Atoi(s[4:6])
	d, _ := strconv.Atoi(s[6:])
	return y >= 1900 && y <= 2200 && m >= 1 && m <= 12 && d >= 1 && d <= 31
}

// inSource 判断 IPv4 是否落在 source_cidrs 内；未配置则全部通过。
func (r *Rule) inSource(ip string) bool {
	if len(r.cidrs) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range r.cidrs {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// ShiftIP 按段位移 IPv4；超 255 取模回绕。
func (r *Rule) ShiftIP(ip string) string {
	parts := strings.Split(ip, ".")
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return ip
		}
		nums[i] = n
	}
	out := make([]string, 4)
	for i := 0; i < 4; i++ {
		shift := 0
		if i < len(r.Offsets) && r.Offsets[i] != nil {
			shift = *r.Offsets[i]
		} else if r.Offset != nil {
			shift = *r.Offset
		}
		if i == 0 && r.FirstOctet != nil {
			out[i] = strconv.Itoa(*r.FirstOctet)
			continue
		}
		out[i] = strconv.Itoa(((nums[i]+shift)%256 + 256) % 256)
	}
	return strings.Join(out, ".")
}

// HashToken 前段 SHA-256 等长变形，末尾 keep_tail 位原样保留。
// 与 Python 版一致：只对 head 做摘要（salt+head），尾部原样拼回，
// 摘要短于 head 时继续 sha256 摘要串接到够长。
func (r *Rule) HashToken(s string) string {
	runes := []rune(s)
	kt := r.KeepTail
	var head, tail string
	if kt <= 0 || kt >= len(runes) {
		head, tail = s, ""
	} else {
		head = string(runes[:len(runes)-kt])
		tail = string(runes[len(runes)-kt:])
	}
	if head == "" {
		return s
	}
	sum := sha256.Sum256([]byte(r.Salt + head))
	digest := hex.EncodeToString(sum[:])
	for len(digest) < len(head) {
		next := sha256.Sum256([]byte(digest))
		digest += hex.EncodeToString(next[:])
	}
	return digest[:len(head)] + tail
}

var tokenRe = regexp.MustCompile(`\{(?:stars|len|keep:\d+|head:\d+|g\d+)\}`)

// Expand 展开 replace 模板。空串表示删除。
func (r *Rule) Expand(original string, groups []string) string {
	if r.Replace == "" {
		return ""
	}
	n := utf8.RuneCountInString(original)
	runes := []rune(original)
	return tokenRe.ReplaceAllStringFunc(r.Replace, func(tok string) string {
		switch {
		case tok == "{stars}":
			return strings.Repeat("*", n)
		case tok == "{len}":
			return strconv.Itoa(n)
		case strings.HasPrefix(tok, "{keep:"):
			k := atoiOr(tok[6:len(tok)-1], 0)
			if k <= 0 {
				return ""
			}
			if k >= len(runes) {
				return original
			}
			return string(runes[len(runes)-k:])
		case strings.HasPrefix(tok, "{head:"):
			k := atoiOr(tok[6:len(tok)-1], 0)
			if k <= 0 {
				return ""
			}
			if k >= len(runes) {
				return original
			}
			return string(runes[:k])
		case strings.HasPrefix(tok, "{g"):
			i := atoiOr(tok[2:len(tok)-1], 0)
			if i >= 1 && i <= len(groups) {
				return groups[i-1]
			}
			return ""
		}
		return tok
	})
}

// Render 生成一条命中的替换结果：ip_shift/hash 走各自算法，其余走模板。
func (r *Rule) Render(original string, groups []string) string {
	switch r.Type {
	case TypeIPShift:
		return r.ShiftIP(original)
	case TypeHash:
		return r.HashToken(original)
	default:
		return r.Expand(original, groups)
	}
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
