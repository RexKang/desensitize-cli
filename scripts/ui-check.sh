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

field() { echo "$REPORT" | grep -o "\"$1\":[0-9]*" | head -1 | cut -d: -f2; }
echo "$REPORT" | grep -q '"ok":true' || fail "自检失败: $(echo "$REPORT" | head -c 300)"
[ "$(field presets)" -ge 10 ]        || fail "页面里案例数 $(field presets)"
[ "$(field rulesAfterLoad)" -ge 1 ]  || fail "载入案例后规则仍为空"
[ "$(field listRendered)" -eq "$(field rulesAfterLoad)" ] || fail "左栏渲染 $(field listRendered) 条 ≠ 规则 $(field rulesAfterLoad) 条"
[ "$(field hits)" -ge 1 ]            || fail "空跑没命中"
[ "$(field rows)" -ge 1 ]            || fail "结果表没有行"
grep -q '变更单.docx' "$WORK/dom.html" || fail "docx 没进预演结果"
[ "$(field queueLen)" -ge 1 ]        || fail "加入队列没生效"
echo "$REPORT" | grep -q '"confirmShown":true' || fail "确认弹窗没弹出"
[ "$(field confirmActive)" -ge 1 ]   || fail "确认弹窗没列出启用规则"
[ "$(field executed)" -ge 1 ]        || fail "执行没写任何文件"
[ "$(field execFailed)" -eq 0 ]      || fail "执行有失败文件"
grep -q '\.bak' "$WORK/dom.html" && ok "队列  1 套 · 确认弹窗列出 $(field confirmActive) 条启用规则 · 执行写入 $(field executed) 个文件"
[ "$(field marked)" -ge 1 ]          || fail "上下文没有高亮"
echo "$REPORT" | grep -q '"banner":"ok"' || fail "banner 状态不是 ok"
grep -q '工单.txt' "$WORK/dom.html" || fail "结果表没显示文件名"
ok "页面  案例 $(field presets) · 载入 $(field rulesAfterLoad) 条 · 命中 $(field hits) 处 / $(field files) 文件 · 结果 $(field rows) 行 · 高亮 $(field marked) 处"

# ── 3) 截图留证 ───────────────────────────────────────────
"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$WORK_WIN/prof2" --virtual-time-budget=12000 --window-size=1500,950 \
  --screenshot="$WORK_WIN/ui.png" "$SELF_URL" >/dev/null 2>&1
[ -s "$WORK/ui.png" ] || fail "截图失败"
ok "截图  $WORK/ui.png"
echo "全部通过"
