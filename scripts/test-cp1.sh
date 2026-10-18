#!/usr/bin/env bash
# CP1／關卡 G0 驗收：Claude Code 透過 MCP 建一個 Bot、和它對話，網頁上同步看得到。
#
# 會用到：make build、make box 建好的映像、Docker、claude 指令（用你目前的登入，
# 只用 --mcp-config 暫時掛上 XBot，不會改你的 Claude 設定）。
# 模型預設用假模型（xbot dev fake-model，永遠回 OK）；要用真模型請設
#   CP1_BASE_URL、CP1_MODEL、CP1_API_KEY（金鑰從環境變數讀，不會出現在指令列）。
set -euo pipefail
cd "$(dirname "$0")/.."
[ -x bin/xbot ] || { echo "先執行 make build"; exit 1; }
command -v claude >/dev/null || { echo "找不到 claude 指令"; exit 1; }

work=$(mktemp -d)
port=${CP1_PORT:-8793}
export XBOT_DATA_DIR="$work/data" XBOT_PORT=$port XBOT_URL="http://127.0.0.1:$port"
pids=()
cleanup() {
  kill "${pids[@]}" 2>/dev/null || true
  # 清掉這次建立的 Bot 電腦與資料卷
  for id in $(cat "$work/bot-ids" 2>/dev/null); do
    docker rm -f "xbot-box-$id" >/dev/null 2>&1 || true
    docker volume rm "xbot-box-$id-home" "xbot-box-$id-workspace" "xbot-box-$id-hermes" >/dev/null 2>&1 || true
  done
  rm -rf "$work"
}
trap cleanup EXIT

bin/xbot serve >"$work/serve.log" 2>&1 & pids+=($!)
if [ -z "${CP1_BASE_URL:-}" ]; then
  bin/xbot dev fake-model --addr 127.0.0.1:8798 >"$work/model.log" 2>&1 & pids+=($!)
  base=http://127.0.0.1:8798/v1 model=fake-model key=sk-fake
else
  base=$CP1_BASE_URL model=$CP1_MODEL key=${CP1_API_KEY:?需要 CP1_API_KEY}
fi
curl -fsS --retry 30 --retry-connrefused --retry-delay 1 "$XBOT_URL/healthz" >/dev/null
echo "▶ 服務：$XBOT_URL（資料：$XBOT_DATA_DIR）"

echo "▶ 1. 設定模型"
printf '{"id":"cp1","base_url":"%s","api_key":"%s","default_model":"%s"}' "$base" "$key" "$model" | bin/xbot models set_provider --json - >/dev/null

echo "▶ 2. 產生配對碼並換成代理權杖"
code=$(bin/xbot agents pair_start --name "Claude Code（CP1）" --scopes read --scopes operate --scopes manage | python3 -c 'import json,sys; print(json.load(sys.stdin)["code"])')
token=$(curl -fsS -X POST -H 'Content-Type: application/json' -d "{\"code\":\"$code\",\"name\":\"Claude Code（CP1）\"}" "$XBOT_URL/api/v1/agents.pair" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')

cat >"$work/mcp.json" <<EOF
{"mcpServers":{"xbot":{"type":"http","url":"$XBOT_URL/mcp","headers":{"Authorization":"Bearer $token"}}}}
EOF

echo "▶ 3. Claude Code 透過 MCP 建 Bot 並對話"
prompt='請只用 xbot 的 MCP 工具完成：1) 建立一個名字叫「小秘」的 Bot（描述：CP1 測試）；2) 對它說「請回覆 OK」；3) 用 wait_until_settled 等它回答；4) 最後只輸出一行：BOT_REPLY=<Bot 的回答原文>'
(cd "$work" && claude -p --mcp-config "$work/mcp.json" --strict-mcp-config \
   --allowedTools "mcp__xbot" --output-format text "$prompt" </dev/null) | tee "$work/claude.out"

echo "▶ 4. 從服務端確認"
bots=$(bin/xbot bots list)
echo "$bots" | grep -q '小秘' && echo "✅ 服務裡有 Bot「小秘」" || { echo "❌ 找不到 Bot"; exit 1; }
grep -q 'BOT_REPLY=.*OK' "$work/claude.out" && echo "✅ Claude Code 拿到 Bot 的回答" || { echo "❌ Claude Code 沒拿到回答"; exit 1; }
bin/xbot audit list | grep -q 'agent' && echo "✅ 稽核紀錄裡有代理的操作"

echo "▶ 5. 網頁上看得到這段對話（截圖）"
bot_id=$(echo "$bots" | python3 -c 'import json,sys; print([b for b in json.load(sys.stdin)["bots"] if b["name"]=="小秘"][0]["id"])')
echo "$bot_id" >>"$work/bot-ids"
(cd web && XBOT_URL="$XBOT_URL" BOT_ID="$bot_id" OUT="${CP1_SHOT:-../docs/checkpoints/cp1-web.png}" \
  mise exec -- node -e '
const { chromium } = require("@playwright/test");
(async () => {
  const b = await chromium.launch(); const p = await b.newPage({ viewport: { width: 1440, height: 900 } });
  await p.goto(process.env.XBOT_URL + "/bots/" + process.env.BOT_ID);
  await p.getByText("OK", { exact: true }).first().waitFor({ timeout: 15000 });
  await p.screenshot({ path: process.env.OUT }); await b.close();
  console.log("✅ 網頁顯示 Bot 的回答，截圖：" + process.env.OUT);
})().catch(e => { console.error("❌ 網頁沒有顯示回答：" + e.message); process.exit(1); });')

echo
echo "✅ CP1 驗收通過"
