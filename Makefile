# XBot：所有操作都從 make 開始。執行 `make` 或 `make help` 看全部指令。
SHELL := /bin/bash
.DEFAULT_GOAL := help

# 用 mise 鎖定的工具版本（make setup 會裝好 mise）。
MISE := $(shell command -v mise 2>/dev/null || ls $(HOME)/.local/bin/mise 2>/dev/null)
RUN  := $(if $(MISE),$(MISE) exec --,)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X xbot/internal/version.Version=$(VERSION)
GOBUILD := CGO_ENABLED=0 $(RUN) go build -tags webembed -trimpath -ldflags "$(LDFLAGS)"
ARCH    := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
XBOT    := bin/xbot
, := ,
# 管理已安裝服務時，優先用裝好的執行檔；還沒安裝就用剛建出來的。
XBOT_INSTALLED = $(shell command -v $(HOME)/.local/bin/xbot 2>/dev/null || echo $(XBOT))

.PHONY: help doctor setup deps dev check fmt gen test test-docker e2e e2e-docker box box-latest box-test build web-build install start stop restart \
        status logs token up down update backup restore clean uninstall

help: ## 列出所有指令
	@echo "XBot 指令（完整說明見 docs/PLAN.md §2）："
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-12s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "常用參數：YES=1 不詢問直接執行｜WITH_TAILSCALE=1 也裝 Tailscale｜SKIP_BROWSERS=1 不下載測試瀏覽器"
	@echo "　　　　　PURGE=1 移除時連資料一起刪｜INCLUDE_LOGINS=1 備份含登入資料｜FILE=… 還原用的備份檔"

doctor: ## 檢查缺什麼，並告訴你怎麼修
	@scripts/bootstrap.sh doctor

setup: ## 自動安裝缺少的工具與套件（需要 sudo 的會先問）
	@scripts/bootstrap.sh setup

deps: ## 只安裝專案相依套件（Go 與前端）
	$(RUN) go mod download
	cd web && $(RUN) pnpm install --frozen-lockfile

dev: ## 開發模式：後端熱重載＋前端 Vite
	@RUN="$(RUN)" scripts/dev.sh

fmt: ## 自動排版 Go 程式
	$(RUN) gofmt -w cmd internal web/*.go

gen: ## 由操作登錄表產生前端型別 web/src/api/generated.ts（改了操作後執行並提交）
	$(RUN) go run ./cmd/xbot gen ts

check: ## 全部檢查（排版、vet、生成檔是否最新、staticcheck、測試、前端型別、lint、單元測試）
	@files=$$($(RUN) gofmt -l cmd internal web/*.go); \
	  if [ -n "$$files" ]; then echo "❌ 這些檔案沒有排版，請執行 make fmt："; echo "$$files"; exit 1; fi
	$(RUN) go vet ./...
	@$(RUN) go run ./cmd/xbot gen ts --check || { echo "❌ 前端型別過期，請執行 make gen 並提交"; exit 1; }
	$(RUN) staticcheck ./...
	$(RUN) go test ./...
	cd web && $(RUN) pnpm typecheck && $(RUN) pnpm lint && $(RUN) pnpm test
	@echo "✅ make check 全部通過"

test: ## 只跑單元測試（Go＋前端）
	$(RUN) go test ./...
	cd web && $(RUN) pnpm test

test-docker: ## Docker 整合測試（真的開 Bot 電腦，含對話端對端與 SSH 遠端主機；沒有 Docker 或映像時略過，先 make box）
	BOX_IMAGE=$(BOX_IMAGE) $(RUN) go test -tags docker -count=1 -timeout 30m ./internal/box/... ./internal/runtime/... ./internal/catalog/... ./internal/hosts/...

test-race: ## 用 race detector 重複跑 Go 測試，抓偶發失敗（合併前建議跑）
	$(RUN) go test -race -count=3 ./...

e2e: ## Playwright 端對端測試（會先 make build）
	@$(MAKE) --no-print-directory build
	cd web && $(RUN) pnpm e2e

e2e-docker: ## 真的開 Bot 電腦的端對端測試（網頁建 Bot → 對話 → Hermes 回覆；要 Docker 與 make box）
	@docker info >/dev/null 2>&1 || { echo "❌ 無法連線到 Docker（make doctor 會告訴你怎麼修）"; exit 1; }
	@docker image inspect $(BOX_IMAGE) >/dev/null 2>&1 || { echo "❌ 沒有映像 $(BOX_IMAGE)，先執行 make box"; exit 1; }
	@$(MAKE) --no-print-directory build
	cd web && XBOT_E2E_DOCKER=1 BOX_IMAGE=$(BOX_IMAGE) $(RUN) pnpm e2e:docker

# Bot 電腦映像：box/Dockerfile（桌面＋Chromium＋noVNC＋Hermes，Hermes 版本固定在 box/HERMES_VERSION）。
# DEBIAN_MIRROR=<網址> 改用較快的 Debian 鏡像站（當成建置密鑰傳入，不影響快取）。
BOX_IMAGE ?= xbot-box:dev
box: ## 建 Bot 電腦映像 xbot-box:dev（沿用快取與固定版本）
	@docker info >/dev/null 2>&1 || { echo "❌ 無法連線到 Docker。請先啟動 Docker，並確認目前的使用者可以執行 docker（make doctor 會告訴你怎麼修）"; exit 1; }
	DOCKER_BUILDKIT=1 docker build $(if $(DEBIAN_MIRROR),--secret id=debian_mirror$(,)env=DEBIAN_MIRROR,) $(BOX_ARGS) -t $(BOX_IMAGE) box
	@echo "✅ 已建好 $(BOX_IMAGE)（Hermes $$(cut -c1-12 box/HERMES_VERSION)）。驗證：make box-test"

box-latest: ## 同 make box，但 grok 等工具與 Python 資料套件更新到最新版
	@$(MAKE) --no-print-directory box BOX_ARGS="--build-arg BOX_REFRESH=$$(date +%Y%m%d%H%M%S) --build-arg GROK_CLI_VERSION=$${GROK_CLI_VERSION:-latest}"

box-test: ## 開一台 Bot 電腦，確認桌面、Chromium、noVNC、Hermes 都起得來
	@BOX_IMAGE=$(BOX_IMAGE) scripts/test-box.sh

# ── 建置 ──────────────────────────────────────────────

web-build:
	cd web && $(RUN) pnpm build

build: web-build ## 建出單一執行檔（前端內嵌），amd64 與 arm64
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/xbot-linux-amd64 ./cmd/xbot
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/xbot-linux-arm64 ./cmd/xbot
	@cp bin/xbot-linux-$(ARCH) $(XBOT)
	@echo "✅ $(XBOT)（$(VERSION)），以及 bin/xbot-linux-amd64、bin/xbot-linux-arm64"

# ── 安裝到這台機器（systemd 使用者服務）────────────────

install: ## 一鍵安裝：補齊工具 → 建置 → 建 Bot 電腦映像 → 設成開機自動啟動的服務
	@scripts/bootstrap.sh setup
	@$(MAKE) --no-print-directory build
	@$(MAKE) --no-print-directory box
	@YES=$(YES) $(XBOT) service install --bin $(XBOT)

start: ## 啟動服務
	@$(XBOT_INSTALLED) service start
stop: ## 停止服務
	@$(XBOT_INSTALLED) service stop
restart: ## 重新啟動服務
	@$(XBOT_INSTALLED) service restart
status: ## 顯示服務狀態
	@$(XBOT_INSTALLED) service status
logs: ## 即時查看服務紀錄（Ctrl+C 離開）
	@$(XBOT_INSTALLED) service logs
token: ## 顯示其他裝置用的登入碼
	@$(XBOT_INSTALLED) token

update: ## 更新：拉新版 → 建置 → 備份 → 換新版重啟（失敗自動換回上一版）
	@if [ -d .git ] && [ -z "$$(git status --porcelain)" ]; then git pull --ff-only; \
	  else echo "⚠️  有未提交的修改或不是 git 專案，略過 git pull，直接用目前的程式碼"; fi
	@$(MAKE) --no-print-directory build
	@$(XBOT_INSTALLED) backup
	@YES=$(YES) $(XBOT) service update --bin $(XBOT)

backup: ## 備份資料（INCLUDE_LOGINS=1 連登入資料一起）
	@$(XBOT_INSTALLED) backup $(if $(INCLUDE_LOGINS),--include-logins,) $(if $(OUT),--out $(OUT),)

restore: ## 從備份還原：make restore FILE=備份檔
	@test -n "$(FILE)" || { echo "用法：make restore FILE=備份檔"; exit 1; }
	@$(XBOT_INSTALLED) restore "$(FILE)"

uninstall: ## 移除服務與執行檔（資料保留；PURGE=1 連資料一起刪，會再確認）
	@YES=$(YES) $(XBOT_INSTALLED) service uninstall $(if $(PURGE),--purge,)

clean: ## 清掉建置產物（不動資料）
	rm -rf bin tmp web/dist web/e2e-results

# ── docker compose 版本（和 make install 二選一）──────────

up: ## 用 docker compose 啟動整套服務
	XBOT_VERSION=$(VERSION) docker compose up -d --build
	@echo "✅ 已啟動：http://127.0.0.1:$${XBOT_PORT:-8780}（make down 停止）"
down: ## 停止 docker compose 版本
	docker compose down
