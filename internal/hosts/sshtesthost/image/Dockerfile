# B11 測試用「遠端主機」：自己的 Docker daemon（dind）＋ sshd，只放 xbot 帳號。
# 只給 go test -tags docker 用，不是正式的主機安裝方式（那是 B12 的一行安裝指令）。
FROM docker:29-dind
RUN apk add --no-cache openssh-server curl iproute2 iproute2-tc \
 && (addgroup -S docker 2>/dev/null || true) \
 && adduser -D -s /bin/sh xbot \
 && addgroup xbot docker \
 && sed -i 's/^xbot:!/xbot:*/' /etc/shadow \
 && install -d -o xbot -g xbot -m 0755 /var/lib/xbot
COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/xbot-testhost
ENTRYPOINT ["/usr/local/bin/xbot-testhost"]
