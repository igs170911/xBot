#!/bin/sh
# 放公鑰、產生主機金鑰、讓 sshd 被殺掉後自動重啟（測重連用），最後啟動 dockerd。
set -e
install -d -o xbot -g xbot -m 0700 /home/xbot/.ssh
printf '%s\n' "$XBOT_AUTHORIZED_KEY" > /home/xbot/.ssh/authorized_keys
chown xbot:xbot /home/xbot/.ssh/authorized_keys
chmod 0600 /home/xbot/.ssh/authorized_keys
[ -f /etc/ssh/ssh_host_ed25519_key ] || ssh-keygen -q -t ed25519 -N '' -f /etc/ssh/ssh_host_ed25519_key
(
  while true; do
    /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config || true
    sleep 0.3
  done
) &
exec dockerd-entrypoint.sh dockerd --host=unix:///var/run/docker.sock --group=docker
