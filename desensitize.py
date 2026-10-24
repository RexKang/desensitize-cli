#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""desensitize — 测试数据脱敏命令行工具

输入：.txt / .md / .csv / .xlsx / .xlsm（旧版 .xls 不支持）
流程：扫描 -> 预演汇总 -> y/n 确认 -> 备份 -> 原地覆盖

规则放在同目录 rules.json（首次运行会从 rules.sample.json 复制一份），
改规则不用改代码。
"""

from __future__ import annotations

import argparse
import bisect
import json
import os
import re
import shutil
import sys
import unicodedata
from collections import OrderedDict
from datetime import date, datetime, time
from pathlib import Path

VERSION = "0.1.0"
SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_RULES = SCRIPT_DIR / "rules.json"

TEXT_EXTS = {".txt", ".md", ".markdown", ".csv", ".log", ".ini", ".conf", ".yaml", ".yml", ".json"}
XLSX_EXTS = {".xlsx", ".xlsm"}
SKIP_NAMES = {"desensitize.py", "README.md", "LICENSE", "rules.json", "rules.sample.json"}
SKIP_DIRS = {"node_modules", "__pycache__", "venv", "site-packages", "$RECYCLE.BIN", "System Volume Information"}


# ────────────────────────────── 控制台输出 ──────────────────────────────

def _init_console() -> None:
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(encoding="utf-8", errors="replace")
        except Exception:
            pass


def _w(s: str) -> int:
    return sum(2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in s)


def pad(s: str, width: int) -> str:
    return s + " " * max(0, width - _w(s))


def clip(s: str, width: int) -> str:
    if _w(s) <= width:
        return s
    out = ""
    for ch in s:
        if _w(out + ch) > width - 1:
            break
        out += ch
    return out + "…"


def elide_middle(s: str, width: int) -> str:
    """超宽时从中间省略，保留头尾——路径和 sheet!单元格 的关键部分都在两端。"""
    if _w(s) <= width:
        return s
    if width <= 3:
        return "…"
    head_budget = (width - 1) // 2
    tail_budget = width - 1 - head_budget
    head, acc = "", 0
    for ch in s:
        w = 2 if unicodedata.east_asian_width(ch) in "WF" else 1
        if acc + w > head_budget:
            break
        head += ch
        acc += w
    tail, acc = "", 0
    for ch in reversed(s):
        w = 2 if unicodedata.east_asian_width(ch) in "WF" else 1
        if acc + w > tail_budget:
            break
        tail = ch + tail
        acc += w
    return head + "…" + tail


def show_after(after: str) -> str:
    """替换成空串时显示成（空），否则看不出改了什么。"""
    return "（空）" if after == "" else after


CTX_RADIUS = 40


def ctx_snippet(text: str, start: int, end: int) -> str:
    """匹配点前后各取一段，用 ⟦⟧ 标出被替换的原串。

    前缀类规则（10. / N-）只匹配到很短一段，光看原文看不出改的是哪个 IP/编号，
    上下文补上这个信息。
    """
    s = max(0, start - CTX_RADIUS)
    e = min(len(text), end + CTX_RADIUS)

    def clean(x: str) -> str:
        return re.sub(r"\s+", " ", x)

    return (
        ("…" if s > 0 else "")
        + clean(text[s:start])
        + "⟦" + clean(text[start:end]) + "⟧"
        + clean(text[end:e])
        + ("…" if e < len(text) else "")
    )


def render_loc(loc: str, width: int) -> str:
    """xlsx 的位置写成 `单元格 @sheet`：单元格引用永不截断，sheet 名才压缩。"""
    if "!" not in loc:
        return elide_middle(loc, width)
    sheet, cell = loc.rsplit("!", 1)
    if _w(loc) <= width:
        return loc
    sheet_w = max(4, width - _w(cell) - 2)
    return f"{cell} @{elide_middle(sheet, sheet_w)}"


def hr(width: int = 78, ch: str = "─") -> None:
    print(ch * width)


# ────────────────────────────── 规则 ──────────────────────────────

def is_valid_yyyymmdd(s: str) -> bool:
    if len(s) != 8 or not s.isdigit():
        return False
    y, m, d = int(s[:4]), int(s[4:6]), int(s[6:])
    if not (1900 <= y <= 2099) or not (1 <= m <= 12) or not (1 <= d <= 31):
        return False
    try:
        date(y, m, d)
        return True
    except ValueError:
        return False


class Rule:
    """一条替换规则。"""

    __slots__ = ("id", "desc", "type", "enabled", "replace", "skip_valid_date", "note", "pattern",
                 "first_octet", "offset", "offsets", "source_cidrs", "nets", "keep_tail", "salt")

    def __init__(self, idx: int, raw: dict):
        self.id = str(raw.get("id") or f"rule{idx}")
        self.desc = str(raw.get("desc") or self.id)
        self.enabled = bool(raw.get("enabled", True))
        self.replace = str(raw.get("replace", "8888"))
        self.skip_valid_date = bool(raw.get("skip_valid_date", False))
        self.note = raw.get("note")
        self.type = str(raw.get("type") or ("literal" if "find" in raw else "regex"))
        self.first_octet = raw.get("first_octet")
        self.offset = int(raw.get("offset") or 0)
        self.offsets = raw.get("offsets")
        self.keep_tail = int(raw.get("keep_tail") or 0)
        self.salt = str(raw.get("salt") or "")
        self.source_cidrs = raw.get("source_cidrs")
        self.nets = []
        if self.type == "ip_shift" and self.source_cidrs:
            import ipaddress

            for c in self.source_cidrs:
                try:
                    self.nets.append(ipaddress.ip_network(str(c), strict=False))
                except ValueError:
                    pass
        self.pattern = self._build(raw)

    def _build(self, raw: dict) -> str:
        t = self.type
        ic = bool(raw.get("ignore_case", False))
        if t == "digits":
            n = int(raw["length"])
            return r"(?<!\d)\d{%d}(?!\d)" % n
        if t == "literal":
            body = re.escape(str(raw["find"]))
            if ic:
                body = f"(?i:{body})"
            boundary = str(raw.get("boundary", "none"))
            if boundary == "ip_prefix":
                # 必须后面仍是完整 IPv4 形状（三段点分），否则 1.2.3-abc 这类编号会被误判
                return r"(?<![\d.])%s(?=\d{1,3}\.\d{1,3}\.\d{1,3}(?![\d.]))" % body
            if boundary == "digit":
                return r"(?<!\d)%s(?!\d)" % body
            if boundary == "word":
                # 左边不能是英文字母或数字：单词内部的片段不算；空格、括号、下划线、中文、行首都算分隔符
                return r"(?<![A-Za-z0-9])%s" % body
            return body
        if t == "ip_shift":
            # 只认完整四段、每段 1~3 位，前后不能紧挨数字或点（-188、/24 这类后缀保留）
            return r"(?<![\d.])\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?![\d.])"
        if t in ("regex", "hash"):
            pat = str(raw["pattern"])
            if ic:
                pat = f"(?i:{pat})"
            if raw.get("digit_boundary", False):
                return r"(?<!\d)(?:%s)(?!\d)" % pat
            return pat
        raise ValueError(f"未知规则类型: {t}（rule id={self.id}）")

    def in_source(self, text: str) -> bool:
        """没配 source_cidrs 就全放行；配了就只认落在这些网段里的地址。"""
        if not self.nets:
            return True
        import ipaddress

        try:
            addr = ipaddress.ip_address(text)
        except ValueError:
            return False
        return any(addr in n for n in self.nets)

    def shift_ip(self, text: str) -> str:
        """按段做固定算术位移：第一段可固定成指定值，其余段各加偏移（超过 255 取模回绕）。"""
        parts = text.split(".")
        if len(parts) != 4 or not all(x.isdigit() for x in parts):
            return text
        nums = [int(x) for x in parts]
        if any(n > 255 for n in nums):
            return text
        out = []
        for i, n in enumerate(nums):
            if i == 0 and self.first_octet is not None:
                out.append(int(self.first_octet) % 256)
                continue
            off = self.offsets[i] if self.offsets and i < len(self.offsets) else self.offset
            out.append(n if off is None else (n + int(off)) % 256)
        return ".".join(str(x) for x in out)

    def hash_token(self, text: str) -> str:
        """把前段做 SHA-256 取等长十六进制，尾部 keep_tail 位原样保留。

        同一个输入永远得到同一个结果，所以跨文件的主机名保持一致；加 salt 可以
        让不同项目的映射互不相通。
        """
        import hashlib

        kt = self.keep_tail
        if kt <= 0 or kt >= len(text):
            head, tail = text, ""
        else:
            head, tail = text[:-kt], text[-kt:]
        if not head:
            return text
        seed = (self.salt + head).encode("utf-8")
        digest = hashlib.sha256(seed).hexdigest()
        while len(digest) < len(head):
            digest += hashlib.sha256(digest.encode("utf-8")).hexdigest()
        return digest[: len(head)] + tail

    def expand(self, original: str, groups: tuple = ()) -> str:
        if self.type == "ip_shift":
            return self.shift_ip(original)
        if self.type == "hash":
            return self.hash_token(original)
        tpl = self.replace
        if groups:
            for i, g in enumerate(groups, 1):
                token = "{g%d}" % i
                if token in tpl:
                    tpl = tpl.replace(token, g or "")
        if "{head:" in tpl:
            tpl = re.sub(r"\{head:(\d+)\}", lambda m: original[: int(m.group(1))], tpl)
        if "{stars}" in tpl:
            tpl = tpl.replace("{stars}", "*" * len(original))
        tpl = re.sub(r"\{keep:(\d+)\}", lambda m: original[-int(m.group(1)):], tpl)
        return tpl.replace("{len}", str(len(original)))


def load_rules(path: Path, allow_copy: bool = False) -> tuple[list[Rule], dict]:
    if not path.exists():
        sample = path.with_name("rules.sample.json")
        # 只有默认的 rules.json 缺失时才自动复制；--rules 指定的路径不存在就直接报错，
        # 否则打错文件名会被静默当成样例规则跑
        if allow_copy and sample.exists():
            shutil.copyfile(sample, path)
            print(f"没找到 {path.name}，已从 {sample.name} 复制一份；改完规则重跑即可。")
        else:
            raise SystemExit(f"规则文件不存在: {path}")
    data = json.loads(path.read_text(encoding="utf-8-sig"))
    raw_rules = data.get("rules") if isinstance(data, dict) else data
    if not isinstance(raw_rules, list) or not raw_rules:
        raise SystemExit(f"规则文件里没有 rules 数组: {path}")
    rules = [Rule(i, r) for i, r in enumerate(raw_rules)]
    meta = {k: v for k, v in data.items() if k != "rules"} if isinstance(data, dict) else {}
    return rules, meta


# ────────────────────────────── 计数与样本 ──────────────────────────────

class Stats:
    """命中计数 + 去重明细。终端只显示前若干条，完整明细走 --report。"""

    DETAIL_CAP = 20000

    def __init__(self, sample_limit: int = 3):
        self.sample_limit = sample_limit
        self.hits: dict[str, int] = {}
        self.skips: dict[str, int] = {}
        self.noop: dict[str, int] = {}
        self.rule_files: dict[str, int] = {}
        self.detail: OrderedDict[tuple, int] = OrderedDict()  # (file, rule, loc, before, after, ctx) -> 次数
        self.detail_capped = False

    def bump(self, bucket: dict, key: str, n: int = 1) -> None:
        bucket[key] = bucket.get(key, 0) + n

    def add_detail(self, file: str, rule_id: str, loc: str, before: str, after: str, ctx: str = "") -> None:
        key = (file, rule_id, loc, before, after, ctx)
        if key in self.detail:
            self.detail[key] += 1
        elif len(self.detail) < self.DETAIL_CAP:
            self.detail[key] = 1
        else:
            self.detail_capped = True

    def samples_for(self, limit_per_file_rule: int) -> list[tuple]:
        """按 (文件, 规则) 限条，供终端预览。"""
        out, used = [], {}
        for (file, rule_id, loc, before, after, ctx), n in self.detail.items():
            k = (file, rule_id)
            if used.get(k, 0) >= limit_per_file_rule:
                continue
            used[k] = used.get(k, 0) + 1
            out.append((file, rule_id, loc, before, after, ctx, n))
        return out

    def first_sample_of(self, rule_id: str):
        for (file, rid, loc, before, after, ctx), n in self.detail.items():
            if rid == rule_id:
                return loc, before, after, n
        return None


class Masker:
    """把启用规则合成一个正则做单趟扫描，替换结果不会再被其它规则命中。"""

    def __init__(self, rules: list[Rule], stats: Stats):
        self.stats = stats
        self.rules = [r for r in rules if r.enabled]
        self.rx = None
        self.group_offsets: list[int] = []
        if self.rules:
            joined = "|".join(f"(?P<r{i}>{r.pattern})" for i, r in enumerate(self.rules))
            self.rx = re.compile(joined)
            # 合并正则后每个规则前面都多了一个外层命名组，记下每条规则自己的捕获组起点，
            # 否则 {g1} 会取到别的规则（或外层组）的内容
            pos = 0
            for r in self.rules:
                self.group_offsets.append(pos + 1)
                pos += 1 + re.compile(r.pattern).groups

    def apply(self, text: str, file_label: str, loc_fn=None) -> str:
        if not self.rx or not text:
            return text
        pieces: list[str] = []
        last = 0
        for m in self.rx.finditer(text):
            idx = int(m.lastgroup[1:])
            rule = self.rules[idx]
            original = m.group(0)
            if rule.skip_valid_date and is_valid_yyyymmdd(original):
                self.stats.bump(self.stats.skips, rule.id)
                continue
            if rule.type == "ip_shift" and not rule.in_source(original):
                continue
            self.stats.bump(self.stats.hits, rule.id)
            all_groups = m.groups()
            local = all_groups[self.group_offsets[idx]:] if all_groups else ()
            after = rule.expand(original, local)
            if after == original:
                # 已经是目标值（例如重跑第二次），不算命中，否则汇总会虚高
                self.stats.bump(self.stats.noop, rule.id)
                self.stats.hits[rule.id] -= 1
                continue
            loc = loc_fn(m.start()) if loc_fn else ""
            self.stats.add_detail(file_label, rule.id, loc, original, after,
                                  ctx_snippet(text, m.start(), m.end()))
            pieces.append(text[last:m.start()])
            pieces.append(after)
            last = m.end()
        if not pieces:
            return text
        pieces.append(text[last:])
        return "".join(pieces)


# ────────────────────────────── 文件读写 ──────────────────────────────

def detect_encoding(raw: bytes) -> tuple[str | None, bytes]:
    """返回 (编码, BOM 字节)。BOM 单独返回，写回时原样拼回去，不改变文件形态。"""
    if raw.startswith(b"\xef\xbb\xbf"):
        return "utf-8", b"\xef\xbb\xbf"
    if raw.startswith(b"\xff\xfe"):
        return "utf-16-le", b"\xff\xfe"
    if raw.startswith(b"\xfe\xff"):
        return "utf-16-be", b"\xfe\xff"
    for enc in ("utf-8", "gb18030"):
        try:
            raw.decode(enc)
            return enc, b""
        except UnicodeDecodeError:
            continue
    return None, b""


class Pending:
    """待写回的文件。确认前只驻留在内存里。"""

    def __init__(self, path: Path, writer, changes: int):
        self.path = path
        self.writer = writer
        self.changes = changes
        self.backup: Path | None = None

    def save(self) -> None:
        self.writer()


def atomic_write_bytes(path: Path, data: bytes) -> None:
    """先写临时文件再替换，避免中途失败留下半截文件。"""
    tmp = path.with_name(path.name + ".desensitize.tmp")
    try:
        tmp.write_bytes(data)
        os.replace(tmp, path)
    finally:
        if tmp.exists():
            try:
                tmp.unlink()
            except OSError:
                pass


def process_text(path: Path, masker: Masker, notes: list[str]) -> Pending | None:
    raw = path.read_bytes()
    enc, bom = detect_encoding(raw)
    if enc is None:
        notes.append("无法识别编码（UTF-8 / UTF-16 / GB18030 都不匹配），跳过")
        return None
    text = raw[len(bom):].decode(enc)

    # 行首偏移一次性建表，命中位置二分查找；否则每个命中都要从文件头数一遍换行
    starts = [0]
    pos = text.find("\n")
    while pos != -1:
        starts.append(pos + 1)
        pos = text.find("\n", pos + 1)

    def loc(offset: int) -> str:
        return f"L{bisect.bisect_right(starts, offset)}"

    new = masker.apply(text, str(path), loc)
    if new == text:
        return None

    def writer() -> None:
        atomic_write_bytes(path, bom + new.encode(enc))

    return Pending(path, writer, 1)


def _xlsx_raw_features(path: Path) -> set[str]:
    """不加载 workbook，直接从 zip 里探特性，用来提前告警将被丢弃的东西。"""
    feats: set[str] = set()
    try:
        import zipfile

        with zipfile.ZipFile(path) as z:
            names = z.namelist()
            for name in names:
                if not (name.startswith("xl/worksheets/sheet") and name.endswith(".xml")):
                    continue
                data = z.read(name)
                if b"<dataValidation" in data:
                    feats.add("数据校验（下拉/名单）")
                if b"<drawing" in data:
                    feats.add("图形对象")
            if any(n.startswith("xl/drawings/") for n in names):
                feats.add("图表或图片")
            if any("pivotCache" in n or "pivotTable" in n for n in names):
                feats.add("数据透视表")
    except Exception:
        pass
    return feats


def process_xlsx(path: Path, masker: Masker, notes: list[str]) -> Pending | None:
    import warnings

    import openpyxl

    keep_vba = path.suffix.lower() == ".xlsm"
    with warnings.catch_warnings():
        # openpyxl 会对不支持的扩展刷一堆 UserWarning，我们在 notes 里自己汇报
        warnings.simplefilter("ignore")
        wb = openpyxl.load_workbook(str(path), keep_vba=keep_vba)
    changed = 0
    skipped_formula = 0

    for ws in wb.worksheets:
        for row in ws.iter_rows():
            for cell in row:
                v = cell.value
                if v is None:
                    continue
                if cell.data_type == "f" or (isinstance(v, str) and v.startswith("=")):
                    skipped_formula += 1
                    continue
                if isinstance(v, (datetime, date, time, bool)):
                    continue
                label = str(path)
                loc_prefix = f"{ws.title}!{cell.coordinate}"
                if isinstance(v, (int, float)):
                    if isinstance(v, float) and not float(v).is_integer():
                        s = repr(v)
                    else:
                        s = str(int(v))
                    new = masker.apply(s, label, lambda _o, p=loc_prefix: p)
                    if new != s:
                        if new == "":
                            cell.value = None
                        else:
                            cell.value = int(new) if new.isdigit() else new
                            if isinstance(cell.value, int):
                                cell.number_format = "General"
                        changed += 1
                elif isinstance(v, str):
                    new = masker.apply(v, label, lambda _o, p=loc_prefix: p)
                    if new != v:
                        cell.value = None if new == "" else new
                        changed += 1

    if skipped_formula:
        notes.append(f"跳过公式单元格 {skipped_formula} 个（不改公式）")
    extras = _xlsx_raw_features(path)
    if extras:
        notes.append("回写会丢失：" + "、".join(sorted(extras)) + "（openpyxl 限制）")
    if changed == 0:
        return None

    def writer() -> None:
        wb.save(str(path))

    return Pending(path, writer, changed)


# ────────────────────────────── 文件发现 ──────────────────────────────

def _skip_file(name: str) -> bool:
    """Excel 锁文件/临时文件、备份、规则文件、脚本自身都不参与扫描。"""
    if name.startswith(("~$", ".~")) or ".bak" in name:
        return True
    if name in SKIP_NAMES:
        return True
    return name.startswith("rules") and name.endswith(".json")


def _iter_dir(root: Path, recursive: bool):
    """默认递归；剪掉版本库/依赖目录和隐藏目录，避免爬进 .git。"""
    if not recursive:
        for p in sorted(root.iterdir()):
            if p.is_file():
                yield p
        return
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS and not d.startswith("."))
        for name in sorted(filenames):
            yield Path(dirpath) / name


def discover(targets: list[str], recursive: bool) -> tuple[list[Path], list[str], list[Path]]:
    files: list[Path] = []
    problems: list[str] = []
    xls_skipped: list[Path] = []
    for t in targets:
        p = Path(t).expanduser()
        if p.is_dir():
            for f in _iter_dir(p, recursive):
                if _skip_file(f.name):
                    continue
                if f.suffix.lower() in TEXT_EXTS | XLSX_EXTS:
                    files.append(f)
                elif f.suffix.lower() == ".xls":
                    xls_skipped.append(f)
        elif p.is_file():
            if _skip_file(p.name):
                problems.append(f"{p} 是临时/规则文件，跳过")
                continue
            if p.suffix.lower() == ".xls":
                xls_skipped.append(p)
            elif p.suffix.lower() in TEXT_EXTS | XLSX_EXTS:
                files.append(p)
            else:
                problems.append(f"{p} 类型不支持（{p.suffix or '无扩展名'}）")
        else:
            problems.append(f"{p} 不存在")
    seen = set()
    uniq = []
    for f in files:
        k = str(f.resolve()).lower()
        if k not in seen:
            seen.add(k)
            uniq.append(f)
    return uniq, problems, xls_skipped


# ────────────────────────────── 汇总输出 ──────────────────────────────

def print_summary(rules: list[Rule], stats: Stats, per_file: list[dict], problems: list[str],
                  *, roots: list[str], total_files: int, list_files: int, show_all: bool,
                  top: int = 0, xls_skipped: list[Path] | None = None) -> None:
    active = [r for r in rules if r.enabled]
    disabled = [r for r in rules if not r.enabled]
    hit_files = [x for x in per_file if x["total"] > 0]
    total_hits = sum(x["total"] for x in hit_files)
    total_skip = sum(stats.skips.values())
    total_noop = sum(stats.noop.values())

    print()
    hr(72, "═")
    print("脱敏预演 · 未写入任何文件")
    scope = "  ".join(roots[:3]) + ("  等" if len(roots) > 3 else "")
    print("范围  " + elide_middle(scope, 88))
    print(f"扫描  {total_files} 个文件 → {len(hit_files)} 个有命中；启用规则 {len(active)} 条"
          + (f"，停用 {len(disabled)} 条" if disabled else ""))
    hr(72, "═")

    # ── 一、规则汇总（只有统计，不放数据行）
    print()
    print("【一】规则汇总")
    wd = min(max([_w(r.desc) for r in active] + [4]) + 2, 18)
    rdw = min(max([_w(r.desc) for r in active] + [4]) + 2, 16)
    print(pad("规则", wd) + pad("命中", 7) + pad("文件", 6) + "首例")
    for r in active:
        n = stats.hits.get(r.id, 0)
        s = stats.first_sample_of(r.id)
        if s:
            ex = f"{clip(s[1], 20)}  →  {clip(show_after(s[2]), 18)}" + (f"  ×{s[3]}" if s[3] > 1 else "")
        else:
            ex = "—"
        print(pad(clip(r.desc, wd - 2), wd) + pad(str(n) if n else "-", 7)
              + pad(str(stats.rule_files.get(r.id, 0)) if n else "-", 6) + ex)
    print(pad("合计", wd) + pad(str(total_hits), 7) + pad(str(len(hit_files)), 6))

    bits = []
    if total_skip:
        bits.append(f"跳过 {total_skip} 处（疑似日期）")
    if total_noop:
        bits.append(f"已是目标值 {total_noop} 处")
    heavy = [r.desc for r in active if r.type in ("ip_shift", "hash")]
    if heavy:
        bits.append("位移/哈希规则不可重跑：" + "、".join(heavy) + " 再跑一次结果会变")
    if disabled:
        bits.append("停用 " + "、".join(r.id for r in disabled))
    if bits:
        print("   " + "；".join(bits))

    if top > 0 and stats.detail:
        per_rule: dict[str, dict] = {}
        for (_file, rid, _loc, before, _after, _ctx), n in stats.detail.items():
            bucket = per_rule.setdefault(rid, {})
            bucket[before] = bucket.get(before, 0) + n
        print()
        print(f"【一附】命中值 Top（每规则前 {top} 个，用来判断有没有误伤）")
        for r in active:
            bucket = per_rule.get(r.id)
            if not bucket:
                continue
            items = sorted(bucket.items(), key=lambda kv: -kv[1])[:top]
            line = "   ".join(f"{clip(v, 20)} ×{k}" for v, k in items)
            print(pad(clip(r.desc, rdw - 2), rdw) + line)

    # ── 二、目录分布
    if hit_files:
        print()
        print("【二】目录分布")
        groups: OrderedDict[str, dict] = OrderedDict()
        for x in hit_files:
            parts = x["name"].split(os.sep)
            key = os.sep.join(parts[:3]) if len(parts) > 3 else (os.sep.join(parts[:-1]) or ".")
            g = groups.setdefault(key, {"files": 0, "hits": 0})
            g["files"] += 1
            g["hits"] += x["total"]
        rows = sorted(groups.items(), key=lambda kv: (-kv[1]["hits"], kv[0]))
        preview = rows if show_all else rows[:8]
        gw = min(max([_w(k) for k, _ in preview] + [8]) + 2, 50)
        print(pad("目录", gw) + pad("文件", 6) + "命中")
        for k, v in preview:
            print(pad(elide_middle(k, gw - 2), gw) + pad(str(v["files"]), 6) + str(v["hits"]))
        if len(rows) > len(preview):
            rest = rows[len(preview):]
            print(f"   … 其余 {len(rest)} 个目录：{sum(v['files'] for _, v in rest)} 个文件 / "
                  f"{sum(v['hits'] for _, v in rest)} 处命中")

    # ── 三、明细
    print()
    if not hit_files:
        print("没有命中任何规则，文件保持不变。")
        hr(72, "═")
        return
    ordered = sorted(hit_files, key=lambda x: (-x["total"], x["name"]))
    shown = ordered if show_all else ordered[:list_files]
    samples = stats.samples_for(max(1, stats.sample_limit))
    by_file: dict[str, list] = {}
    for file, rid, loc, before, after, ctx, n in samples:
        by_file.setdefault(file, []).append((rid, loc, before, after, ctx, n))

    locw = min(max([_w(s[2]) for s in samples] + [6]) + 2, 26)
    if list_files <= 0:
        print(f"【三】明细（已用 --list-files 0 关闭；{len(ordered)} 个文件共 {total_hits} 处命中）")
    else:
        print(f"【三】明细（按命中数降序，显示 {len(shown)}/{len(ordered)} 个文件；"
              f"每文件每规则最多 {stats.sample_limit} 条）")
    for x in shown:
        print()
        print("  " + elide_middle(x["name"], 84) + f"    共 {x['total']} 处")
        rows = by_file.get(x["path"], [])
        for rid, loc, before, after, ctx, n in rows:
            rule = next((r for r in rules if r.id == rid), None)
            tag = rule.desc if rule else rid
            cnt = f"  ×{n}" if n > 1 else ""
            print(f"    {pad(clip(tag, rdw - 2), rdw)}{pad(render_loc(loc, locw - 2), locw)}"
                  f"{clip(before, 26)}  →  {clip(show_after(after), 18)}{cnt}")
        rest = x["total"] - sum(n for *_, n in rows)
        if rest > 0:
            print(f"    … 另有 {rest} 处未显示（--samples 调大，或 --report 导出全集）")
        for note in x["notes"]:
            print("    · " + elide_middle(note, 88))
    if len(ordered) > len(shown):
        rest_hits = sum(x["total"] for x in ordered[len(shown):])
        print()
        print(f"  … 其余 {len(ordered) - len(shown)} 个文件未展开（{rest_hits} 处命中）")
        print("     看全部：加 --all   导出明细：--report 明细.csv（或 .html）")

    tips = list(problems)
    if stats.detail_capped:
        tips.append(f"明细超过 {Stats.DETAIL_CAP} 条，已截断；请用 --report 导出")
    if tips or xls_skipped:
        print()
        print("【四】提示")
        if xls_skipped:
            print(f"  · 跳过 {len(xls_skipped)} 个旧版 .xls（不支持该格式，未处理）")
            for p in xls_skipped[:6]:
                print("      " + str(p))
            if len(xls_skipped) > 6:
                print(f"      … 另 {len(xls_skipped) - 6} 个")
        for t in tips:
            print("  · " + t)
    hr(72, "═")


def confirm(prompt: str) -> bool:
    if not sys.stdin.isatty():
        return False
    try:
        ans = input(prompt).strip().lower()
    except (EOFError, KeyboardInterrupt):
        return False
    return ans in ("y", "yes", "是", "确认")


def _ctx_html(ctx: str, esc) -> str:
    if "⟦" not in ctx:
        return esc(ctx)
    pre, rest = ctx.split("⟦", 1)
    mid, post = rest.split("⟧", 1)
    return f"{esc(pre)}<mark>{esc(mid)}</mark>{esc(post)}"


def _render_rows(rows: list[tuple], esc) -> str:
    out = []
    for tag, loc, before, after, ctx, n in rows:
        out.append(
            "<tr>"
            f"<td class='rule'>{esc(tag)}</td>"
            f"<td class='loc'>{esc(render_loc(loc, 46))}</td>"
            f"<td class='val'>{esc(before)}</td>"
            f"<td class='val out'>{esc(show_after(after))}</td>"
            f"<td class='ctx'>{_ctx_html(ctx, esc)}</td>"
            f"<td class='num'>{n}</td>"
            "</tr>"
        )
    return "\n".join(out)


REPORT_CSS = """
*{box-sizing:border-box}
html,body{height:100%}
body{margin:0;font:13.5px/1.6 system-ui,"Microsoft YaHei",sans-serif;color:#1f2328;background:#fff;
     display:grid;grid-template-columns:322px minmax(0,1fr);overflow:hidden}
#nav{height:100vh;overflow-y:auto;border-right:1px solid #e3e8ee;background:#fbfcfd;padding:14px 12px 30px}
.tl{font-size:16px;font-weight:700;margin:0}
.tsub{color:#6e7781;font-size:11.5px;margin:2px 0 8px}
.tstat{font-size:12px;color:#3d4451;margin-bottom:10px}
.tstat b{color:#cf222e;font-size:13.5px;font-variant-numeric:tabular-nums}
table.mini{width:100%;border-collapse:collapse;font-size:11.8px;margin:0 0 12px}
table.mini th,table.mini td{padding:3px 4px;text-align:left;border-bottom:1px solid #eef1f4}
table.mini th{color:#57606a;font-weight:600;border-bottom:1px solid #dfe4ea}
table.mini td.n{text-align:right;font-variant-numeric:tabular-nums}
.mini-h{font-size:11px;color:#8b949e;margin:0 0 4px;letter-spacing:.04em}
#q{width:100%;padding:5px 8px;border:1px solid #d0d7de;border-radius:6px;font:inherit;font-size:12px}
#hint{display:block;font-size:11px;color:#8b949e;margin:4px 0 6px;min-height:14px}
.toc{border-top:1px solid #eef1f4;padding-top:6px}
.toc a{display:block;text-decoration:none;color:#1f2328;padding:4px 7px;border-radius:6px;
       border-left:2px solid transparent;margin-bottom:1px}
.toc a:hover{background:#eef2f6}
.toc a.on{background:#e7f0fb;border-left-color:#0969da}
.toc .nm{display:block;font-weight:600;font-size:12.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.toc .pa{display:block;color:#96a0ab;font-size:11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.toc .cn{color:#8b949e;font-weight:400;font-size:11px;margin-left:5px}
#main{height:100vh;overflow-y:auto;padding:16px 26px 70vh;position:relative;scroll-behavior:smooth}
section.sec{padding:2px 0 12px;border-bottom:1px solid #eef1f4}
section.sec h2{font-size:14.5px;margin:12px 0 2px;color:#0a3069;word-break:break-all;font-weight:600}
.dsum{color:#8b949e;font-size:11.5px;margin-bottom:6px}
.file{margin:0 0 12px}
.file h3{font-size:12.8px;margin:10px 0 5px;font-family:ui-monospace,Consolas,monospace;font-weight:600;color:#3d4451}
.file h3 span{color:#8b949e;font-weight:400;margin-left:8px;font-family:inherit}
table.d{border-collapse:collapse;width:100%}
table.d th,table.d td{border:1px solid #d8dee4;padding:4px 8px;text-align:left;vertical-align:top}
table.d th{background:#f6f8fa;font-size:11.5px;color:#57606a;font-weight:600}
table.d tr:nth-child(even) td{background:#fbfcfd}
.rule{white-space:nowrap;color:#6639ba}
.loc{font-family:ui-monospace,Consolas,monospace;font-size:11.5px;color:#0a3069;white-space:nowrap}
.val{font-family:ui-monospace,Consolas,monospace;font-size:11.5px;color:#cf222e;white-space:pre-wrap;word-break:break-all}
.val.out{color:#1a7f37}
.ctx{font-size:11.5px;color:#3d4451;white-space:pre-wrap;word-break:break-all;max-width:720px}
mark{background:#ffd8d3;color:#82071e;font-weight:600;padding:0 1px;border-radius:2px}
.num{text-align:right;white-space:nowrap;font-variant-numeric:tabular-nums}
a.open{color:inherit;text-decoration:none;border-bottom:1px dashed #c4ccd4}
a.open:hover{color:#0969da;border-bottom-color:#0969da}
a.open.dir{font-size:11.5px;font-weight:400;color:#6e7781;margin-left:8px}
.warn{color:#9a6700;background:#fff8c5;border:1px solid #eed888;padding:6px 10px;border-radius:6px;
      font-size:12px;margin:0 0 10px;max-width:760px}
@media(max-width:900px){body{grid-template-columns:1fr}#nav{display:none}}
"""

REPORT_JS = """
<script>
const nav=document.getElementById('nav');
const main=document.getElementById('main');
const links=new Map();
document.querySelectorAll('.toc a[data-sec]').forEach(a=>links.set(a.dataset.sec,a));
/* 中文目录按拼音重排，左右两侧同步移动 */
[...document.querySelectorAll('.toc a[data-sec]')]
  .map(a=>({a:a,s:document.getElementById(a.getAttribute('data-sec')),k:a.getAttribute('title')||''}))
  .sort((x,y)=>x.k.localeCompare(y.k,'zh-Hans-CN',{numeric:true}))
  .forEach(p=>{document.querySelector('.toc').appendChild(p.a); if(p.s)main.appendChild(p.s);});
const secs=[...document.querySelectorAll('section.sec')];
let active=null;
function setActive(id){
  if(active===id)return;
  const prev=links.get(active); if(prev)prev.classList.remove('on');
  active=id;
  const a=links.get(id);
  if(!a)return;
  a.classList.add('on');
  const r=a.getBoundingClientRect(), n=nav.getBoundingClientRect();
  if(r.top<n.top+6||r.bottom>n.bottom-6)a.scrollIntoView({block:'nearest'});
}
function visible(){return secs.filter(s=>s.style.display!=='none');}
function spy(){
  const vis=visible();
  if(!vis.length)return;
  const top=main.scrollTop+96;
  let pick=vis[0];
  for(const s of vis){ if(s.offsetTop<=top)pick=s; else break; }
  setActive(pick.id);
}
let raf=0;
main.addEventListener('scroll',()=>{ if(raf)return; raf=requestAnimationFrame(()=>{spy();raf=0;}); },{passive:true});
links.forEach((a,id)=>a.addEventListener('click',e=>{
  e.preventDefault();
  const t=document.getElementById(id);
  if(!t)return;
  main.scrollTo({top:t.offsetTop-6,behavior:'smooth'});
  setActive(id);
}));
const q=document.getElementById('q');
q.addEventListener('input',()=>{
  const v=q.value.trim().toLowerCase();
  secs.forEach(s=>{
    const hit=!v||s.dataset.s.indexOf(v)>=0;
    s.style.display=hit?'':'none';
    const a=links.get(s.id);
    if(a)a.parentElement.style.display=hit?'':'none';
  });
  document.getElementById('hint').textContent=v?(visible().length+' 个目录命中'):'';
  main.scrollTop=0; spy();
});
spy();
</script>
"""


def write_report(path: Path, per_file: list[dict], stats: Stats, rules: list[Rule], stamp: str,
                 scan: dict | None = None) -> None:
    """导出完整明细：.csv 平铺 / .html 左目录右内容 / 其它 JSON。终端只显示前几条，这里给全集。"""
    rel = {x["path"]: x["name"] for x in per_file}
    files = [x for x in per_file if x["total"] > 0]
    ordered = sorted(files, key=lambda x: x["name"])
    rows_by_name: dict[str, list] = {}
    flat: list[tuple] = []
    for (file, rid, loc, before, after, ctx), n in stats.detail.items():
        rule = next((r for r in rules if r.id == rid), None)
        name = rel.get(file, file)
        rec = (rule.desc if rule else rid, loc, before, after, ctx, n)
        rows_by_name.setdefault(name, []).append(rec)
        flat.append((name,) + rec)

    path.parent.mkdir(parents=True, exist_ok=True)
    suf = path.suffix.lower()

    if suf == ".csv":
        import csv

        with path.open("w", encoding="utf-8-sig", newline="") as fh:
            w = csv.writer(fh)
            w.writerow(["文件", "规则", "位置", "原文", "替换后", "上下文", "次数"])
            w.writerows(flat)
        return

    if suf in (".html", ".htm"):
        from html import escape
        esc = escape  # 内部函数把 esc 当可调用对象用

        # 按「文件所在目录」分组，目录按名字排序；目录内文件也按名字排序
        groups: dict[str, list[dict]] = {}
        for f in ordered:
            groups.setdefault(str(Path(f["name"]).parent), []).append(f)
        dirs = sorted(groups)

        abs_by_name = {x["name"]: x["path"] for x in ordered}
        total_hits = sum(x["total"] for x in files)
        sec_parts: list[str] = []
        toc_parts: list[str] = []
        for i, d in enumerate(dirs):
            here = sorted(groups[d], key=lambda x: x["name"])
            n_hits = sum(f["total"] for f in here)
            sec_id = f"sec{i}"
            hay = [d] + [f["name"] for f in here]
            body = []
            for f in here:
                rows = rows_by_name.get(f["name"], [])
                hay += [f"{t} {l} {b} {a}" for t, l, b, a, _c, _n in rows]
                abs_path = abs_by_name.get(f["name"], "")
                try:
                    furl = Path(abs_path).resolve().as_uri()
                except (ValueError, OSError):
                    furl = ""
                name_html = (
                    f'<a class="open" href="{furl}" target="_blank" title="{esc(abs_path)}">'
                    f'{esc(Path(f["name"]).name)}</a>' if furl else esc(Path(f["name"]).name)
                )
                body.append(
                    f'<div class="file"><h3>{name_html}'
                    f"<span>共 {f['total']} 处</span></h3>"
                    "<table class=d><thead><tr><th>规则</th><th>位置</th><th>原文</th><th>替换后</th>"
                    "<th>上下文（⟦⟧ 内是被替换的那一段）</th><th class='num'>次数</th></tr></thead>"
                    f"<tbody>{_render_rows(rows, esc)}</tbody></table></div>"
                )
            try:
                dir_uri = Path(abs_by_name.get(here[0]["name"], "")).resolve().parent.as_uri()
            except (ValueError, OSError):
                dir_uri = ""
            dir_link = (f'<a class="open dir" href="{dir_uri}" title="打开这个文件夹">打开文件夹</a>'
                        if dir_uri else "")
            sec_parts.append(
                f'<section class="sec" id="{sec_id}" data-s="{esc(" ".join(hay).lower())}">'
                f"<h2>{esc(d)} {dir_link}</h2>"
                f'<div class="dsum">{len(here)} 文件 · {n_hits} 处</div>'
                + "".join(body)
                + "</section>"
            )
            parent = str(Path(d).parent)
            parent = "" if parent == "." else parent
            toc_parts.append(
                f'<a href="#{sec_id}" data-sec="{sec_id}" title="{esc(d)}">'
                f'<span class="nm">{esc(Path(d).name)}'
                f'<span class="cn">{n_hits} 处 · {len(here)} 文件</span></span>'
                f'<span class="pa">{esc(parent)}</span></a>'
            )

        mini_rows = "".join(
            f"<tr><td>{escape(r.desc)}</td><td class='n'>{stats.hits.get(r.id, 0)}</td>"
            f"<td class='n'>{stats.rule_files.get(r.id, 0)}</td></tr>"
            for r in rules if r.enabled and stats.hits.get(r.id)
        )
        scanned = (scan or {}).get("files", 0)
        doc = (
            '<!doctype html><html lang="zh"><head><meta charset="utf-8">'
            f"<title>脱敏明细 {escape(stamp)}</title><style>{REPORT_CSS}</style></head><body>"
            '<nav id="nav">'
            '<div class="tl">脱敏明细</div>'
            f'<div class="tsub">{escape(stamp)}</div>'
            f'<div class="tstat"><b>{total_hits}</b> 处替换 · {len(files)} 文件 · {len(dirs)} 目录</div>'
            '<div class="mini-h">规则汇总</div>'
            "<table class='mini'><thead><tr><th>规则</th><th class='n'>命中</th><th class='n'>文件</th></tr></thead>"
            f"<tbody>{mini_rows}</tbody></table>"
            '<div class="mini-h">扫描</div>'
            f'<div class="tstat" style="margin:-4px 0 10px">{scanned} 个文件 → 有命中 {len(files)} 个</div>'
            '<input id="q" placeholder="筛选：目录 / 文件名 / 原文 / 规则">'
            '<span id="hint"></span>'
            f'<div class="toc">{"".join(toc_parts)}</div>'
            "</nav>"
            '<main id="main">'
            '<div class="warn">报告里含脱敏前的原始内容，仅供本地核对，不要提交仓库或发公开渠道</div>'
            f'{"".join(sec_parts)}'
            "</main>"
            f"{REPORT_JS}</body></html>"
        )
        path.write_text(doc, encoding="utf-8")
        return

    payload = {
        "version": VERSION,
        "timestamp": stamp,
        "scan": scan or {},
        "files": [{k: v for k, v in x.items() if k != "path"} for x in per_file],
        "hits": stats.hits,
        "skips": stats.skips,
        "detail": [
            {"file": f, "rule": r, "loc": loc, "before": b, "after": a, "context": c, "count": n}
            for f, r, loc, b, a, c, n in flat
        ],
    }
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")


# ────────────────────────────── 主流程 ──────────────────────────────

def main(argv: list[str] | None = None) -> int:
    _init_console()
    ap = argparse.ArgumentParser(
        description="测试数据脱敏：txt/md/csv/xlsx 原地替换，先预演后确认",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    ap.add_argument("targets", nargs="*", help="文件或目录")
    ap.add_argument("--rules", default=str(DEFAULT_RULES), help="规则文件（默认同目录 rules.json）")
    ap.add_argument("--no-recursive", dest="recursive", action="store_false", default=True,
                    help="只看目标目录第一层，不进子目录（默认递归全部层级）")
    ap.add_argument("-n", "--dry-run", action="store_true", help="只预演，不写入")
    ap.add_argument("-y", "--yes", action="store_true", help="跳过确认直接写入（非交互场景用）")
    ap.add_argument("--no-backup", action="store_true", help="不生成 .bak 备份（不建议）")
    ap.add_argument("--report", metavar="PATH", help="导出明细：.csv / .html 完整表；其它扩展名存 JSON")
    ap.add_argument("--samples", type=int, default=3, help="终端明细里每文件每规则最多显示几条，默认 3")
    ap.add_argument("--list-files", type=int, default=12, help="终端明细里最多展开几个文件，默认 12；0 只看汇总")
    ap.add_argument("--all", action="store_true", help="终端里展开全部文件与全部目录")
    ap.add_argument("--top", type=int, default=0, help="额外列出每规则最高频命中值前 N 个（判断误伤用），默认 0 关闭")
    ap.add_argument("--dump-rules", action="store_true", help="打印生效规则后退出")
    ap.add_argument("--version", action="version", version=f"desensitize {VERSION}")
    args = ap.parse_args(argv)

    rules_path = Path(args.rules)
    rules, _meta = load_rules(rules_path, allow_copy=args.rules == str(DEFAULT_RULES))
    if args.dump_rules:
        for r in rules:
            flag = "启用" if r.enabled else "停用"
            print(f"[{flag}] {r.id:<14} {r.desc:<14} {r.type:<7} {r.replace:<12} {r.pattern}")
        return 0

    if not args.targets:
        ap.error("至少给一个文件或目录")
    files, problems, xls_skipped = discover(args.targets, args.recursive)
    if not files:
        print("没有找到可处理的文件。")
        if xls_skipped:
            print(f"  跳过 {len(xls_skipped)} 个旧版 .xls（不支持该格式）")
        for p in problems:
            print("  " + p)
        return 1

    stats = Stats(sample_limit=max(1, args.samples))
    masker = Masker(rules, stats)
    pending: list[Pending] = []
    per_file: list[dict] = []

    for f in files:
        notes: list[str] = []
        before_hits = dict(stats.hits)
        try:
            if f.suffix.lower() in XLSX_EXTS:
                item = process_xlsx(f, masker, notes)
            else:
                item = process_text(f, masker, notes)
        except PermissionError:
            notes.append("文件被占用（Excel 可能正处于打开状态），跳过")
            item = None
        except Exception as exc:  # 单个文件失败不拖垮整批
            notes.append(f"处理失败：{type(exc).__name__}: {exc}")
            item = None

        delta = {k: v - before_hits.get(k, 0) for k, v in stats.hits.items() if v - before_hits.get(k, 0) > 0}
        total = sum(delta.values())
        try:
            name = str(f.relative_to(Path.cwd()))
        except ValueError:
            name = str(f)
        for k in delta:
            stats.rule_files[k] = stats.rule_files.get(k, 0) + 1
        per_file.append({"path": str(f), "name": name, "hits": delta, "total": total, "notes": notes})
        if item is not None:
            pending.append(item)

    print_summary(rules, stats, per_file, problems,
                  roots=list(args.targets), total_files=len(files),
                  list_files=max(0, args.list_files), show_all=args.all, top=max(0, args.top),
                  xls_skipped=xls_skipped)

    total_hits = sum(stats.hits.values())
    if pending:
        print(f"待写入 {len(pending)} 个文件 / {total_hits} 处替换。")
    if args.report:
        rp = Path(args.report).expanduser()
        write_report(rp, per_file, stats, rules, datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                     scan={"files": len(files), "hits": total_hits})
        print(f"明细已导出：{rp}（{len(stats.detail)} 行去重明细）")

    if not pending:
        return 4  # 2 留给 argparse 的参数错误

    if args.dry_run:
        print("--dry-run 已指定，未写入。")
        return 0

    if not args.yes:
        if not sys.stdin.isatty():
            print("当前环境非交互终端，未写入。确认后加 --yes 重跑。")
            return 3
        if not confirm("确认覆盖这些文件？备份会留在原地 [y/N] "):
            print("已取消，未写入任何文件。")
            return 3

    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    written = 0
    for item in pending:
        try:
            if not args.no_backup:
                bak = item.path.with_name(f"{item.path.name}.{stamp}.bak")
                shutil.copy2(item.path, bak)
                item.backup = bak
            item.save()
            written += 1
        except Exception as exc:
            print(f"写入失败 {item.path}: {type(exc).__name__}: {exc}")

    print()
    hr()
    print(f"完成：{written}/{len(pending)} 个文件已原地保存，{total_hits} 处替换。")
    if not args.no_backup:
        print(f"备份后缀：.{stamp}.bak（同目录，确认无误后自行删除）")
    hr()

    if args.report:
        print(f"（明细表已在上一步生成，含全部 {len(stats.detail)} 行去重明细）")

    return 0


if __name__ == "__main__":
    sys.exit(main())
