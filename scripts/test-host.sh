#!/usr/bin/env bash
# 驗證 B12 的 make deploy 與 make host-add，不會動到你自己的機器：
#   A. 部署：一台有 systemd＋sshd 的乾淨 Debian 容器（像一台雲端主機），用你自己的 SSH（測試金鑰）部署 XBot：
#      第一次安裝 → 再部署一次（可以重複執行）→ 部署壞掉的版本要自動換回上一版、服務照常。
#   B. 加主機：一台「剛租來的主機」容器（Debian＋sshd＋Docker，沒有 xbot 帳號），本機開一個暫時的 XBot 服務，
#      xbot host add 用 root 的 SSH 跑安裝腳本並加入（跑兩次不出錯），再 xbot host check。
# 用法：scripts/test-host.sh（make test-host 會先 make build）。只跑其中一段：ONLY=deploy 或 ONLY=add。
set -euo pipefail
cd "$(dirname "$0")/.."
arch=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
[ -x "bin/xbot-linux-$arch" ] || { echo "先執行 make build"; exit 1; }
BOX_IMAGE=${BOX_IMAGE:-xbot-box:dev}

work=$(mktemp -d)
deploy_name=xbot-deploy-test
fresh_name=xbot-hostadd-test
net=xbot-test-host-net
serve_pid=""
cleanup() {
  [ -n "$serve_pid" ] && kill "$serve_pid" 2>/dev/null || true
  docker rm -f "$deploy_name" "$fresh_name" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
cleanup_containers() { docker rm -f "$deploy_name" "$fresh_name" >/dev/null 2>&1 || true; docker network rm "$net" >/dev/null 2>&1 || true; }
cleanup_containers
docker network create "$net" >/dev/null

ssh-keygen -q -t ed25519 -N '' -f "$work/key"
ssh_opts="-i $work/key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes"
step() { printf '\n▶ %s\n' "$*"; }
ip_of() { docker inspect -f "{{(index .NetworkSettings.Networks \"$net\").IPAddress}}" "$1"; }
wait_ssh() { # 等 sshd 能登入
  for _ in $(seq 60); do ssh $ssh_opts "$1" true 2>/dev/null && return 0; sleep 1; done
  echo "❌ $1 的 SSH 沒起來"; return 1
}

if [ "${ONLY:-}" != "add" ]; then
  # ── A. make deploy ─────────────────────────────────────
  img=xbot-deploy-test:local
  docker build -q -t "$img" - >/dev/null <<'EOF'
FROM debian:13
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv dbus dbus-user-session libpam-systemd openssh-server curl ca-certificates >/dev/null \
 && useradd -m -u 1000 -s /bin/bash xb && mkdir -p /var/lib/systemd/linger && touch /var/lib/systemd/linger/xb \
 && systemctl enable ssh
CMD ["/sbin/init"]
EOF
  docker run -d --name "$deploy_name" --network "$net" --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock "$img" >/dev/null
  docker exec "$deploy_name" sh -c 'install -d -m 700 -o xb -g xb /home/xb/.ssh'
  docker cp "$work/key.pub" "$deploy_name":/home/xb/.ssh/authorized_keys
  docker exec "$deploy_name" sh -c 'chown xb:xb /home/xb/.ssh/authorized_keys && chmod 600 /home/xb/.ssh/authorized_keys'
  dest="xb@$(ip_of "$deploy_name")"
  wait_ssh "$dest"
  health() { ssh $ssh_opts "$dest" 'curl -fsS http://127.0.0.1:8780/healthz'; }

  step "A1. 第一次部署（安裝成服務）"
  YES=1 bin/xbot deploy --host "$dest" --ssh-opts "$ssh_opts"
  health >/dev/null && echo "✅ 服務正常"
  v1=$(ssh $ssh_opts "$dest" '.local/bin/xbot version')

  step "A2. 再部署一次（可以重複執行）"
  YES=1 bin/xbot deploy --host "$dest" --ssh-opts "$ssh_opts"
  health >/dev/null && echo "✅ 服務正常"

  step "A3. 部署壞掉的版本 → 應該失敗並自動換回上一版"
  printf '#!/bin/sh\nexit 1\n' >"$work/bad" && chmod +x "$work/bad"
  if YES=1 bin/xbot deploy --host "$dest" --ssh-opts "$ssh_opts" --bin "$work/bad"; then echo "❌ 壞掉的版本不該部署成功"; exit 1; fi
  health >/dev/null || { echo "❌ 回滾後服務沒有回應"; exit 1; }
  v3=$(ssh $ssh_opts "$dest" '.local/bin/xbot version')
  [ "$v1" = "$v3" ] || { echo "❌ 版本不對：$v3（應該是 $v1）"; exit 1; }
  echo "✅ 已換回上一版（$v3），服務正常"

  step "A4. 重開機 → 服務自動啟動"
  docker restart "$deploy_name" >/dev/null
  wait_ssh "$dest"
  for _ in $(seq 60); do health >/dev/null 2>&1 && break; sleep 1; done
  health >/dev/null || { echo "❌ 重開機後服務沒有自動啟動"; exit 1; }
  echo "✅ 重開機後自動啟動"
  docker rm -f "$deploy_name" >/dev/null
fi

if [ "${ONLY:-}" != "deploy" ]; then
  # ── B. make host-add ───────────────────────────────────
  docker image inspect "$BOX_IMAGE" >/dev/null 2>&1 || { echo "沒有 $BOX_IMAGE（先 make box），略過 host-add"; exit 0; }
  fimg=xbot-test-freshhost:script
  docker build -q -t "$fimg" internal/hosts/sshtesthost/fresh >/dev/null
  docker run -d --name "$fresh_name" --network "$net" --privileged --hostname cloud-test -v xbot-test-freshhost-docker:/var/lib/docker "$fimg" >/dev/null
  docker exec "$fresh_name" sh -c 'install -d -m 700 /root/.ssh'
  docker cp "$work/key.pub" "$fresh_name":/root/.ssh/authorized_keys
  docker exec "$fresh_name" sh -c 'chown root:root /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys'
  ip=$(ip_of "$fresh_name")
  wait_ssh "root@$ip"
  for _ in $(seq 60); do docker exec "$fresh_name" docker info >/dev/null 2>&1 && break; sleep 1; done

  data="$work/data"
  port=8797
  export XBOT_DATA_DIR="$data" XBOT_PORT=$port XBOT_BOX_IMAGE="$BOX_IMAGE"
  bin/xbot serve >"$work/serve.log" 2>&1 &
  serve_pid=$!
  for _ in $(seq 30); do curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break; sleep 0.5; done

  step "B1. make host-add（第一次：跑安裝腳本、加入、傳映像）"
  YES=1 bin/xbot host add --host "root@$ip" --ssh-opts "$ssh_opts" --no-tailscale --name cloud-test --max-running 2
  step "B2. make host-add（第二次：不能出錯）"
  YES=1 bin/xbot host add --host "root@$ip" --ssh-opts "$ssh_opts" --no-tailscale
  step "B3. make host-check"
  bin/xbot host check --host cloud-test
  n=$(bin/xbot hosts list | grep -c '"kind": "ssh"' || true)
  [ "$n" = 1 ] || { echo "❌ 主機清單應該只有一台遠端主機，有 $n 台"; exit 1; }
  echo "✅ 清單裡只有一台 cloud-test"
  kill "$serve_pid" 2>/dev/null || true
  wait "$serve_pid" 2>/dev/null || true
  serve_pid=""
  docker exec "$fresh_name" sh -c 'ids=$(docker ps -aq --filter label=xbot.managed); [ -z "$ids" ] || docker rm -f $ids >/dev/null; true'
fi

printf '\n✅ 主機與部署測試全部通過\n'
