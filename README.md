# desensitize

[![tests](https://github.com/RexKang/desensitize-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/RexKang/desensitize-cli/actions/workflows/ci.yml)

测试数据脱敏命令行工具：把 `.txt` / `.md` / `.csv` / `.xlsx` / `.docx` 里的手机号、邮箱、身份证、IP、主机名等换成占位内容，**先出预演供人工确认，再备份并原地覆盖**，处理完可以直接在浏览器里逐条核对。

- 单个可执行文件，运行时零依赖（不用装运行时、不用联网）
- 预演 → 确认 → 备份 → 覆盖 四步走，不确认绝不落盘
- 六种规则类型（含「删除图片」），规则外置 `rules.json`，改规则不用重新编译
- 支持 Excel（保留公式、日期、数值类型）和 Word（保留样式、图片、run 格式）、UTF-8 / UTF-16 / GB18030
- 报告导出 HTML / CSV / JSON；生成 HTML 后**自动判断能否打开浏览器**，能开就直接跳过去

## 安装

```bash
# 有 Go 环境
go install github.com/RexKang/desensitize-cli/cmd/desensitize@latest

# 或从源码编译
git clone git@github.com:RexKang/desensitize-cli.git
cd desensitize-cli
go build -ldflags "-s -w -X main.version=v0.2.0" -o desensitize ./cmd/desensitize
```

需要 Go **1.26+** 编译（`excelize` 的最新版要求）；跑的时候只要那个可执行文件。

首次运行会自动从 `rules.sample.json` 复制一份 `rules.json` 放在可执行文件旁边（找不到就放当前目录），改规则不用改代码。

## 快速开始

```bash
# 1) 先看会改什么，一个字都不写盘
desensitize ./测试数据 --dry-run

# 2) 确认无误，真跑（会问一次 y/N）
desensitize ./测试数据

# 3) 跑完直接给一份可点的报告，处理完自动打开
desensitize ./测试数据 --report 明细.html
```

常用开关：

| 需求 | 命令 |
|---|---|
| 只看汇总，不展开明细 | `--dry-run --list-files 0` |
| 明细多展开几个文件 | `--dry-run --list-files 40` |
| 每文件每规则多列几条 | `--dry-run --samples 10` |
| 看每规则最高频命中值（找误伤） | `--dry-run --top 10` |
| 只看目标目录第一层 | `--no-recursive` |
| 导出完整明细 | `--report 明细.html` / `--report 明细.csv` |
| 生成临时报告并打开 | `--open` |
| 不要自动开浏览器 | `--no-open` |
| 打印当前生效规则 | `--dump-rules` |
| 看有哪些内置案例 | `--list-presets` |
| 直接用某个案例跑 | `--preset network` |
| 备份方式 | `--backup file` / `--backup zip` / `--backup none` |
| 免交互（脚本里用） | `--yes` |

退出码：`0` 成功 / `1` 没找到可处理文件、规则文件不存在 / `2` 参数错误 / `3` 未确认 / `4` 没有命中。

## 处理完自动打开 HTML

`--report x.html` 之后会走三步判断，任一步不通过就只打印路径，不会报错：

| 判断 | 结果 |
|---|---|
| 给了 `--no-open`，或环境变量 `DESENSITIZE_NO_BROWSER` 非空 | 不打开，提示已禁用 |
| stdout 不是终端（管道、CI、服务里跑） | 不打开，提示非交互环境 |
| 图形会话 | Windows 走 `rundll32`、Linux 走 `xdg-open`（无 `DISPLAY`/`WAYLAND_DISPLAY` 自动降级）、macOS 走 `open` |

想先看报告再决定跑不跑，就 `--dry-run --report x.html`：报告在写盘前生成，一样会自动打开。

## 输出长什么样

分四段，统计与数据分开：规则汇总 → 目录分布 → 明细 → 提示，最后等 `y` 确认。

```
════════════════════════════════════════════════════════════════════════
脱敏预演 · 未写入任何文件
范围  ./测试数据
扫描  5 个文件 → 5 个有命中；启用规则 7 条
════════════════════════════════════════════════════════════════════════

【一】规则汇总
规则      命中   文件  首例
IPv4      9      3     10.1.2.3  →  203.8.9.10
手机号    8      5     13800138000  →  ***********
工单号    7      3     TCK-  →  （空）
邮箱      4      4     deep@example.com  →  user@example.com
代号前缀  3      2     AP-  →  SITE-
主机名    2      1     host-web01  →  218c248501
身份证    1      1     110101199001011234  →  ******************
合计      34     5
   已是目标值 3 处；位移/哈希规则不可重跑：IPv4、主机名 再跑一次结果会变

【二】目录分布
目录                文件  命中
测试数据            4     32
测试数据\nested\l2  1     2

【三】明细（按命中数降序，显示 1/5 个文件；每文件每规则最多 2 条）

  测试数据\sample.xlsx    共 12 处
    工单号    工单!A2       TCK-  →  （空）
    手机号    工单!C2       13800138000  →  ***********
    IPv4      工单!E2       10.1.2.3  →  203.8.9.10
    … 另有 3 处未显示（--samples 调大，或 --report 导出全集）
    · 跳过公式单元格 2 个（不改公式）
    · 跳过日期单元格 2 个

【四】提示
  · 跳过 1 个 .xls（旧格式不支持，请先另存为 .xlsx）
════════════════════════════════════════════════════════════════════════
待写入 5 个文件 / 34 处替换。
确认覆盖这些文件？备份会留在原地 [y/N]
```

读法：

| 位置 | 含义 |
|---|---|
| `×3` | 同一位置、同一内容命中 3 次 |
| `L14` / `工单!C2` / `正文 段6` | 文本给行号；xlsx 给 `sheet!单元格`；docx 给 `部件 段序号` |
| `… 另有 N 处未显示` | 该文件还有命中没展开（`--samples` 控制每文件每规则条数） |
| `10.` 这类短原文 | 终端只显示匹配到的片段，完整上下文看 `--report` 的上下文列 |

## 图形界面（可选）

不想手改 JSON 的时候用。双击 exe（或 `desensitize gui`）就起来，浏览器接管后控制台窗口自动收起。

```bash
desensitize                      # 等价于 desensitize gui
desensitize gui --port 18080     # 指定端口（默认随机）
desensitize gui --keep-alive     # 页面关掉也不退出
```

界面按 **目标 → 规则 → 预演 → 执行** 四步组织，步骤条常驻顶部，每一步都带当前状态
（目标文件名 / 规则条数 / 命中数 / 已写文件数），点一下就能跳过去。

顶部是目标条：`选择目录…` / `选择文件…` 调系统原生对话框（Windows 就是资源管理器那个，
能输地址栏、能用网络位置），其它平台回退到「手选…」里的目录浏览器；递归、扫描上限、
备份方式、报告路径收在「任务设置 ▾」里。底部是常驻操作条：左边是本次任务摘要，
右边「看报告 / 空跑 / 执行…」——**没空跑过，或目标、规则、选项改过之后，「执行…」是禁用的**，
必须先确认命中面。

| 位置 | 干什么 |
|---|---|
| 左栏 · 规则库 | 挑/改规则，点「启 / 停」徽标切状态；模板收进中栏右上角「从模板开始 ▾」 |
| 中栏 · 编辑 | 改类型、参数；改完自动校验并给出「内置样本里会变成什么」；「单条试跑」让这一条规则单独跑一遍目标目录 |
| 右栏 · 本次任务 | 目标 / 规则套数 / 上次预演 / 备份方式 / 报告名，一眼看清这趟要做什么 |
| 右栏 · 待执行规则 | 「加入待执行」把当前规则库存成快照排队；卡片标「一致 / 规则库已改」，改过可以「同步规则库」；排第一的优先级最高 |
| ③ 预演 | 整页给结果：摘要（扫描 / 命中 / 命中文件数）+ 明细表（文件 / 位置、规则、原文 → 替换后、上下文带高亮），右上角「导出报告」「清空结果」 |

按钮约定：**空跑绿色**（只算不写）、**执行红色**（写盘，会再确认一次）、**清空结果蓝色**。

- **只监听 `127.0.0.1`**，地址里带一次性 token；本机别的程序、别的网页访问一律 401
- **页面关掉 12 秒后进程自己退出**（页面每 4 秒发一次心跳），不用手动 kill 服务
- 执行前弹确认：复述目标、**将改写几个文件（共扫描几个）**、**上次预演命中数与时间**、备份方式、报告路径、规则顺序，只列启用的规则（停用的灰掉标注）；最近一次空跑 0 命中时会额外提醒「执行不会改动任何文件」
- 执行时按队列顺序把各套规则作用在同一份内容的每个单元上，最后一个文件只写一次盘
- 规则库可以「保存」到当前文件，也可以**另存为**到任意位置（存完后续保存跟着走）；保存前留 `rules.json.<时间戳>.bak`，文件被别处改过会返回冲突，不盲覆盖
- 空跑**不写任何文件**，xlsx 连临时文件都不落

| 页面上的东西 | 对应命令行 |
|---|---|
| 空跑 | `--dry-run` |
| 执行 | 去掉 `--dry-run` 后确认（`--yes`） |
| 待执行规则 | 多组规则顺序套用（命令行一次只用一套） |
| 备份方式 | `--backup zip/file/none` |
| 报告路径 | `--report 明细.html` |
| 递归子目录 | 取消 `--no-recursive` |
| 模板 | `--preset <id>` |
| 保存 / 另存为规则库 | 写 `rules.json`，等价于手改文件 |

## 内置案例库

不想从零写正则时，直接挑一个案例跑：

```bash
desensitize --list-presets                # 看有哪些
desensitize ./测试数据 --preset ticket --dry-run
desensitize ./hosts --preset network --yes
```

| id | 场景 | 规则 | 风险 |
|---|---|---|---|
| `ticket` | 变更单 / 工单 | 手机、邮箱、工单号前缀删除、主机名哈希 | 低 |
| `excel-form` | Excel 需求调查表 | 手机、邮箱、身份证、内网 IP | 低 |
| `network` | 网络配置 / hosts | 私网 IP 位移、主机名哈希 | 中 |
| `logs` | 日志与告警 | 手机、邮箱、IP、trace id | 低 |
| `api-json` | 接口测试数据 | 手机、邮箱、身份证、银行卡（留后 4 位） | 中 |
| `contact` | 客户 / 供应商名单 | 手机、带区号固话、邮箱 | 低 |
| `config` | 配置与密钥 | password/token/api_key 的值、内网 IP | 中 |
| `hostname` | 主机名清单 | 主机名哈希、私网 IP 位移 | 中 |
| `audit` | 审计记录 | IP、主机名、手机 | 中 |
| `plate` | 车牌号 | 中国大陆车牌 | 低 |
| `serial` | 工号 / 编号 | 定长 8 位数字，**默认关闭** | 高 |
| `images` | 文档去图 | 只删 docx/xlsx 图片（含页眉页脚），不动文字 | 中 |

每个案例也随二进制内置；界面上点一下就载入成当前规则库，改完再保存成自己的 `rules.json`。

明确点名的无扩展名文件（`hosts`、`sshd_config` 这类）按文本处理；编码认不出来会跳过并提示。

## 规则

`rules.json` 里是一个数组，**顺序即优先级**。单趟扫描：先匹配到的规则吃掉这段文本，后面的规则不会再碰它。

```json
{
  "id": "mobile",
  "desc": "手机号",
  "type": "regex",
  "pattern": "1[3-9]\\d{9}",
  "digit_boundary": true,
  "replace": "{stars}",
  "enabled": true
}
```

字段在命令行、界面、内置案例之间共用同一份 schema。

| 字段 | 适用类型 | 说明 |
|---|---|---|
| `id` / `desc` | 全部 | 内部标识 / 人看的名字 |
| `type` | 全部 | `literal` / `regex` / `digits` / `ip_shift` / `hash` |
| `enabled` | 全部 | 省略视为启用 |
| `find` | literal | 字面串，程序自动转义 |
| `pattern` | regex、hash | 裸正则；RE2 语法，**不支持 lookbehind/lookahead** |
| `length` | digits | 数字位数 |
| `boundary` | literal | `none` / `digit` / `word` / `ip_prefix` |
| `digit_boundary` | regex | 前后不能是数字，不切开长数字串 |
| `ignore_case` | 全部 | 忽略大小写 |
| `skip_valid_date` | digits | 跳过合法 `YYYYMMDD` |
| `first_octet` / `offset` / `offsets` / `source_cidrs` | ip_shift | 见下方示例 |
| `keep_tail` / `salt` | hash | 保留尾部几位 / 盐 |

`replace` 模板：`{stars}` 等长星号、`{keep:N}` 留后 N 位、`{head:N}` 留前 N 位、`{len}` 原长度、`{gN}` 第 N 个捕获组、空串删除。

`strip_images` 不需要 `pattern` / `replace` / 边界字段，写了也不会用。

### 六种类型

**literal**：字面替换或删除，`boundary: "word"` 保证 `MAP-01` 里的 `AP-` 不被误伤。

```json
{"id":"tag","desc":"代号前缀","type":"literal","find":"AP-","boundary":"word","replace":"SITE-","enabled":true}
```

**regex**：注意 RE2 没有 lookbehind，`(?<=...)` 会直接编译失败；边界交给 `digit_boundary` / `boundary`。

```json
{"id":"email","desc":"邮箱","type":"regex","pattern":"[A-Za-z0-9._%+\\-]+@[A-Za-z0-9.\\-]+\\.[A-Za-z]{2,}","replace":"user@example.com","enabled":true}
```

**digits**：定长数字，开之前先用 `--top 10` 看误伤面。

```json
{"id":"num8","desc":"8位数字","type":"digits","length":8,"replace":"{stars}","skip_valid_date":true,"enabled":false}
```

**ip_shift**：按段位移，超 255 取模。`source_cidrs` 不加会把 `1.8.0.181` 这类版本号也当 IP。

```json
{"id":"ipv4","desc":"IPv4","type":"ip_shift","first_octet":203,"offset":7,
 "source_cidrs":["10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"],"enabled":true}
```

`10.1.2.3 → 203.8.9.10`、`192.168.1.10 → 203.175.8.17`。**不可重跑**。

**hash**：前段 SHA-256 等长变形，尾部 `keep_tail` 位保留。`host-web01 → 218c248501`（10 位进 10 位出），跨文件稳定。**不可重跑**。

```json
{"id":"hostname","desc":"主机名","type":"hash","pattern":"host-[a-z]{2,6}\\d{2,4}","keep_tail":2,"salt":"","enabled":false}
```

**strip_images**：结构型规则，不扫文本。处理 `.docx` / `.xlsx` 时把图片整张删掉——正文、页眉页脚、
脚注尾注批注、工作表单元格图片都算；图表、SmartArt、文本框、嵌入对象（OLE）保持原样。
媒体文件、关系项、页眉页脚里的 `&G` 占位一起清理，不留坏引用。

```json
{"id":"strip_images","desc":"删除图片","type":"strip_images","enabled":true}
```

只想删图、不改文字时，规则库里留这一条就够了；它同样计入命中与明细（位置写 `正文` / `页眉1` / `工作表图片`），
只有图片被删、没有文字命中的文件也会写盘。

删完是「图片字节从包里消失」，不只是把引用抹掉：`word/media`、`xl/media` 下没人再引用的文件一并删除。
关系目标写成绝对路径的 xlsx（openpyxl、WPS 的产物）也能处理，不会留下 `xl/media` 里的原始图片。
两处有意不动：WPS「嵌入单元格」图片（`xl/cellimages.xml` 这一支）和 docx 的文档缩略图
（`docProps/thumbnail.jpeg`）——它们不是正文里插进去的图，动它们容易把工作簿/文档写坏。

## 报告

`--report` 按扩展名决定格式，导出全集，不受终端 `--list-files` / `--samples` 限制，并且在写盘前生成。

| 扩展名 | 内容 |
|---|---|
| `.html` | 左目录右内容：左栏总览 + 目录列表（点击跳转、滚动高亮）；右栏逐条明细，带**上下文**列（被替换片段高亮）；文件名与章节标题是 `file:///` 链接，点开就是原文件 |
| `.csv` | UTF-8 带 BOM，Excel 直接打开 |
| 其它 | JSON |

⚠ 报告里含**脱敏前的原文**，只用于本地核对，别提交到仓库或发到公开渠道。`.bak` 备份同理。

## 备份

两种打备份的方式，按场景选：

| 方式 | 参数 | 产物 | 适合 |
|---|---|---|---|
| 按文件 | `--backup file`（默认） | 每个被改的文件旁边留 `a.txt.20261024-151816.bak` | 改几个文件，回滚单个 |
| 整体打包 | `--backup zip` | 上层目录生成 `数据.20261024-151816.bak.zip`，目录按「上层目录」为根打包 | 整目录交付、要能一次还原 |
| 不备份 | `--backup none` | 无 | 只在有版本控制或副本时用 |

```bash
# 整目录先打包，再原地脱敏
desensitize ./测试数据 --backup zip

# 输出
整体备份：D:\测试数据.20261024-151816.bak.zip（357 B）
已写入 5 个文件（34 处替换），原件在上层目录的 .bak.zip 里。
```

几条约定：

- **打包失败就整体中止**：任何一个目标打包不成功，一个文件都不写，退出码 1
- **备份不会被二次扫描**：备份名里一定带 `.bak`（`*.bak` 与 `*.bak.zip`），扫描时统一跳过；`a.txt.desensitize.tmp` 这类中间产物同样排除
- **包内不含旧备份**：打包时会跳过已经存在的 `.bak` / `.bak.zip`，不会越备越大
- **还原就是解压**：`unzip 数据.20261024-151816.bak.zip -d .`（在目标的上层目录执行）

## 安全设计

| 环节 | 做法 |
|---|---|
| 写盘前 | 必须先出预演，交互敲 `y` 才写；非交互且没给 `--yes` 直接拒绝（退出码 3） |
| 备份 | 默认同目录 `原文件名.<YYYYMMDD-HHMMSS>.bak`；`--backup zip` 改成上层目录的整包；打包失败即中止 |
| 原子写 | 文本/图片类先写同目录临时文件再 `os.Rename`；xlsx 先 `SaveAs` 到临时文件再改名；docx 在内存里重打包后一次写出 |
| 单文件失败 | 只跳过它并在提示里注明，不影响整批 |
| 编码 | UTF-8 / UTF-8-BOM / UTF-16-LE / UTF-16-BE / GB18030 原样写回，BOM 与换行符不改 |
| Excel | 保持原单元格类型：数字格写回数字，文本格写回文本（`0001` 不会被写成 `1`）；公式、日期、布尔不动 |
| 幂等 | 普通规则连跑两次结果一致；`ip_shift` / `hash` 例外，汇总里会提示 |

回滚：备份名带时间戳，看一眼目录再改回去。

```bash
mv "工单.xlsx.20260101-101500.bak" "工单.xlsx"
```

## 已知限制

- **旧版 `.xls` / `.doc` 不支持**，只在提示里列出并跳过（Word 里另存为 `.docx`）
- **xlsx 回写会丢图表、图片、数据透视表、数据校验**（`excelize` 限制），处理前会点名哪个文件有这些东西
- 不计算公式；公式引用的格被替换后，结果要等 Excel 打开才更新
- 合并单元格只有左上角有值，其余位置读出来是空
- **docx 只处理文字**：图片、图表、SmartArt 里的字是像素，改不到；修订记录里标记删除的文字不处理；页眉页脚/脚注/尾注/批注都会处理
- docx 里的 Excel 嵌入对象、宏（`vbaProject.bin`）原样保留，不会脱敏
- 数值格替换成非数字串（如 `{stars}`）会失去数值类型；替换成纯数字则保留
- `ip_shift` / `hash` 不可重跑
- 正则走 RE2：没有反向引用和 lookbehind，写惯 PCRE 的规则要改
- **删除图片只处理 `.docx` / `.xlsx`**：图表、SmartArt、文本框、嵌入对象不删；旧 `.doc` / `.xls` 依旧不支持
- **原生选路径只在 Windows 上**（shell 的 `IFileOpenDialog`）；其它平台自动退回「手选…」的目录浏览器，选文件也只支持单选

## 开发

```
desensitize-cli/
├── cmd/desensitize/          CLI 入口：参数、流程、终端输出、浏览器打开判断
├── internal/
│   ├── rules/                规则模型、匹配、模板、ip_shift、hash
│   ├── mask/                 引擎：多规则合并成一次改写
│   ├── files/                编码、文本/xlsx/docx 处理、目录发现、统计
│   ├── gui/                  本机界面：server.go（token/心跳）+ api.go + run.go + browse.go + pick.go + page.html
│   ├── filedialog/           系统原生「选目录 / 选文件」对话框（Windows IFileOpenDialog，其它平台回退）
│   ├── presets/              内置案例库（presets.json + 校验）
│   ├── report/               HTML / CSV / JSON 报告
│   ├── browser/              各平台打开本地文件（构建标签分文件）
│   └── fileurl/              file:/// URL 生成
├── scripts/ui-check.sh       界面自检：无头浏览器真开页面 + 断言 + 截图
├── rules.sample.json         样例规则，首跑自动复制成 rules.json
└── .github/workflows/ci.yml  gofmt / go vet / go test / 编译
```

```bash
go test ./...        # 7 个包：规则、引擎、编码、xlsx、docx、发现、CLI 退出码、GUI 接口、原生对话框
go vet ./...
gofmt -l .           # 应无输出
bash scripts/ui-check.sh   # 改过界面就跑这个（见下）
```

### 改完界面必须真开一次页面

接口返回正确不等于页面显示正确。改过 `page.html` 或任何 GUI 接口后，跑：

```bash
bash scripts/ui-check.sh            # 用 .ui-check/ 下的样例数据
bash scripts/ui-check.sh <目录>      # 或指定目录
```

它做的事：起一个临时实例（随机端口、`--no-open`）→ 用无头 Chrome/Edge 真的打开页面 →
断言接口、模板载入、单条试跑、队列徽标、空跑命中、结果行数、上下文高亮、
**步骤条**（4 步、初始停在②规则、空跑后停在③预演）、**执行门禁**（未预演时「执行…」禁用）、
**按钮配色**（空跑绿 / 执行红 / 清空结果蓝）和**布局**（中栏最宽、底部操作条在首屏内、无横向溢出）→
截图到 `.ui-check/ui.png`（运行报告）、`.ui-check/ui-config.png`（规则配置）与 `.ui-check/ui-confirm.png`（执行确认）。
任何一项不对就非零退出。

页面本身带一个自检模式，脚本就是靠它驱动真实代码路径（载入案例 → 空跑 → 渲染）：

```
http://127.0.0.1:<port>/?t=<token>&selftest=1&target=<目录>
```

跑完会在页面末尾塞一段 `#selftest-report` 的 JSON。手工排查时也可以直接用它。

## License

MIT，见 [LICENSE](LICENSE)。
