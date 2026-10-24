# Python 实现（v0.1.x，已冻结）

主实现已换成 Go（见仓库根目录的 [README](../README.md)），这里是 v0.1.0 的单文件 Python 版，
仅作存档与行为对照：`rules.json` 格式、预演汇总、备份、编码处理都与 Go 版一致。

**新功能不再往这里加**。没有 Go 环境时可以用它救急：

```bash
python desensitize.py ./测试数据 --dry-run
python desensitize.py ./测试数据
```

依赖 Python 3.10+ 与 `pip install openpyxl`。自测：

```bash
python tests/make_demo.py
python desensitize.py tests/fixtures --yes --rules tests/rules.test.json
python tests/verify.py     # 45 条断言
```

---

以下是 v0.1.0 时的原始说明。

---

# desensitize

测试数据脱敏命令行工具：把 `.txt` / `.md` / `.csv` / `.xlsx` 里的手机号、邮箱、身份证、IP、主机名等换成占位内容，**先出预演供人工确认，再备份并原地覆盖**。

- 单文件脚本，只用标准库 + `openpyxl`，无服务、无网络
- 预演 → 确认 → 备份 → 覆盖 四步走，不确认绝不落盘
- 支持 Excel（保留公式、日期、数值类型）、GB18030、UTF-16
- 报告可导出 HTML（左目录右内容，逐条带上下文）和 CSV，适合逐条人工核对

## 运行要求

- Python **3.10+**（用到 `X | None` 类型标注）
- `pip install openpyxl`（只处理文本文件时可不装）

```bash
git clone git@github.com:RexKang/desensitize-cli.git && cd desensitize-cli
python desensitize.py --version
```

首次运行会自动从 `rules.sample.json` 复制一份 `rules.json`，改规则不用改代码。用 `--rules 别的文件` 指定时不会自动创建：文件不存在直接报错，免得打错文件名还静默跑样例规则。

## 快速开始

```bash
# 1) 先看会改什么，一个字都不写盘
python desensitize.py ./测试数据 --dry-run

# 2) 确认无误，真跑（会问一次 y/N）
python desensitize.py ./测试数据

# 3) 只处理单个文件
python desensitize.py ./测试数据/工单.xlsx
```

常用开关：

| 需求 | 命令 |
|---|---|
| 只看汇总，不展开明细 | `--dry-run --list-files 0` |
| 明细多展开几个文件 | `--dry-run --list-files 40` |
| 每文件每规则多列几条 | `--dry-run --samples 10` |
| 看每规则最高频命中值（找误伤） | `--dry-run --top 10` |
| 只看目标目录第一层 | `--no-recursive` |
| 导出完整明细 | `--report 明细.html` 或 `--report 明细.csv` |
| 打印当前生效规则 | `--dump-rules` |
| 免交互（脚本里用） | `--yes` |

退出码：`0` 成功 / `1` 没找到可处理文件、指定的规则文件不存在 / `2` 参数错误（argparse 约定）/ `3` 未确认 / `4` 没有命中。

## 输出长什么样

分四段，统计与数据分开：规则汇总 → 目录分布 → 明细 → 提示，最后等 `y` 确认。

```
════════════════════════════════════════════════════════════════════════
脱敏预演 · 未写入任何文件
范围  tests/fixtures
扫描  5 个文件 → 5 个有命中；启用规则 7 条
════════════════════════════════════════════════════════════════════════

【一】规则汇总
规则      命中   文件  首例
邮箱      4      4     staff@example.org  →  user@example.com
身份证    1      1     110101199001011234  →  ******************
手机号    8      5     13800138000  →  ***********
IPv4      9      3     10.1.2.3  →  203.8.9.10
代号前缀  3      2     AP-  →  SITE-
工单号    7      3     TCK-  →  （空）
主机名    2      1     host-web01  →  218c248501
合计      34     5
   已是目标值 3 处；位移/哈希规则不可重跑：IPv4、主机名 再跑一次结果会变

【二】目录分布
目录                   文件  命中
tests\fixtures         4     32
tests\fixtures\nested  1     2

【三】明细（按命中数降序，显示 2/5 个文件；每文件每规则最多 2 条）

  tests\fixtures\sample.xlsx    共 12 处
    工单号    工单!A2  TCK-  →  （空）
    手机号    工单!C2  13800138000  →  ***********
    IPv4      工单!E2  10.1.2.3  →  203.8.9.10
    代号前缀  工单!G2  AP-  →  SITE-
    … 另有 3 处未显示（--samples 调大，或 --report 导出全集）
    · 跳过公式单元格 2 个（不改公式）
════════════════════════════════════════════════════════════════════════
待写入 5 个文件 / 34 处替换。
确认覆盖这些文件？备份会留在原地 [y/N]
```

读法：

| 位置 | 含义 |
|---|---|
| `×3` | 同一位置、同一内容命中 3 次 |
| `L14` / `工单!C2` | 文本给行号；xlsx 给 `sheet!单元格`，列宽不够只压缩 sheet 名 |
| `… 另有 N 处未显示` | 该文件还有命中没展开（`--samples` 控制每文件每规则条数） |
| `10.` 这类短原文 | 终端只显示匹配到的片段，完整内容见 `--report` 的上下文列 |

## 规则

`rules.json` 里是一个数组，**顺序即优先级**。单趟扫描：先匹配到的规则吃掉这段文本，后面的规则不会再碰它。

### 字段

| 字段 | 适用类型 | 说明 |
|---|---|---|
| `id` | 全部 | 内部标识，`--dump-rules` 里显示 |
| `desc` | 全部 | 人看的名字，出现在汇总和明细里 |
| `type` | 全部 | `literal` / `regex` / `digits` / `ip_shift` / `hash` |
| `enabled` | 全部 | `false` 不参与匹配，但会在汇总里提示「停用 xxx」 |
| `find` | literal | 要匹配的字面串，程序自动转义，`AP-` 直接写 |
| `pattern` | regex、hash | 裸正则，反斜杠写两个（`\\d`） |
| `length` | digits | 数字位数 |
| `boundary` | literal | `none` 纯字面 / `digit` 两侧不能是数字 / `word` 左边不能是字母数字 / `ip_prefix` 限完整 IPv4 段首 |
| `digit_boundary` | regex | `true` 时外面套 `(?<!\d)…(?!\d)`，不切开长数字串 |
| `ignore_case` | 全部 | `true` 忽略大小写（默认区分） |
| `skip_valid_date` | digits | `true` 时合法的 `YYYYMMDD` 跳过 |
| `first_octet` / `offset` / `offsets` / `source_cidrs` | ip_shift | 见下方示例 |
| `keep_tail` / `salt` | hash | 保留尾部几位 / 盐 |

### replace 模板

原文 `13800138000`：

| 写法 | 结果 |
|---|---|
| `"00000000000"` | 固定串 |
| `"{stars}"` | `***********`（等长星号） |
| `"{keep:4}"` | `8000`（保留后 4 位） |
| `"{head:3}00000000"` | `13800000000`（保留前 3 位） |
| `"{len}"` | `11` |
| `"{g1}..."` | 正则第 1 个捕获组 |
| `""` | 删除 |

### 五种类型的例子

**literal：字面替换 / 删除**

```json
{"id": "tag", "desc": "代号前缀", "type": "literal",
 "find": "AP-", "boundary": "word", "replace": "SITE-", "enabled": true}
```

`boundary: "word"` 保证 `MAP-01` 里的 `AP-` 不被误伤。`replace` 写空串就是删除。

**regex：正则**

```json
{"id": "email", "desc": "邮箱", "type": "regex",
 "pattern": "[A-Za-z0-9._%+\\-]+@[A-Za-z0-9.\\-]+\\.[A-Za-z]{2,}",
 "replace": "user@example.com", "enabled": true}
```

**digits：定长数字**

```json
{"id": "num8", "desc": "8位数字", "type": "digits", "length": 8,
 "replace": "{stars}", "skip_valid_date": true, "enabled": false}
```

纯位数匹配会连编号、参数值一起打，开之前先 `--top 10` 看看最高频的命中值。

**ip_shift：按段位移**

```json
{"id": "ipv4", "desc": "IPv4", "type": "ip_shift", "first_octet": 203, "offset": 7,
 "source_cidrs": ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"], "enabled": true}
```

第一段固定成 203，其余段各 +7，超过 255 取模回绕：`10.1.2.3 → 203.8.9.10`、`192.168.1.10 → 203.175.8.17`。

- `source_cidrs` 限定只动这些网段；不加会把 `1.8.0.181` 这类版本号也当 IP
- 逐段不同偏移用 `offsets: [null, 1, 2, 3]`，`null` 表示该段不动
- **不可重跑**：第二次会再位移一次

**hash：定长哈希**

```json
{"id": "hostname", "desc": "主机名", "type": "hash",
 "pattern": "host-[a-z]{2,6}\\d{2,4}", "keep_tail": 2, "salt": "", "enabled": false}
```

前段做 SHA-256 取等长十六进制，尾部 `keep_tail` 位原样保留：`host-web01 → 218c248501`（10 位进、10 位出）。同一输入永远同一结果，跨文件一致；换 `salt` 可以让不同项目互不可比。**不可重跑**。

### 优先级

- 邮箱放前面，否则 `13800138000@example.com` 会先被手机号规则打掉
- 更长的数字规则排在短的前面
- 定长数字规则自带数字边界，`12345678901234567890` 不会被拆成几段

## 报告

`--report` 按扩展名决定格式，导出的是全集，不受终端 `--list-files` / `--samples` 限制，并且**在写盘前就生成**，所以可以先 `--dry-run --report x.html` 看报告再决定跑不跑。

| 扩展名 | 内容 |
|---|---|
| `.html` | 左目录右内容：左栏是总览 + 目录列表（点击跳转，滚动时高亮跟随）；右栏逐条明细，带**上下文**列（被替换片段高亮）。文件名与章节标题是 `file://` 链接，点开就是原文件 |
| `.csv` | UTF-8 带 BOM，Excel 直接打开；列：文件 / 规则 / 位置 / 原文 / 替换后 / 上下文 / 次数 |
| 其它 | JSON |

⚠ 报告里含**脱敏前的原文**，只用于本地核对，别提交到仓库或发到公开渠道。`.bak` 备份同理。

## 安全设计

| 环节 | 做法 |
|---|---|
| 写盘前 | 必须先出预演，交互敲 `y` 才写；非交互环境没给 `--yes` 直接拒绝（退出码 3） |
| 备份 | 覆盖前复制成 `原文件名.<YYYYMMDD-HHMMSS>.bak`，同目录 |
| 原子写 | 文本先写临时文件再 `os.replace`，中途失败不会留下半截文件 |
| 单文件失败 | 只跳过它并在提示里注明，不影响整批 |
| 编码 | 自动识别 UTF-8 / UTF-8-BOM / UTF-16 / GB18030，写回保留原编码与 BOM，换行符不改 |
| Excel 数值 | 替换成纯数字时仍是数值；日期格、布尔格、公式格不动 |
| 幂等 | 普通规则连跑两次结果一致；`ip_shift` / `hash` 例外，汇总里会提示 |

回滚：备份名带时间戳，看一眼目录再改回去。

```bash
move "工单.xlsx.20260101-101500.bak" "工单.xlsx"
```

## 已知限制

- **旧版 `.xls` 不支持**，只在提示里列出并跳过
- **xlsx 回写会丢图表、图片、数据透视表、数据校验**（openpyxl 限制）；处理前会在明细里点名哪个文件有这些东西
- openpyxl 不计算公式；公式引用的格被替换后，结果要等 Excel 打开才更新
- 合并单元格只有左上角有值，其余位置读出来是空
- 数值格如果替换成非数字串（如 `{stars}`）会失去数值类型；替换成纯数字则保留
- `ip_shift` / `hash` 不可重跑

## 自测

```bash
python tests/make_demo.py                                        # 造夹具（示例数据，非真实业务数据）
python desensitize.py tests/fixtures --yes --rules tests/rules.test.json
python tests/verify.py                                           # 45 条断言
```

覆盖：五种规则类型、GB18030 与 UTF-16 写回、xlsx 数值/日期/公式、同格多处替换、整格删空、三层嵌套递归、隐藏目录跳过、编码探测。

## 目录结构

```
desensitize/
├── desensitize.py        主程序（单文件）
├── rules.sample.json     样例规则，首次运行自动复制成 rules.json
├── tests/
│   ├── make_demo.py      生成夹具
│   ├── verify.py         断言检查
│   └── rules.test.json   自测专用固定规则
├── .gitignore
├── LICENSE
├── CHANGELOG.md
└── README.md
```

## License

MIT，见 [LICENSE](LICENSE)。
