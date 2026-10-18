#!/bin/sh
# XBot：把這台 Linux 主機準備成 Bot 電腦主機（B12，PRD §16 CL-2）。用 root 執行，可以重複執行。
#   1. 安裝 Docker（已經有 26 版以上就略過）
#   2. 安裝 Tailscale 並加入你的 tailnet（已經有就略過；--no-tailscale 不裝）
#   3. 建 __XBOT_USER__ 帳號（在 docker 群組），只放 XBot 的 SSH 公鑰，不設密碼
#   4. 設定 sshd：允許 XBot 用的轉發（/etc/ssh/sshd_config.d/10-xbot.conf）
#   5. 建 __XBOT_DATA_DIR__（Bot 的資料放這裡）
# 不開任何公開埠；電腦映像在 XBot 的第 2 步經 SSH 傳過來。
set -eu

XBOT_USER='__XBOT_USER__'
XBOT_KEY='__XBOT_AUTHORIZED_KEY__'
XBOT_DATA='__XBOT_DATA_DIR__'
DOCKER_MIN=__XBOT_DOCKER_MIN__
TAILSCALE=1
for a in "$@"; do
	case "$a" in
	--no-tailscale) TAILSCALE=0 ;;
	--tailscale) TAILSCALE=1 ;;
	*) printf '不認識的參數：%s\n' "$a" >&2; exit 2 ;;
	esac
done

say() { printf '%s\n' "$*"; }
ok() { printf '✅ %s\n' "$*"; }
step() { printf '\n▶ %s\n' "$*"; }
die() { printf '❌ %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[ "$(id -u)" = 0 ] || die "要用 root 執行：在指令的 sh 前面加 sudo"
[ "$(uname -s)" = Linux ] || die "只支援 Linux 主機"
case "$(uname -m)" in
x86_64 | amd64 | aarch64 | arm64) ;;
*) die "不支援這種 CPU 架構：$(uname -m)。Bot 電腦映像只有 amd64 與 arm64" ;;
esac

install_pkg() {
	if have apt-get; then
		DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null
	elif have dnf; then
		dnf install -y -q "$@"
	elif have yum; then
		yum install -y -q "$@"
	elif have apk; then
		apk add --no-cache -q "$@"
	elif have pacman; then
		pacman -Sy --noconfirm --needed "$@" >/dev/null
	else
		die "找不到套件管理工具，請先手動安裝：$*"
	fi
}

# ── 1. Docker ─────────────────────────────────────────
step "1/5 Docker"
docker_version() { docker version --format '{{.Server.Version}}' 2>/dev/null || true; }
if ! have docker; then
	say "安裝 Docker（官方安裝程式 get.docker.com）…"
	have curl || install_pkg curl ca-certificates
	curl -fsSL https://get.docker.com | sh
fi
if [ -z "$(docker_version)" ]; then
	if have systemctl; then systemctl enable --now docker >/dev/null 2>&1 || true; fi
	if have service; then service docker start >/dev/null 2>&1 || true; fi
	i=0
	while [ -z "$(docker_version)" ] && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
fi
v=$(docker_version)
[ -n "$v" ] || die "Docker 裝好了但沒有在執行。請執行 systemctl start docker 再貼一次這行指令"
major=${v%%.*}
case "$major" in '' | *[!0-9]*) major=0 ;; esac
[ "$major" -ge "$DOCKER_MIN" ] || die "Docker 版本太舊（$v），需要 $DOCKER_MIN 以上。更新方法：curl -fsSL https://get.docker.com | sh"
ok "Docker $v"

# ── 2. Tailscale ──────────────────────────────────────
step "2/5 Tailscale"
if [ "$TAILSCALE" = 1 ]; then
	if ! have tailscale; then
		say "安裝 Tailscale…"
		have curl || install_pkg curl ca-certificates
		curl -fsSL https://tailscale.com/install.sh | sh
	fi
	if tailscale status >/dev/null 2>&1; then
		ok "已在你的 tailnet：$(tailscale ip -4 2>/dev/null | head -n1)"
	else
		say "加入你的 tailnet：打開下面印出的網址，用和 XBot 那台同一個帳號登入。"
		tailscale up || die "Tailscale 沒有登入成功。登入後再貼一次這行指令（已完成的步驟會略過）"
		ok "已加入 tailnet：$(tailscale ip -4 2>/dev/null | head -n1)"
	fi
else
	say "略過（--no-tailscale）：XBot 要能直接連到這台的 SSH"
fi

# ── 3. xbot 帳號 ──────────────────────────────────────
step "3/5 $XBOT_USER 帳號"
if ! getent group docker >/dev/null 2>&1; then
	if have groupadd; then groupadd docker; else addgroup -S docker; fi
fi
if ! id "$XBOT_USER" >/dev/null 2>&1; then
	if have useradd; then
		useradd -m -s /bin/sh "$XBOT_USER"
	else
		adduser -D -s /bin/sh "$XBOT_USER"
	fi
fi
if have usermod; then
	usermod -aG docker "$XBOT_USER"
	# 不設密碼、但也不鎖住帳號（鎖住的帳號在部分 sshd 設定下不能用金鑰登入）
	usermod -p '*' "$XBOT_USER"
else
	addgroup "$XBOT_USER" docker 2>/dev/null || true
	sed -i "s/^$XBOT_USER:!:/$XBOT_USER:*:/" /etc/shadow
fi
home=$(getent passwd "$XBOT_USER" | cut -d: -f6)
group=$(id -gn "$XBOT_USER")
install -d -m 0700 -o "$XBOT_USER" -g "$group" "$home/.ssh"
keys="$home/.ssh/authorized_keys"
touch "$keys"
# 同一把金鑰（不管註解）只留一行
key_body=$(printf '%s' "$XBOT_KEY" | cut -d' ' -f1-2)
if ! grep -qF "$key_body" "$keys"; then
	printf '%s\n' "$XBOT_KEY" >>"$keys"
fi
chown "$XBOT_USER:$group" "$keys"
chmod 0600 "$keys"
ok "$XBOT_USER 帳號（docker 群組）＋ XBot 的公鑰"

# ── 4. sshd ───────────────────────────────────────────
step "4/5 SSH 設定"
if ! have sshd && [ ! -x /usr/sbin/sshd ]; then
	say "安裝 OpenSSH 伺服器…"
	if have apt-get; then install_pkg openssh-server; else install_pkg openssh; fi
	if have systemctl; then systemctl enable --now ssh >/dev/null 2>&1 || systemctl enable --now sshd >/dev/null 2>&1 || true; fi
fi
SSHD=$(command -v sshd || echo /usr/sbin/sshd)
mkdir -p /etc/ssh/sshd_config.d
cat >/etc/ssh/sshd_config.d/10-xbot.conf <<'EOF'
# XBot（B12）：一條 SSH 連線同時帶 Docker API、看畫面的埠轉發、Bot 連回 XBot 的 product.sock。
AllowTcpForwarding yes
AllowStreamLocalForwarding yes
StreamLocalBindUnlink yes
StreamLocalBindMask 0111
EOF
if ! grep -qiE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/' /etc/ssh/sshd_config; then
	sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config
fi
[ -f /etc/ssh/ssh_host_ed25519_key ] || ssh-keygen -q -A
"$SSHD" -t || die "sshd 設定檢查沒過（上面是原因）。/etc/ssh/sshd_config.d/10-xbot.conf 是 XBot 加的"
effective=$("$SSHD" -T -C "user=$XBOT_USER,host=localhost,addr=127.0.0.1" 2>/dev/null | tr 'A-Z' 'a-z')
for want in 'allowtcpforwarding yes' 'allowstreamlocalforwarding yes' 'streamlocalbindunlink yes' 'streamlocalbindmask 0111'; do
	printf '%s\n' "$effective" | grep -qx "$want" || die "sshd 的「$want」沒有生效：/etc/ssh/sshd_config 或其他設定檔把它蓋掉了，請改成和 /etc/ssh/sshd_config.d/10-xbot.conf 一樣"
done
reloaded=0
if have systemctl && systemctl is-active --quiet ssh 2>/dev/null; then systemctl reload ssh && reloaded=1; fi
if [ $reloaded = 0 ] && have systemctl && systemctl is-active --quiet sshd 2>/dev/null; then systemctl reload sshd && reloaded=1; fi
if [ $reloaded = 0 ] && [ -f /run/sshd.pid ]; then kill -HUP "$(cat /run/sshd.pid)" && reloaded=1; fi
if [ $reloaded = 0 ] && [ -f /var/run/sshd.pid ]; then kill -HUP "$(cat /var/run/sshd.pid)" && reloaded=1; fi
if [ $reloaded = 0 ] && have service; then service ssh reload >/dev/null 2>&1 && reloaded=1 || true; fi
[ $reloaded = 1 ] || say "⚠️  找不到執行中的 sshd，請自己重新啟動 SSH 服務讓設定生效"
ok "sshd 允許 XBot 的轉發"

# ── 5. 資料目錄 ───────────────────────────────────────
step "5/5 資料目錄"
install -d -m 0755 -o "$XBOT_USER" -g "$group" "$XBOT_DATA"
ok "$XBOT_DATA"

say ""
say "🎉 這台主機準備好了。回到 XBot，在第 2 步填這台的位址："
if have tailscale && tailscale status >/dev/null 2>&1; then
	name=$(tailscale status --self --json 2>/dev/null | sed -n 's/.*"DNSName": *"\([^"]*\)\.".*/\1/p' | head -n1)
	[ -n "$name" ] && say "   $name（Tailscale 名稱）"
	say "   $(tailscale ip -4 2>/dev/null | head -n1)（Tailscale IP）"
else
	for ip in $(hostname -I 2>/dev/null); do say "   $ip"; done
fi
say "建議整台主機專給 XBot：$XBOT_USER 在 docker 群組，等於有這台的最高權限。"
