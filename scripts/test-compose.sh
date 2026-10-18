#!/usr/bin/env bash
# 驗證 make up（docker compose 版本）真的能開 Bot 的電腦：make up → 設假模型 → 建 Bot → 經服務對話（電腦開機、
# Hermes 經 product.sock 呼叫模型）→ 休眠後再對話一次（服務自己把電腦叫醒）。結束時清掉這次建的容器、volume 與資料。
# 用法：scripts/test-compose.sh（要 Docker 與 make box 建好的映像）。COMPOSE_TEST_PORT 改服務埠（預設 9043）。
set -euo pipefail
cd "$(dirname "$0")/.."
BOX_IMAGE=${BOX_IMAGE:-xbot-box:dev}
docker image inspect "$BOX_IMAGE" >/dev/null 2>&1 || { echo "先執行 make box"; exit 1; }
port=${COMPOSE_TEST_PORT:-9043}
model_port=$((port + 1))
# product.sock 的完整路徑不能超過 108 字元，所以資料放在短一點的地方。
data=$(mktemp -d "$HOME/.cache/xbct-XXXX")
export COMPOSE_PROJECT_NAME=xbotcomposetest XBOT_PORT=$port XBOT_HOST_BIND=127.0.0.1 XBOT_BOX_IMAGE=$BOX_IMAGE
svc=$COMPOSE_PROJECT_NAME-xbot-1
bot=""
cleanup() {
  if [ -n "$bot" ]; then
    docker rm -f "xbot-box-$bot" >/dev/null 2>&1 || true
    docker volume rm "xbot-box-$bot-home" "xbot-box-$bot-workspace" "xbot-box-$bot-hermes" "xbot-box-$bot-cred" >/dev/null 2>&1 || true
  fi
  make --no-print-directory down XBOT_COMPOSE_DATA="$data" >/dev/null 2>&1 || true
  rm -rf "$data"
}
trap cleanup EXIT
x() { docker exec -i -e XBOT_URL="http://127.0.0.1:$port" "$svc" xbot "$@"; }
field() { python3 -c "import json,sys; print(json.load(sys.stdin)['$1'])"; }
step() { printf '\n▶ %s\n' "$*"; }

step "1. make up"
make --no-print-directory up XBOT_COMPOSE_DATA="$data"
curl -fsS --retry 30 --retry-connrefused --retry-delay 1 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 || { echo "❌ 服務沒有起來"; docker logs --tail 30 "$svc"; exit 1; }
docker exec -d "$svc" sh -c "xbot dev fake-model --addr 127.0.0.1:$model_port >/tmp/fake-model.log 2>&1"

step "2. 設定假模型、建 Bot"
printf '{"id":"fake","base_url":"http://127.0.0.1:%s/v1","api_key":"sk-fake","default_model":"fake-model"}' "$model_port" | x models set_provider --json - >/dev/null
bot=$(x bots create --name 組合測試 | field id)

talk() {
  task=$(x chat send --bot_id "$bot" --text "$1" | field task_id)
  reply=$(x chat wait_until_settled --task_id "$task" --timeout_s 240 | field text)
  [ "$reply" = OK ] || { echo "❌ Bot 沒有回答（$reply）"; docker logs --tail 30 "$svc"; exit 1; }
}
step "3. 經服務對話（第一次開機）"
talk "請回覆 OK"
echo "✅ Bot 的電腦開機，Hermes 經 product.sock 回答了"
test -S "$data/bots/$bot/ipc/product.sock" && echo "✅ product.sock 在主機上同一個路徑"

step "4. 休眠 → 再對話（服務把電腦叫醒）"
x computers sleep --bot_id "$bot" >/dev/null
talk "再一次"
echo "✅ 叫醒後照常回答"
# 電腦畫面只發布在主機的 127.0.0.1：服務（主機網路）要連得到。
addr=$(docker port "xbot-box-$bot" 6080/tcp | head -1)
docker exec "$svc" curl -fsS -o /dev/null "http://$addr/" && echo "✅ 服務連得到電腦畫面（$addr）"

printf '\n✅ docker compose 版本可以開 Bot 的電腦\n'
