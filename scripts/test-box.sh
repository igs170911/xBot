#!/usr/bin/env bash
# Bot 電腦映像的冒煙測試（任務卡 B01）：開一台 Bot 電腦，確認
#   桌面（Xvnc＋XFCE）、Chromium（含 CDP 9222）、noVNC（6080）、Hermes（執行檔＋8642 的 gateway 主機）都起得來，
#   Claude Code／Codex CLI 的 ACP adapter 裝好且回應 initialize（E12），
# 做完一定把容器刪掉。
# 用法：scripts/test-box.sh            （映像預設 xbot-box:dev，可用 BOX_IMAGE=… 換）
#       KEEP=1 scripts/test-box.sh     （失敗時保留容器方便查）
set -euo pipefail

image=${BOX_IMAGE:-xbot-box:dev}
name=xbot-box-smoke-$$
timeout=${TIMEOUT:-120}
failed=0

docker image inspect "$image" >/dev/null 2>&1 || {
  echo "❌ 找不到映像 $image。請先執行 make box"; exit 1; }

cleanup() {
  if [ "$failed" = 1 ] && [ -n "${KEEP:-}" ]; then
    echo "（KEEP=1：保留容器 $name，看完用 docker rm -f $name 刪掉）"
  else
    docker rm -f "$name" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

ok()   { printf '  ✅ %s\n' "$1"; }
fail() { printf '  ❌ %s\n' "$1"; failed=1; }
in_box() { docker exec "$name" "$@"; }
# 以 Bot 使用者身分執行（和正式開機一樣用 XBOT_UID 降權）。
as_bot() { docker exec -u "$uid:$uid" "$name" "$@"; }

# 和 LazyBoy server/computers.py 一樣的資源設定，並用一般使用者身分跑 Bot。
uid=1000
echo "▶ 開機：$image"
docker run -d --name "$name" --shm-size 512m -e XBOT_UID=$uid -e XBOT_GID=$uid \
  -p 127.0.0.1::6080 "$image" >/dev/null

echo "▶ 等桌面就緒（最多 ${timeout} 秒）"
start=$(date +%s)
until in_box test -f /tmp/xbot/ready 2>/dev/null; do
  if [ "$(docker inspect -f '{{.State.Running}}' "$name")" != true ]; then
    echo "❌ 容器已停止，最後的紀錄："; docker logs --tail 40 "$name"; failed=1; exit 1
  fi
  if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then
    echo "❌ 等不到桌面就緒，最後的紀錄："; docker logs --tail 40 "$name"; failed=1; exit 1
  fi
  sleep 1
done
ok "桌面就緒（$(( $(date +%s) - start )) 秒）"

# 有些服務在 ready 之後才陸續起來，給一段時間重試。
retry() {
  local i
  for i in $(seq 1 30); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}

echo "▶ 檢查"
if retry in_box xdpyinfo -display :1; then ok "X 顯示器 :1（Xtigervnc）"; else fail "X 顯示器 :1 沒起來"; fi
if retry in_box pgrep -x xfwm4 && retry in_box pgrep -x xfce4-panel; then ok "XFCE（xfwm4＋xfce4-panel）"; else fail "XFCE 沒起來"; fi

version=$(in_box chromium --version 2>/dev/null || true)
if [ -n "$version" ]; then ok "Chromium 已安裝：$version"; else fail "找不到 Chromium"; fi
cdp() { in_box curl -fsS --max-time 2 http://127.0.0.1:9222/json/version; }
if retry cdp; then ok "Chromium 執行中，CDP 9222 有回應"; else fail "Chromium／CDP 9222 沒有回應"; fi

port=$(docker port "$name" 6080/tcp | head -n1 | sed 's/.*://')
novnc() { curl -fsS --max-time 3 -o /dev/null "http://127.0.0.1:$port/vnc.html"; }
if retry novnc; then ok "noVNC 在 6080 有回應（主機埠 $port）"; else fail "noVNC 6080 沒有回應"; fi

hermes=$(as_bot hermes --version 2>/dev/null | head -n1 || true)
if [ -n "$hermes" ]; then ok "Hermes 可執行：$hermes"; else fail "hermes --version 失敗"; fi
pinned=$(tr -d '[:space:]' < "$(dirname "$0")/../box/HERMES_VERSION")
inside=$(in_box cat /opt/hermes/HERMES_VERSION 2>/dev/null | tr -d '[:space:]' || true)
if [ "$inside" = "$pinned" ]; then ok "Hermes 版本固定在 ${pinned:0:12}"; else fail "映像裡的 Hermes 版本（${inside:-無}）和 box/HERMES_VERSION（$pinned）不同"; fi
if as_bot env PYTHONPATH=/opt/hermes python -c 'import tui_gateway.ws, run_agent' >/dev/null 2>&1; then
  ok "Hermes gateway 模組可載入（tui_gateway.ws）"; else fail "Hermes gateway 模組載入失敗"; fi
health() { in_box curl -fsS --max-time 2 http://127.0.0.1:8642/health | grep -q '"ok":true'; }
if retry health; then ok "Hermes gateway 主機（hermes_host.py）在 8642 回應 /health"; else fail "8642 /health 沒有回應"; fi
if in_box test -d /product-ipc && in_box test ! -e /opt/xbot/product_mcp.py; then
  ok "product_mcp.py 已移除，預留 /product-ipc/product.sock 給 XBot MCP"; else fail "product.sock 預留位置不對"; fi

# ACP adapters (E12): installed, runnable as the bot user, and answer ACP initialize without a login.
acp_init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false}}}'
for adapter in claude-agent-acp codex-acp; do
  reply=$(as_bot sh -c "(echo '$acp_init'; sleep 15) | timeout 40 $adapter 2>/dev/null | head -n1" || true)
  if printf '%s' "$reply" | grep -q '"protocolVersion":1'; then
    ok "$adapter 已安裝，回應 ACP initialize（$(printf '%s' "$reply" | grep -o '"version":"[^"]*"' | head -n1)）"
  else
    fail "$adapter 沒有回應 ACP initialize：${reply:-（沒有輸出）}"
  fi
done
if in_box test -d /opt/xbot/cred && [ "$(in_box stat -c '%U %a' /opt/xbot/cred)" = "root 755" ] && [ -z "$(in_box ls -A /opt/xbot/cred)" ]; then
  ok "訂閱憑證資料夾 /opt/xbot/cred 是空的、屬於 root"; else fail "/opt/xbot/cred 不對（要空的、root 755）"; fi

# XBot MCP 的 stdio 橋接（C11）：在、可執行，而且沒有 product.sock 時好好回錯誤，不會卡住或崩潰。
mcp_init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
bridge=$(as_bot sh -c "echo '$mcp_init' | XBOT_WORKER_KEY=smoke timeout 10 xbot-mcp" 2>&1 || true)
if printf '%s' "$bridge" | grep -q '"id":1,"error"'; then
  ok "XBot MCP 橋接 xbot-mcp 可執行（沒有 product.sock 時回錯誤）"; else fail "xbot-mcp 不正常：$bridge"; fi

if [ "$failed" = 1 ]; then
  echo "❌ Bot 電腦冒煙測試失敗。最後的紀錄："; docker logs --tail 40 "$name" || true
  exit 1
fi
echo "✅ Bot 電腦冒煙測試通過（$image）"
