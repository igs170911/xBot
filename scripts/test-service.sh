#!/usr/bin/env bash
# 在有 systemd 的乾淨容器裡驗證 C06：安裝成服務、狀態、備份與還原、更新失敗自動回滾、
# 重開機自動啟動、移除。不會動到你自己的機器。
# 用法：scripts/test-service.sh（需要先 make build）
set -euo pipefail
cd "$(dirname "$0")/.."
[ -x bin/xbot-linux-amd64 ] || { echo "先執行 make build"; exit 1; }

name=xbot-service-test
img=xbot-systemd-test:local
docker rm -f "$name" >/dev/null 2>&1 || true
docker build -q -t "$img" - >/dev/null <<'EOF'
FROM debian:13
RUN apt-get update -qq && apt-get install -y -qq systemd systemd-sysv dbus dbus-user-session curl ca-certificates >/dev/null \
 && useradd -m -u 1000 -s /bin/bash xb && mkdir -p /var/lib/systemd/linger && touch /var/lib/systemd/linger/xb
CMD ["/sbin/init"]
EOF
docker run -d --name "$name" --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock "$img" >/dev/null
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT

as_user() {
  docker exec -u xb -e YES=1 -e XDG_RUNTIME_DIR=/run/user/1000 -e DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus -e USER=xb \
    -w /home/xb "$name" bash -c "$1"
}
wait_for() { # 等條件成立，最多 60 次
  for _ in $(seq 60); do if eval "$1" >/dev/null 2>&1; then return 0; fi; docker exec "$name" true; done
  return 1
}
step() { printf '\n▶ %s\n' "$*"; }

wait_for "as_user 'systemctl --user is-system-running --wait || systemctl --user show-environment'"
docker cp bin/xbot-linux-amd64 "$name":/home/xb/xbot
printf '#!/bin/sh\nexit 1\n' > /tmp/xbot-bad && docker cp /tmp/xbot-bad "$name":/home/xb/bad && rm /tmp/xbot-bad
docker exec "$name" chown xb:xb /home/xb/xbot /home/xb/bad
docker exec "$name" chmod +x /home/xb/xbot /home/xb/bad

step "1. 安裝成服務"
as_user './xbot service install --bin ./xbot'

step "2. 狀態"
as_user '.local/bin/xbot service status'
# Go 對 0.0.0.0 用雙協定 socket，會出現在 tcp6 的 [::]；兩個檔都看。
as_user 'grep -qE " (00000000|00000000000000000000000000000000):224C [0-9A-F:]+ 0A " /proc/net/tcp /proc/net/tcp6 && echo "✅ 服務聽所有介面的 8780（區網可連）"' || { echo "❌ 服務沒有聽 0.0.0.0"; exit 1; }

step "3. 備份 → 刪掉資料 → 還原"
as_user 'echo 測試資料 > .local/share/xbot/note.txt && .local/bin/xbot backup --out backups/t.tar.gz'
as_user 'rm -rf .local/share/xbot/note.txt'
as_user '.local/bin/xbot restore backups/t.tar.gz'
as_user 'test "$(cat .local/share/xbot/note.txt)" = 測試資料 && echo "✅ 資料完整回來"'

step "4. 更新成壞掉的版本 → 應該自動換回上一版"
if as_user '.local/bin/xbot service update --bin ./bad'; then echo "❌ 壞掉的版本不該更新成功"; exit 1; fi
as_user 'curl -fsS http://127.0.0.1:8780/healthz && echo && echo "✅ 回滾後服務正常"'

step "5. 重開機（重啟容器）→ 服務應自動啟動"
docker restart "$name" >/dev/null
wait_for "as_user 'curl -fsS http://127.0.0.1:8780/healthz'" || { echo "❌ 重開機後服務沒有自動啟動"; exit 1; }
echo "✅ 重開機後自動啟動"

step "6. 移除（資料保留）"
as_user '.local/bin/xbot service uninstall'
as_user 'test ! -e .config/systemd/user/xbot.service && test ! -e .local/bin/xbot && test -e .local/share/xbot/access-token && echo "✅ 服務已移除，資料保留"'

printf '\n✅ 服務安裝測試全部通過\n'
