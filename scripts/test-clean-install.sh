#!/usr/bin/env bash
# 驗證「全新的機器也裝得起來」：在乾淨的 Linux 容器裡跑 make setup YES=1 && make check。
# 用法：scripts/test-clean-install.sh [映像，預設 debian:13]
set -euo pipefail
cd "$(dirname "$0")/.."
image="${1:-debian:13}"

case "$image" in
  # 只先裝 make，其餘（curl、git、libatomic…）要由 make setup 自己補齊。
  debian*|ubuntu*) prep='apt-get update -qq && apt-get install -y -qq make >/dev/null' ;;
  archlinux*)      prep='pacman -Sy --noconfirm --needed make >/dev/null' ;;
  fedora*)         prep='dnf install -y -q make' ;;
  *) echo "不支援的映像：$image"; exit 2 ;;
esac

echo "▶ 在 $image 裡測試全新安裝（只帶入 git 追蹤或未忽略的檔案）"
git ls-files -co --exclude-standard -z | tar --null -T - -czf - | \
  docker run --rm -i "$image" bash -c "
    set -e
    $prep
    mkdir -p /src && cd /src && tar -xzf -
    make setup YES=1 ${SKIP_BROWSERS:+SKIP_BROWSERS=1}
    make check
  "
echo "✅ $image：make setup && make check 通過"
