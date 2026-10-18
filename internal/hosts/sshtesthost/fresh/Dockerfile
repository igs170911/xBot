# B12 測試用「剛租來的主機」：Debian 13＋OpenSSH（發行版預設設定）＋Debian 的 docker.io／docker-cli（26 版）。
# 沒有 xbot 帳號、沒有 XBot 的 sshd 設定 —— 測試在裡面以 root 跑一次 B12 的安裝腳本（等同使用者貼那一行指令）。
# Docker 已經預先裝好（裝 Docker 要連 get.docker.com，測試不依賴外網）；dind 腳本處理巢狀 cgroup。
FROM debian:13
RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends openssh-server docker.io docker-cli iproute2 procps ca-certificates >/dev/null \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd
COPY --from=docker:29-dind /usr/local/bin/dind /usr/local/bin/dind
COPY --chmod=755 entrypoint.sh /usr/local/bin/xbot-freshhost
ENTRYPOINT ["/usr/local/bin/xbot-freshhost"]
