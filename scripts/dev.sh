#!/usr/bin/env bash
# make dev：同時跑 Go 服務（air 熱重載）與前端 Vite，Ctrl+C 一起停。
# 開發用 8782 埠與 .data/，不會和 make install 裝好的服務（8780）互相干擾。
set -euo pipefail
cd "$(dirname "$0")/.."
RUN="${RUN:-}"
export XBOT_DEV=1 XBOT_DATA_DIR="${XBOT_DATA_DIR:-$PWD/.data}" XBOT_PORT="${XBOT_PORT:-8782}"

[ -d web/node_modules ] || { echo "前端套件還沒裝，先執行 make setup（或 make deps）"; exit 1; }

pids=()
cleanup() { trap - INT TERM EXIT; kill "${pids[@]}" 2>/dev/null || true; wait 2>/dev/null || true; }
trap cleanup INT TERM EXIT

$RUN air -c .air.toml &
pids+=($!)
(cd web && $RUN pnpm dev) &
pids+=($!)

echo ""
echo "  前端（開這個）：http://localhost:${XBOT_WEB_PORT:-5181}"
echo "  API：          http://127.0.0.1:${XBOT_PORT}"
echo "  資料目錄：     ${XBOT_DATA_DIR}"
echo ""
wait -n "${pids[@]}"
