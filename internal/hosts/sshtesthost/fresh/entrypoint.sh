#!/bin/sh
# 產生主機金鑰、在背景跑 sshd（被殺掉會自動重啟；/tmp/nosshd 存在時先不啟動，測主機離線用），最後啟動 dockerd。
set -e
ssh-keygen -q -A
(
  while true; do
    if [ ! -f /tmp/nosshd ]; then
      /usr/sbin/sshd -D -e || true
    fi
    sleep 0.3
  done
) &
exec dind dockerd --host=unix:///var/run/docker.sock
