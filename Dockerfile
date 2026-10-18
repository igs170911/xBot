# XBot 服務的容器版本（make up）。Bot 的電腦映像在 box/（B01），不是這個。
# 版本要和 mise.toml 一致。

FROM node:26-trixie-slim AS web
WORKDIR /src/web
RUN npm install -g pnpm@12.14.0
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27.2-trixie AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/*.go web/
COPY --from=web /src/web/dist web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -tags webembed -trimpath \
      -ldflags "-s -w -X xbot/internal/version.Version=${VERSION}" -o /out/xbot ./cmd/xbot

FROM debian:13-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=server /out/xbot /usr/local/bin/xbot
# 資料目錄由 compose.yaml 以「和主機相同的絕對路徑」掛進來（XBOT_DATA_DIR），見 compose.yaml 的說明。
ENV XBOT_BIND=0.0.0.0 XBOT_PORT=8780
EXPOSE 8780
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD curl -fsS "http://127.0.0.1:${XBOT_PORT}/healthz" || exit 1
ENTRYPOINT ["xbot"]
CMD ["serve"]
