# Changelog

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)，格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [0.1.0] - 2026-10-24

首个公开版本。

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

[0.1.0]: https://github.com/RexKang/desensitize-cli/releases/tag/v0.1.0
