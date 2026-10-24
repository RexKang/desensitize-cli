# Changelog

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)，格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [0.5.2] - 2026-10-24

把界面上缺的三块补上：目录选择、设置入口、空规则库的引导。

### 修复

- **规则库为空时看着像「什么都没加载」**：现在检测到规则库为空会自动切到「案例」页签，并在规则库页签里给出空状态与「去看案例 →」按钮
- **没有设置入口**：右上角加「⚙ 设置」抽屉，可改递归、最多扫描文件数、备份方式（执行时生效）、报告路径
- **目标路径只有文本框**：加「浏览…」目录选择器，列出盘符与子目录，可逐层进入，选中后回填路径

### 新增

- `/api/ls?path=` 目录浏览接口（列根目录 / 子目录、给上级路径，跳过隐藏目录，最多 500 项）

## [0.5.1] - 2026-10-24

修「双击 exe 一闪而过」。

### 修复

- **无参数启动直接进界面**：以前不带参数会当命令行用，报「至少给一个文件或目录」后立刻退出，双击的效果就是窗口一闪而过。现在判断「无参数 + 交互终端或父进程是 explorer.exe」就直接起界面
- **浏览器接管后收起控制台**：窗口成功打开后调用 `FreeConsole`，黑窗口不再留在屏幕上让人以为程序没退
- **没有 rules.json 也能起**：界面按空规则启动，左边挑一个内置案例就有规则了；命令行缺规则文件仍然直接报错
- 双击场景下启动失败会停住等回车，不至于连错误提示都看不见

## [0.5.0] - 2026-10-24

加了本机图形界面（`desensitize gui`），只读：配置规则、空跑预演、保存 rules.json。

### 新增

- **`desensitize gui`**：在 127.0.0.1 起本机页面并自动打开浏览器
  - 随机端口 + 一次性 token（`X-Token` 头或 `?t=`），别的进程/网页访问返回 401
  - 页面每 4 秒心跳，关掉 12 秒后进程自动退出；`--keep-alive` 可关掉这个行为
  - `--port` 指定端口，`--rules` 指定规则文件
- 页面能力：规则库增删改、启用开关、内置案例一键载入、空跑预演（带上下文与命中数）、保存 rules.json
- 保存时自动留 `rules.json.<时间戳>.bak`；文件被外部改过返回 409 冲突，不覆盖
- `/api/state`、`/api/preview`、`/api/rules`、`/api/ping`、`/api/quit` 五个接口，全部要 token

### 变更

- `files.ProcessXLSX` 增加 `dry` 参数：预演时连临时文件都不落
- `browser` 包新增 `OpenURL`，打开 http 地址与本地文件走同一套平台分支

### 工程

- `internal/gui` 测试：token 校验、页面下发、状态、预演只读、非法规则、保存备份、mtime 冲突、心跳与退出、token 随机性

## [未发布]

### 变更

- 终端措辞统一成「空跑」：`--dry-run` 的抬头改为「空跑 · 只算不写，未碰任何文件」，结束语改为「空跑模式（--dry-run），未写入任何文件」，`-h` 里也写成「空跑：只算不写，不碰任何文件」
- `--backup` 帮助里的取值顺序调整为 `zip / file / none`，与界面一致（默认仍是 `file`）

## [0.4.0] - 2026-10-24

备份从「只能逐文件」升级成可选方式，并补上整体打包；同时修掉了几个界面设计里暴露出来的语义问题。

### 新增

- **`--backup file|zip|none`**
  - `file`（默认）：同目录 `原文件名.<时间戳>.bak`
  - `zip`：在上层目录打成 `名称.<时间戳>.bak.zip`，按上层目录为根，解压即还原
  - `none`：不备份，确认提示会明确写「没有备份，无法回滚」
- 整体打包失败即中止，**一个文件都不写**（退出码 1）
- 备份包与中间产物统一排除在扫描之外（名字里带 `.bak` 或 `.desensitize.tmp`），打包时也跳过旧备份

### 变更

- `--no-backup` 保留为 `--backup none` 的别名

- 补上 `-h/--help`：列出全部开关与三个示例，退出码 0（此前会把 help 当成参数错误）

### 工程

- `internal/files/backup.go`：`BackupZip` / `HumanSize`，`zip.Deflate` 打包，目录与单文件都支持
- 测试：备份包条目与内容、旧备份不进新包、单文件打包、临时文件排除、三种模式在 CLI 上的行为、备份包不会被当成待处理文件

## [0.3.0] - 2026-10-24

内置案例库：11 个真实场景，带示例数据，可直接跑也可作为规则起点。为下一版 GUI 的左侧「案例」页签做准备。

### 新增

- **`--list-presets`**：列出内置案例（id / 场景 / 规则数 / 风险等级 / 说明）
- **`--preset <id>`**：用某个案例的规则直接跑，不用写 `rules.json`
- **11 个案例**：变更单 / Excel 需求调查表 / 网络配置 / 日志告警 / 接口测试数据 / 客户名单 / 配置密钥 / 主机名清单 / 审计记录 / 车牌号 / 工号编号（默认关闭）
- 每个案例带一段**示例文本**与一段 `note`（写清误伤面与不可重跑等注意事项），案例随二进制内置（`go:embed`）
- **明确点名的无扩展名文件**（`hosts`、`sshd_config`）按文本处理，编码认不出来才跳过

### 工程

- `internal/rules` 拆出 `Parse`（从内存解析）与 `Compile`（编译单条规则），新增 `Clone`（拷贝规则切片，避免界面改到共享对象）
- `internal/presets` 测试：案例唯一性、字段完整性、每条规则可编译、**每个案例的示例都必须真的被改掉**、逐案例的期望结果断言、高危案例默认关闭

## [0.2.0] - 2026-10-24

改用 Go 重写，单个可执行文件跑起来不需要 Python 环境；行为与 0.1.0 逐字节对齐（同样的 `rules.json`、
同样的预演汇总、同样的备份与编码处理），并新增处理完自动打开报告。

### 变更

- **主实现改为 Go**：`cmd/desensitize` + `internal/{rules,mask,files,report,browser,fileurl}`，
  按 `cmd/` + `internal/` 分层，平台相关的打开浏览器逻辑用构建标签分文件
- **Python 版移入 `python/` 并冻结**：仍然可跑，行为作为对照；新功能只进 Go 版
- **版本注入**：`go build -ldflags "-X main.version=v0.2.0"`，`--version` 输出编译期版本

### 新增

- **处理完自动打开 HTML 报告**：`--report x.html` 之后自动判断能否拉起浏览器——
  `--no-open` 或 `DESENSITIZE_NO_BROWSER` 显式关闭；stdout 不是终端（管道/CI/服务）只打印路径；
  图形会话下 Windows 走 `rundll32`、Linux 走 `xdg-open`（无 `DISPLAY`/`WAYLAND_DISPLAY` 自动降级）、macOS 走 `open`
- **`--open`**：没给 `--report` 时自动生成临时 HTML 报告并打开，方便先看再决定
- **跳过日期单元格提示**：xlsx 里被跳过的日期格数量会列在明细里，不再只是静默不动

### 修复

- xlsx 文本格写回时保持原类型：`TCK-0001` 脱敏成 `0001` 仍是文本，不会被 `SetCellInt` 写成 `1`
- 报告里的本地链接改成 `file:///C:/...` 三斜杠，Windows 上双击才有效
- 开关可以写在目标后面（`desensitize data --dry-run`），不再被标准库 flag 的「遇到第一个非开关就停」吃掉

### 工程

- 36 个 Go 测试函数：规则与模板、引擎优先级、编码往返、xlsx 单元格、目录发现、CLI 退出码、打开判断
- CI 拆成两个 job：Go（ubuntu / windows 1.26，`gofmt` + `go vet` + `go test`）与 Python（ubuntu 3.10 / 3.13，45 条断言）
- 交叉验证：同一批夹具分别用 Python 与 Go 处理，4 个文本文件逐字节一致，xlsx 单元格零差异

## [0.1.0] - 2026-10-24

首个公开版本（Python 实现）。

### 新增

- **文本脱敏**：`.txt` / `.md` / `.markdown` / `.csv` / `.log` / `.ini` / `.conf` / `.yaml` / `.yml` / `.json`
- **Excel 脱敏**：`.xlsx` / `.xlsm`，保留公式、日期、布尔与数值类型；`.xls` 明确不支持，只在提示里列出并跳过
- **五种规则类型**
  - `literal`：字面替换或删除，支持 `boundary: none|digit|word|ip_prefix`
  - `regex`：裸正则，支持 `digit_boundary`、`ignore_case`、捕获组回填
  - `digits`：定长数字，可选跳过合法 `YYYYMMDD`
  - `ip_shift`：按段位移 IP，支持首段固定、逐段偏移、`source_cidrs` 网段过滤、超 255 取模
  - `hash`：SHA-256 等长变形并保留尾号，支持 `salt`
- **替换模板**：`{stars}` / `{keep:N}` / `{head:N}` / `{len}` / `{gN}`，空串表示删除
- **规则外置** `rules.json`，首次运行自动从 `rules.sample.json` 复制；`--rules` 指定的文件不存在直接报错，不会静默套用样例
- **四段式预演**：规则汇总 → 目录分布 → 逐条明细 → 提示；`--top N` 列出每规则最高频命中值，用来发现误伤
- **写盘保护**：确认后才备份 `原文件名.<YYYYMMDD-HHMMSS>.bak` 并原地覆盖；文本用临时文件 + `os.replace` 原子写
- **编码保持**：UTF-8 / UTF-8-BOM / UTF-16-LE / UTF-16-BE / GB18030，BOM 与 CRLF/LF 原样写回
- **目录递归**：默认全层级，跳过隐藏目录、`node_modules`、`__pycache__`、`venv`、Excel 锁文件与 `.bak`
- **报告导出**：HTML（左目录右内容、上下文高亮、`file://` 打开原文件）、CSV、JSON
- **退出码**：`0` 成功 / `1` 无文件或规则缺失 / `2` 参数错误 / `3` 未确认 / `4` 无命中

### 已知限制

- `.xls` 不支持
- xlsx 回写会丢图表、图片、数据透视表、数据校验（openpyxl 限制），处理前会点名告警
- 不计算公式，合并单元格只处理左上角
- `ip_shift` / `hash` 不可重跑

### 工程

- `tests/verify.py`：45 条断言，覆盖五种规则类型、编码写回、xlsx 数值/日期/公式、嵌套递归、隐藏目录
- GitHub Actions：ubuntu-latest 3.10 / 3.13、windows-latest 3.12

[0.5.2]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.5.2
[0.5.1]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.5.1
[0.5.0]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.5.0
[0.4.0]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.4.0
[0.3.0]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.3.0
[0.2.0]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.2.0
[0.1.0]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.1.0
