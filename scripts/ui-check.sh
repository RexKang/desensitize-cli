#!/usr/bin/env bash
# 界面自检：起临时实例 → 无头浏览器真打开页面 → 断言渲染与交互 → 截图留证
#
# 用法：scripts/ui-check.sh [样例目录]
# 依赖：Chrome 或 Edge（自动查找）、curl。不依赖 python。
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${UI_CHECK_PORT:-18190}"
WORK="$ROOT/.ui-check"   # 放仓库内，避免 Windows 上 TMPDIR 混用两种分隔符
TARGET="${1:-}"   # 留空则用 .ui-check 下的样例（Windows 原生路径）

fail() { echo "FAIL  $*" >&2; exit 1; }
ok()   { echo "OK    $*"; }

find_browser() {
  for p in \
    "/c/Program Files/Google/Chrome/Application/chrome.exe" \
    "/c/Program Files (x86)/Google/Chrome/Application/chrome.exe" \
    "/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe" \
    "/c/Program Files/Microsoft/Edge/Application/msedge.exe"; do
    [ -x "$p" ] && { echo "$p"; return; }
  done
  command -v google-chrome || command -v chromium || command -v microsoft-edge || true
}

CHROME="$(find_browser)"
[ -n "$CHROME" ] || fail "没找到 Chrome/Edge；请手动打开页面检查，或装一个再来"

rm -rf "$WORK"; mkdir -p "$WORK"
# 原生程序（go / chrome）不认 MSYS 风格路径，转成 Windows 路径再传
WORK_WIN="$(cd "$WORK" && pwd -W 2>/dev/null || echo "$WORK")"
( cd "$ROOT" && go build -o "$WORK_WIN/desensitize.exe" ./cmd/desensitize ) || fail "编译失败"
[ -f "$WORK/desensitize.exe" ] || fail "编译产物没生成"
[ -n "$TARGET" ] || TARGET="$WORK_WIN/样例数据"

mkdir -p "$TARGET/sub"
printf '变更单 TCK-2026-0001\n联系人 13800138000\n内网 10.1.2.3 主机 host-web01\n' > "$TARGET/工单.txt"
printf '{"phone":"13900139000"}\n' > "$TARGET/sub/api.json"
cp "$ROOT/testdata/sample.docx" "$TARGET/变更单.docx" || fail "docx 样例没拷过去"

# 截图单独用一份干净样例：主自检会真执行（写盘），截图要看得到命中
SHOT="$WORK_WIN/截图样例"
rm -rf "$SHOT"; mkdir -p "$SHOT/sub"
printf '变更单 TCK-2026-0001\n联系人 13800138000\n内网 10.1.2.3 主机 host-web01\n' > "$SHOT/工单.txt"
printf '{"phone":"13900139000"}\n' > "$SHOT/sub/api.json"
cp "$ROOT/testdata/sample.docx" "$SHOT/变更单.docx" || fail "截图样例没铺好"

( cd "$WORK" && ./desensitize.exe gui --port "$PORT" --no-open --keep-alive > gui.log 2>&1 & )
for _ in $(seq 1 24); do
  sleep 0.5
  grep -q "http://127.0.0.1:$PORT" "$WORK/gui.log" 2>/dev/null && break
done
URL="$(grep -o "http://127.0.0.1:$PORT/?t=[a-f0-9]*" "$WORK/gui.log" | head -1)"
[ -n "$URL" ] || { cat "$WORK/gui.log"; fail "服务没起来"; }
TOKEN="${URL##*t=}"
cleanup() { curl -s -X POST -H "X-Token: $TOKEN" "http://127.0.0.1:$PORT/api/quit" >/dev/null 2>&1; }
trap cleanup EXIT

# ── 1) 接口层 ─────────────────────────────────────────────
STATE="$(curl -s -H "X-Token: $TOKEN" "http://127.0.0.1:$PORT/api/state")"
echo "$STATE" | grep -q '"ok":true' || fail "state 接口异常: $(echo "$STATE" | head -c 200)"
PRESETS=$(echo "$STATE" | grep -o '"id":"[a-z-]*","name"' | wc -l | tr -d ' ')
[ "$PRESETS" -ge 10 ] || fail "案例数只有 $PRESETS 个"
ok "接口  state 正常 · 案例 $PRESETS 个"

# 单条规则校验：编辑框里每次改动都会调它
CHECK="$(curl -s -X POST -H "X-Token: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"rule":{"id":"m","desc":"手机号","type":"regex","pattern":"1[3-9]\\d{9}","replace":"{stars}"}}' \
  "http://127.0.0.1:$PORT/api/rule-check")"
echo "$CHECK" | grep -q '"valid":true' || fail "rule-check 没判合法: $CHECK"
echo "$CHECK" | grep -q '13800138000' || fail "rule-check 没给示例: $CHECK"
ok "接口  rule-check 合法 + 示例"

# 选路径接口只验路由与入参：真弹原生对话框要人点，不能在无头里跑
CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "X-Token: $TOKEN" \
  -H 'Content-Type: application/json' -d '{"mode":"bad"}' "http://127.0.0.1:$PORT/api/pick")"
[ "$CODE" = "400" ] || fail "pick 接口对非法 mode 应 400, got $CODE"
ok "接口  pick 路由已注册（非法 mode → 400）"

# ── 2) 真开页面（自检模式走真实代码路径）────────────────────
ENC_TARGET=$(echo "$TARGET" | sed 's/ /%20/g')
SELF_URL="$URL&selftest=1&target=$ENC_TARGET"
"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$WORK_WIN/prof" --virtual-time-budget=15000 --window-size=1500,950 \
  --dump-dom "$SELF_URL" > "$WORK/dom.html" 2>/dev/null
[ -s "$WORK/dom.html" ] || fail "页面没渲染出来"

REPORT=$(sed -n 's/.*id="selftest-report"[^>]*>\(.*\)<\/pre>.*/\1/p' "$WORK/dom.html" | head -1)
[ -n "$REPORT" ] || fail "页面里没有自检报告（JS 可能抛错了）"
REPORT=$(echo "$REPORT" | sed 's/&quot;/"/g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g')

field()    { echo "$REPORT" | grep -o "\"$1\":[0-9]*" | head -1 | cut -d: -f2; }
strfield() { echo "$REPORT" | grep -o "\"$1\":\"[^\"]*\"" | head -1 | cut -d'"' -f4; }

echo "$REPORT" | grep -q '"ok":true' || fail "自检失败: $(echo "$REPORT" | head -c 300)"
[ "$(field presets)" -ge 10 ]        || fail "页面里案例数 $(field presets)"
[ "$(field casesRendered)" -ge 10 ]  || fail "左栏模板只渲染 $(field casesRendered) 个"
[ "$(field rulesAfterLoad)" -ge 1 ]  || fail "载入模板后规则仍为空"
[ "$(field listRendered)" -eq "$(field rulesAfterLoad)" ] || fail "左栏渲染 $(field listRendered) 条 ≠ 规则 $(field rulesAfterLoad) 条"
[ "$(field probeHits)" -ge 1 ]       || fail "单条试跑没命中"
[ "$(field probeRows)" -ge 1 ]       || fail "单条试跑没有结果行"
echo "$(strfield checkLine)" | grep -q '校验通过' || fail "规则校验提示不对: $(strfield checkLine)"

# 队列
[ "$(field queueLen)" -ge 1 ]        || fail "加入待执行没生效"
[ "$(field queueCards)" -eq "$(field queueLen)" ] || fail "队列卡片 $(field queueCards) 个 ≠ 队列 $(field queueLen) 套"
[ "$(field queueWarnBadge)" -ge 1 ]  || fail "改了规则库，队列没有「规则库已改」徽标"

# 空跑与结果
[ "$(field hits)" -ge 1 ]            || fail "空跑没命中"
[ "$(field rows)" -ge 1 ]            || fail "结果表没有行"
[ "$(field marked)" -ge 1 ]          || fail "上下文没有高亮"
[ "$(field queueEst)" -ge 1 ]        || fail "空跑后队列卡片没有预计命中数"
grep -q '变更单.docx' "$WORK/dom.html" || fail "docx 没进预演结果"
grep -q '工单.txt' "$WORK/dom.html"   || fail "结果表没显示文件名"

# 标签页：跑完停在「运行报告」，并且能切回「规则配置」
echo "$REPORT" | grep -q '"cfgStepOn":true'           || fail "初始不在②规则步骤"
echo "$REPORT" | grep -q '"reportStepOn":true'         || fail "空跑后没有切到③预演"
echo "$REPORT" | grep -q '"reportPaneVisible":true'  || fail "运行报告面板没显示"
echo "$REPORT" | grep -q '"cfgBackVisible":true'     || fail "切不回规则配置标签"
echo "$REPORT" | grep -q '"showReportEnabled":true'   || fail "跑完后「看报告」按钮没启用"
[ "$(field reportRows)" -eq "$(field rows)" ]  || fail "报告标签表格 $(field reportRows) 行 ≠ 结果 $(field rows) 行"
echo "$(strfield step3Label)" | grep -q '命中' || fail "③ 步骤没显示命中数: $(strfield step3Label)"

# 按钮配色（需求：空跑绿 / 执行红 / 清空结果蓝）
DRY="$(strfield dry)"; EXEC="$(strfield exec)"; CLEAR="$(strfield clear)"
[ "$DRY" = "rgb(31, 136, 61)" ]   || fail "空跑按钮不是绿色: $DRY"
[ "$EXEC" = "rgb(207, 34, 46)" ]  || fail "执行按钮不是红色: $EXEC"
[ "$CLEAR" = "rgb(9, 105, 218)" ] || fail "清空结果不是蓝色: $CLEAR"

# 布局（B 方案：4 步条 + 顶部目标条 + 底部常驻操作条）
STEPS=$(field steps); OVX=$(field pageOverflowX); EW=$(field editorWidth); RW=$(field rightWidth)
AB=$(field actionBarBottom); VH=$(field viewportH); SA=$(field stepActive)
[ "$STEPS" -eq 4 ] || fail "步骤条应 4 步, got $STEPS"
[ "$OVX" -eq 0 ] || fail "页面出现横向溢出 $OVX"
[ "$EW" -gt "$RW" ] || fail "中栏($EW) 应比右栏($RW) 宽"
[ "$AB" -le "$VH" ] || fail "底部操作条被挤出首屏: $AB > $VH"
[ "$SA" -eq 2 ] || fail "初始应停在②规则, got $SA"
echo "$REPORT" | grep -q '"execDisabledBefore":true' || fail "未预演时「执行」应禁用"
[ "$(strfield taskState)" = "已预演" ] || fail "空跑后状态徽标应是「已预演」，实际 $(strfield taskState)"
ok "布局  4 步 · 中栏 $EW > 右栏 $RW · 操作条在首屏内 · 无横向溢出"
ok "配色  空跑 $DRY · 执行 $EXEC · 清空 $CLEAR"

# 确认弹窗与执行
echo "$REPORT" | grep -q '"confirmShown":true' || fail "确认弹窗没弹出"
[ "$(field confirmActive)" -ge 1 ]   || fail "确认弹窗没列出启用规则"
echo "$(strfield cfScope)" | grep -q '个文件' || fail "确认弹窗没写将改写几个文件: $(strfield cfScope)"
echo "$(strfield cfPreview)" | grep -q '命中' || fail "确认弹窗没复述上次预演: $(strfield cfPreview)"
[ "$(field executed)" -ge 1 ]        || fail "执行没写任何文件"
[ "$(field execFailed)" -eq 0 ]      || fail "执行有失败文件"
echo "$REPORT" | grep -q '"banner":"ok"' || fail "banner 状态不是 ok"
grep -q '\.bak' "$WORK/dom.html" && ok "执行  写入 $(field executed) 个文件 · 确认弹窗列出 $(field confirmActive) 条启用规则"
ok "页面  案例 $(field presets) · 载入 $(field rulesAfterLoad) 条 · 单条试跑 $(field probeHits) 处 · 合并空跑 $(field hits) 处 / $(field files) 文件 · 结果 $(field rows) 行 · 高亮 $(field marked) 处"
ok "步骤  ②规则 / ③预演 可互切 · ③ 状态 $(strfield step3Label) · 报告表 $(field reportRows) 行"

# 删除图片规则：类型可选、编辑器只给说明
echo "$REPORT" | grep -q '"stripRuleOption":true' || fail "规则类型里没有 strip_images"
[ "$(field stripRuleInputs)" -eq 2 ] || fail "删除图片规则应只剩 2 个输入框（id/显示名），实际 $(field stripRuleInputs)"
echo "$(strfield stripRuleEditor)" | grep -q '结构型规则' || fail "删除图片规则没有结构型提示: $(strfield stripRuleEditor)"
ok "规则  strip_images 可选 · 编辑器只剩说明（无 pattern/replace）"

# ── 3) 截图留证（dry 模式：看得到命中，也不写盘）───────────
ENC_SHOT=$(echo "$SHOT" | sed 's/ /%20/g')
SHOT_URL="$URL&selftest=dry&target=$ENC_SHOT"
"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$WORK_WIN/prof2" --virtual-time-budget=12000 --window-size=1500,950 \
  --screenshot="$WORK_WIN/ui.png" "$SHOT_URL" >/dev/null 2>&1
[ -s "$WORK/ui.png" ] || fail "截图失败"

"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$WORK_WIN/prof4" --virtual-time-budget=10000 --window-size=1500,950 \
  --screenshot="$WORK_WIN/ui-config.png" "$URL&selftest=cfg&target=$ENC_SHOT" >/dev/null 2>&1
[ -s "$WORK/ui-config.png" ] || fail "规则配置页截图失败"

"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$WORK_WIN/prof5" --virtual-time-budget=10000 --window-size=1500,950 \
  --screenshot="$WORK_WIN/ui-strip.png" "$URL&selftest=strip&target=$ENC_SHOT" >/dev/null 2>&1
[ -s "$WORK/ui-strip.png" ] || fail "删除图片规则截图失败"

"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$WORK_WIN/prof3" --virtual-time-budget=10000 --window-size=1500,950 \
  --screenshot="$WORK_WIN/ui-confirm.png" "$URL&selftest=confirmoff&target=$ENC_SHOT" >/dev/null 2>&1
[ -s "$WORK/ui-confirm.png" ] || fail "确认弹窗截图失败"
ok "截图  $WORK/ui.png / $WORK/ui-config.png / $WORK/ui-strip.png / $WORK/ui-confirm.png"
echo "全部通过"
