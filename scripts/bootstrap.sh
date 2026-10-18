#!/usr/bin/env bash
# make setup／make doctor 的入口。
#
# xbot 的檢查與安裝邏輯是 Go 寫的，所以沒有 Go 時要先在這裡把 mise 和 Go 裝好；
# 有 Go 之後就交給 `go run ./cmd/xbot setup|doctor`。
#
# 用法：scripts/bootstrap.sh setup|doctor [參數]
# 環境變數：YES=1 不詢問直接安裝；WITH_TAILSCALE=1 也裝 Tailscale；SKIP_BROWSERS=1 不下載測試瀏覽器。
set -euo pipefail

cd "$(dirname "$0")/.."
action="${1:-doctor}"
shift || true

say() { printf '%s\n' "$*"; }
ok() { printf '✅ %s\n' "$*"; }
bad() { printf '❌ %s\n' "$*"; }

# 系統基本套件：下載工具，以及 Node 需要的 libatomic（乾淨的 Debian／Fedora 預設沒有）。
ensure_base() {
  local pm="" pkgs=()
  if command -v apt-get >/dev/null 2>&1; then pm=apt
  elif command -v pacman >/dev/null 2>&1; then pm=pacman
  elif command -v dnf >/dev/null 2>&1; then pm=dnf
  fi
  for b in curl git tar; do
    command -v "$b" >/dev/null 2>&1 || pkgs+=("$b")
  done
  command -v xz >/dev/null 2>&1 || case $pm in apt) pkgs+=(xz-utils) ;; *) pkgs+=(xz) ;; esac
  # 任何一個位置有就算有（ls 多個樣式時只要一個不存在就回錯，所以逐一檢查）。
  local atomic=""
  for f in /usr/lib*/libatomic.so.1 /usr/lib*/*/libatomic.so.1 /lib*/libatomic.so.1 /lib*/*/libatomic.so.1; do
    [ -e "$f" ] && { atomic=$f; break; }
  done
  if [ -z "$atomic" ]; then
    case $pm in apt) pkgs+=(libatomic1) ;; dnf) pkgs+=(libatomic) ;; pacman) pkgs+=(gcc-libs) ;; esac
  fi
  if [ "$pm" = apt ] && [ ! -e /etc/ssl/certs/ca-certificates.crt ]; then pkgs+=(ca-certificates); fi
  ((${#pkgs[@]})) || return 0

  local sudo=""
  [ "$(id -u)" -eq 0 ] || sudo="sudo "
  local cmd
  case $pm in
    apt) cmd="${sudo}apt-get update && ${sudo}apt-get install -y ${pkgs[*]}" ;;
    pacman) cmd="${sudo}pacman -S --needed --noconfirm ${pkgs[*]}" ;;
    dnf) cmd="${sudo}dnf install -y ${pkgs[*]}" ;;
    *) bad "缺少系統套件：${pkgs[*]}，請用系統的套件管理員安裝"; return 1 ;;
  esac
  say "缺少系統套件：${pkgs[*]}"
  say "   要執行：$cmd"
  if confirm "   要現在安裝嗎？"; then
    bash -c "$cmd"
    ok "系統套件已安裝"
  else
    bad "沒有這些套件無法繼續。可以自己執行上面那行，或用 make setup YES=1"
    return 1
  fi
}

find_mise() {
  if command -v mise >/dev/null 2>&1; then command -v mise; return; fi
  for p in "$HOME/.local/bin/mise" "$HOME/.local/share/mise/bin/mise"; do
    [ -x "$p" ] && { echo "$p"; return; }
  done
  return 1
}

confirm() {
  [ "${YES:-}" = "1" ] && return 0
  [ -t 0 ] || return 1
  read -r -p "$1 [y/N] " a
  [[ "$a" =~ ^[Yy] ]]
}

case "$action" in
doctor)
  if mise=$(find_mise) && "$mise" exec -- go version >/dev/null 2>&1; then
    export PATH="$(dirname "$mise"):$PATH"
    exec "$mise" exec -- go run ./cmd/xbot doctor "$@"
  elif command -v go >/dev/null 2>&1; then
    exec go run ./cmd/xbot doctor "$@"
  fi
  bad "Go 沒有安裝（xbot 的檢查工具需要 Go）"
  say "   ↳ 修正：make setup"
  find_mise >/dev/null || { bad "mise 沒有安裝"; say "   ↳ 修正：make setup（會自動安裝 mise）"; }
  exit 1
  ;;
setup)
  ensure_base
  if ! mise=$(find_mise); then
    say "需要先安裝 mise（管理 Go、Node、pnpm 版本的工具，裝在你的家目錄，不需要 sudo）"
    say "   要執行：curl -fsSL https://mise.run | sh"
    if confirm "   要現在安裝嗎？"; then
      curl -fsSL https://mise.run | sh
      mise=$(find_mise) || { bad "mise 安裝後找不到，請重新開一個終端機再試"; exit 1; }
    else
      bad "沒有 mise 無法繼續。可以自己執行上面那行，或用 make setup YES=1"
      exit 1
    fi
  fi
  ok "mise：$("$mise" --version 2>/dev/null | head -1)"
  "$mise" trust --quiet mise.toml
  say "安裝 Go（第一次會花一點時間）…"
  "$mise" install go
  ok "$("$mise" exec -- go version)"
  case ":$PATH:" in
    *":$HOME/.local/share/mise/shims:"*) ;;
    *) say "提示：把 mise 加進 shell 會比較方便：echo 'eval \"\$($mise activate bash)\"' >> ~/.bashrc" ;;
  esac
  export PATH="$(dirname "$mise"):$PATH"
  exec "$mise" exec -- go run ./cmd/xbot setup "$@"
  ;;
*)
  say "用法：$0 setup|doctor"
  exit 2
  ;;
esac
