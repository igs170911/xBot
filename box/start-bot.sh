#!/bin/bash
set -euo pipefail
# Keep Hermes and its child tools on the same persistent home after dropping UID.
export HOME=/home/box HERMES_REAL_HOME=/home/box XDG_CONFIG_HOME=/home/box/.config
# XBot starts the box as the bot user (UID/GID 1000, created in the image) with no capabilities and a read-only root
# filesystem (SEC-3): only /tmp, /run, /var/tmp (tmpfs) and the volumes are writable. Starting as root still works for
# a plain `docker run` (older XBot, manual tests): hand the volumes to the bot user and drop privileges.
if test "$(id -u)" = 0 && test -n "${XBOT_UID:-}" && test "$XBOT_UID" != 0; then
  bot_uid=$XBOT_UID
  bot_gid=${XBOT_GID:-$XBOT_UID}
  mkdir -p /tmp/.X11-unix
  chmod 1777 /tmp/.X11-unix
  chown -R "$bot_uid:$bot_gid" /hermes /home/box /workspace
  exec setpriv --reuid "$bot_uid" --regid "$bot_gid" --init-groups /usr/local/bin/start-bot.sh
fi
# Network guard (SEC-4): when XBot mounted the guard's marker, nothing here may touch the network before the host's
# firewall rules for bot computers are in place for this boot (after a host reboot the box can come up before them).
if test -d /opt/xbot/netguard && mountpoint -q /opt/xbot/netguard; then
  boot=$(cat /proc/sys/kernel/random/boot_id)
  waited=0
  until test "$(cat /opt/xbot/netguard/ready 2>/dev/null)" = "$boot"; do
    if test "$waited" -ge "${XBOT_NETGUARD_WAIT:-120}"; then
      echo 'Network guard (xbot-netguard) is not running on this host; refusing to start' >&2
      exit 1
    fi
    sleep 1
    waited=$((waited + 1))
  done
fi
# /tmp survives `docker restart`; markers from the previous boot must not be
# mistaken for this desktop's readiness or session bus.
rm -f /tmp/xbot/ready /tmp/xbot/dbus.env
/usr/local/bin/start-desktop.sh &
desktop_pid=$!
for attempt in $(seq 1 90); do
  if test -f /tmp/xbot/ready; then break; fi
  if ! kill -0 "$desktop_pid" 2>/dev/null; then wait "$desktop_pid"; exit 1; fi
  sleep 0.5
done
if ! test -f /tmp/xbot/ready; then echo 'Desktop did not become ready' >&2; exit 1; fi
source /tmp/xbot/dbus.env
export DBUS_SESSION_BUS_ADDRESS
export PYTHONPATH=/opt/hermes
# tini only signals its direct child. Stay in front of both services so
# `docker stop` reaches start-desktop.sh, which checkpoints and closes Chromium.
python /opt/xbot/hermes_host.py &
host_pid=$!
shutdown() {
  trap - TERM INT
  kill -TERM "$host_pid" "$desktop_pid" 2>/dev/null || true
  wait "$desktop_pid" || true
  wait "$host_pid" || true
  exit 0
}
trap shutdown TERM INT
status=0
wait "$host_pid" || status=$?
kill -TERM "$desktop_pid" 2>/dev/null || true
wait "$desktop_pid" || true
exit "$status"
