# XBot 管理手冊

給負責安裝、升級、備份 XBot 的人。一般使用看 [使用手冊](guide/README.md)。

## 安裝出來的東西

| 東西 | 位置 |
|---|---|
| 執行檔 | `~/.local/bin/xbot`（更新時上一版留在 `xbot.prev`） |
| systemd 使用者服務 | `~/.config/systemd/user/xbot.service`（`ExecStart=xbot serve`） |
| 資料目錄 | `$XDG_DATA_HOME/xbot`，通常是 `~/.local/share/xbot`；`XBOT_DATA_DIR` 可改 |
| 備份 | 資料目錄旁的 `xbot-backups/`（例：`~/.local/share/xbot-backups/`） |
| Bot 電腦映像 | Docker 映像 `xbot-box:dev`（`make box`） |
| `make deploy` 的暫存 | 遠端 `~/.local/share/xbot-deploy/` |

服務用一般帳號執行，不需要 sudo。開機自動啟動要開 linger（`loginctl enable-linger`，安裝時會問）。

## 資料目錄

```
~/.local/share/xbot/
  xbot.db               SQLite（WAL）：Bot、對話、任務、技能紀錄、定時任務、用量、設定、稽核、
                        加密過的模型金鑰／應用程式權杖／Tailscale 登入金鑰、推播金鑰
  xbot.db-wal, -shm     SQLite 暫存（備份時不需要，快照會取代）
  model-proxy.key       主金鑰（32 位元組，600）。加密上面那些金鑰、簽 Bot 的通行碼。
                        和 xbot.db 一定要一起備份、一起搬
  access-token          登入碼（600）。session 的簽章金鑰也由它衍生
  ssh/id_ed25519        連遠端主機用的私鑰（有遠端主機時才有）
  hosts/<主機 id>/known_hosts   每台主機的主機金鑰
  tailscale/            內建 Tailscale 的狀態（打開過才有）
  skills/               共用技能庫
  bots/<Bot id>/ipc/    Bot 連回 XBot 的 Unix socket（product.sock），唯讀掛進 Bot 的電腦
```

**不在資料目錄裡的**：每個 Bot 電腦的資料是 Docker volume，在跑那台電腦的主機上：

| Volume | 掛在 Bot 電腦的 | 內容 |
|---|---|---|
| `xbot-box-<Bot id>-home` | `/home/box` | 家目錄、瀏覽器設定與登入 |
| `xbot-box-<Bot id>-workspace` | `/workspace` | 工作資料夾（成果、上傳） |
| `xbot-box-<Bot id>-hermes` | `/hermes` | Hermes 的設定、技能、記憶、紀錄 |
| `xbot-box-<Bot id>-cred` | `/opt/xbot/cred` | 你同意後的訂閱登入憑證 |

每台 Docker 主機上另外還有：網路 `xbot-box`（預設 172.30.0.0/16，撞到就換）、守門容器 `xbot-netguard` 和 volume `xbot-netguard-state`、`xbot-netguard-ready`；開過「從家裡出去」的主機有 `xbot-egress` 容器和 `xbot-egress-state` volume。所有 Bot 電腦的容器名稱是 `xbot-box-<Bot id>`，標籤 `xbot.managed=true`、`xbot.bot=<Bot id>`。

遠端主機的資料目錄是 `/var/lib/xbot`（`xbot` 帳號擁有）。

## 埠號

| 埠 | 誰用 | 聽哪裡 |
|---|---|---|
| 8780 | 安裝版服務（網頁、API、MCP、Webhook、模型代理給 Bot） | 預設 `0.0.0.0`（IPv4 與 IPv6） |
| 443 | 內建 Tailscale 的 `https://xbot.<tailnet>.ts.net`（打開時） | 只在 tailnet 上；Funnel 打開時網際網路也連得到 |
| 隨機 | 每台 Bot 電腦的畫面（容器內 6080）和 Hermes（容器內 8642） | 只在那台主機的 `127.0.0.1` |
| 8782、5181 | `make dev` 的 API 與前端 | 開發用 |
| 8799 | `make e2e`（`XBOT_E2E_PORT` 可改） | 測試用 |
| 8798 | `xbot dev fake-model` 假模型 | 測試用 |

遠端主機不用開任何公開埠，只要 XBot 連得到它的 SSH。

## 環境變數

### 怎麼設給服務

服務檔只寫了 `XBOT_DATA_DIR`、`XBOT_BIND`、`XBOT_PORT`，而且 `make install`、`make update` 每次都會**用當下的環境變數重寫**服務檔。所以：

- 這三個值：安裝和更新時都帶一樣的值（例：`XBOT_BIND=127.0.0.1 make update`）。
- 其他變數，或想一勞永逸：用覆寫檔，更新不會動到它：

  ```bash
  systemctl --user edit xbot
  ```

  寫入：

  ```ini
  [Service]
  Environment=XBOT_BIND=127.0.0.1
  Environment=XBOT_PUSH_SUBJECT=mailto:you@example.com
  ```

  存檔後 `make restart`。覆寫檔的值比服務檔後面套用，所以會蓋過去。

### 服務（`xbot serve`）

| 變數 | 預設 | 說明 |
|---|---|---|
| `XBOT_BIND` | `0.0.0.0` | 聽哪個位址。`127.0.0.1` 只限本機 |
| `XBOT_PORT` | `8780` | HTTP 埠 |
| `XBOT_DATA_DIR` | `$XDG_DATA_HOME/xbot` 或 `~/.local/share/xbot` | 資料目錄 |
| `XBOT_LOCAL_NOAUTH` | `1` | 從這台電腦（`localhost`、沒經過代理）來的請求不用登入。`0` 關掉 |
| `XBOT_ACCESS_TOKEN` | （空白，用 `access-token` 檔） | 直接指定登入碼。本機 `xbot` 指令列也會用它 |
| `XBOT_PUBLIC_URL` | （空白） | 反向代理的對外網址，例 `https://xbot.example.com`（不能有路徑）。設了就打開「反向代理外部入口」 |
| `XBOT_TRUSTED_PROXIES` | `127.0.0.1`、`::1` | 可以帶 `X-Forwarded-For` 的反向代理位址，逗號分隔的 IP 或 CIDR |
| `XBOT_MCP_LOCAL_OWNER` | 關 | `1`：從這台電腦連 `/mcp` 不帶權杖時當成你本人。這台電腦上任何程式都能拿到完整權限，只建議開發時用 |
| `XBOT_BOX_IMAGE` | `xbot-box:dev` | Bot 電腦映像 |
| `XBOT_NETGUARD` | 開 | `off` 關掉 Bot 電腦的網路隔離（電腦改接 Docker 預設網路，連得到主機與區網）。不建議 |
| `XBOT_PUSH_SUBJECT` | `mailto:xbot@localhost` | 推播的聯絡方式（VAPID subject）。iPhone 推不到時改成你真的 email 或 https 網址 |
| `XBOT_IMPORT_PATHS` | （自動找） | 匯入舊資料要找的資料夾，`:` 分隔；`off` 不找 |
| `LAZYHERMES_DATA_DIR`、`OMB_DATA_DIR` | — | 匯入時額外去找的 LazyBoy、OpenMausBot 資料夾 |
| `XBOT_DEV` | 關 | `1`：開發模式（前端由 Vite 提供） |
| `XBOT_FAKE_NOW` | — | 測試用的假「現在」（RFC 3339），只在 `XBOT_DEV=1` 時有效 |

### 指令列（`xbot …`）

| 變數 | 預設 | 說明 |
|---|---|---|
| `XBOT_URL` | 依 `XBOT_BIND`／`XBOT_PORT`，通常 `http://localhost:8780` | 要呼叫的服務 |
| `XBOT_TOKEN` | （讀本機登入碼，以你本人身分） | 代理權杖。設了就以那個代理的身分呼叫 |
| `XBOT_DEPLOY_PORT` | `8780` | `xbot deploy` 遠端服務的埠（`make deploy PORT=…`） |
| `XBOT_DEV_BASE_URL`、`XBOT_DEV_MODEL`、`XBOT_DEV_API_KEY` | — | `xbot dev` 開發指令用 |

### make 參數

| 參數 | 用在 | 說明 |
|---|---|---|
| `YES=1` | setup、install、update、uninstall、host-add、deploy | 不詢問直接做 |
| `WITH_TAILSCALE=1` | setup | 也裝 Tailscale |
| `SKIP_BROWSERS=1` | setup | 不下載測試用瀏覽器 |
| `INCLUDE_LOGINS=1`、`OUT=…` | backup | 見下面「備份」 |
| `FILE=…` | restore | 備份檔 |
| `PURGE=1` | uninstall | 連資料一起刪（會再確認） |
| `HOST=…`、`SSH_OPTS=…` | host-add、host-check、deploy | SSH 目的地與額外參數 |
| `NAME=`、`MAX=`、`ADDRESS=`、`NO_TAILSCALE=1` | host-add | 主機名稱、同時開機上限、XBot 之後連它用的位址、不裝 Tailscale |
| `UPDATE_IMAGE=1` | host-check | 順便傳新映像 |
| `PORT=` | deploy | 遠端服務埠 |
| `BOX_IMAGE=`、`DEBIAN_MIRROR=` | box | 映像名稱、較快的 Debian 鏡像站 |

### docker compose（`make up`）

`XBOT_HOST_BIND`（預設 `0.0.0.0`）、`XBOT_PORT`（預設 8780）。容器裡固定 `XBOT_DATA_DIR=/data`。這個方式目前只適合看網頁，見 [安裝與更新](guide/01-install.md#另一種方式docker-compose)。

### 內部使用（不用設）

Bot 電腦裡由 XBot 設定：`XBOT_UID`、`XBOT_GID`、`XBOT_WORKER_KEY`、`XBOT_PRODUCT_SOCK`、`XBOT_MODEL_BASE_URL`、`XBOT_MODEL_TOKEN`、`XBOT_MCP_PATH`、`XBOT_SCREEN_GEOMETRY`、`XBOT_NETGUARD_*`。測試用：`XBOT_E2E_*`、`XBOT_TEST_*`、`XBOT_B11_*`、`XBOT_NETGUARD_TEST_LOCK`。

## 資料庫與 migration

- SQLite，單一檔案 `xbot.db`，WAL 模式，純 Go driver（不用 C 編譯器）。
- migration 包在執行檔裡（`internal/store/migrations/`），**服務啟動時自動套用**，每個在自己的交易裡，已套用的跳過。紀錄在 `schema_migrations` 表。
- 資料庫比執行檔新時（例如換回舊版），服務會拒絕啟動：「資料庫的版本比這個 XBot 新，請更新 XBot 後再開」。

## 備份

```bash
make backup                          # → ~/.local/share/xbot-backups/xbot-backup-<日期時間>.tar.gz
make backup INCLUDE_LOGINS=1 OUT=/mnt/usb/xbot.tar.gz
make restore FILE=…                  # 服務會先停、還原後再啟動；原本的資料改名保留
```

- 資料庫用 SQLite 的 `VACUUM INTO` 做快照，服務執行中也能備份。
- 備份檔是整個資料目錄（扣掉 SQLite 暫存）。裡面有 `model-proxy.key`、登入碼、SSH 私鑰：**當成密碼一樣保管**。
- `make update` 每次更新前都會自動備份一次。

**Bot 電腦的資料要另外備份**（上面的 volume）。目前 `make backup` 不含它們。手動備份一個 Bot：

```bash
id=<Bot id>
for v in home workspace hermes; do
  docker run --rm -v xbot-box-$id-$v:/src:ro -v "$PWD":/out debian:13-slim \
    tar czf /out/xbot-box-$id-$v.tar.gz -C /src .
done
```

還原時建一樣名字的 volume，反過來解開即可（Bot 的電腦要先休眠）。

建議：每天 `make backup` 一次（例如用 systemd timer 或 cron），備份檔複製到別台機器。

## 升級

```bash
make update
```

1. `git pull --ff-only`（有沒提交的修改就略過）。
2. `make build`。
3. `xbot backup`（自動備份）。
4. 換新執行檔、重寫服務檔、重啟、等健康檢查（30 秒）。
5. 新版起不來：自動換回 `xbot.prev` 並重啟。

注意：

- 第 4 步用當下的環境變數重寫服務檔，見上面「怎麼設給服務」。
- 新版**已經套用了 migration** 才失敗時，舊版會因為資料庫比較新而起不來。這時用第 3 步的備份還原：`make restore FILE=<最新的備份>`，再裝回舊版。
- `make update` 不會重建 Bot 電腦映像。版本說明提到映像有變時：`make box`，再 `xbot computers update`（逐台把電腦換成新映像，資料保留，正在工作的跳過）。

部署到另一台：`make deploy HOST=…`（見 [安裝與更新](guide/01-install.md#部署到另一台機器)）。`make deploy` 不傳 Bot 電腦映像。

## 紀錄

- 服務：`make logs`（`journalctl --user -u xbot.service -f`）。結構化文字紀錄，金鑰已遮蔽。
- Bot 電腦：`docker logs xbot-box-<Bot id>`。
- 網路防護：`docker logs xbot-netguard`。
- 誰做了什麼：`xbot audit list`（稽核紀錄存在資料庫）。

## 資源與限制

| 項目 | 預設 |
|---|---|
| 每台 Bot 電腦 | 2 CPU、3 GB 記憶體、shm 512 MB、4096 個行程 |
| 可寫的暫存 | `/tmp` 1 GB、`/run` 64 MB、`/var/tmp` 256 MB（tmpfs，算在記憶體裡） |
| 閒置休眠 | 30 分鐘 |
| 開機等待 | 75 秒 |
| 每台遠端主機同時開機 | 3 台（可設 1–64） |
| 遠端主機最少可用磁碟 | 10 GB |
| Bot 總數 | 50 |
| 模型請求大小 | 64 MB；供應商 5 分鐘沒送資料算逾時 |
| 上傳 | 單檔 50 MB（圖片 20 MB）、一次 10 個、合計 200 MB |

所有固定數字都在 `internal/limits`，對照表見 [limits.md](limits.md)。

粗估容量：同時開著 N 台電腦，需要 N × 3 GB 記憶體加上系統本身。每個 Bot 的 volume 大小看它做什麼，瀏覽器資料常常是大宗。

## 對外開放前的安全檢查

### 區網（預設）

- [ ] 了解：區網裡任何人知道登入碼就能操作 XBot。登入碼用 `make token` 看，不要貼在公開的地方。
- [ ] 區網的 `http://` 沒有加密。不信任的網路（公司、咖啡店）不要用，或改 `XBOT_BIND=127.0.0.1` 只走 Tailscale。
- [ ] 這台電腦有別人共用時，設 `XBOT_LOCAL_NOAUTH=0`。
- [ ] 這台的防火牆只對信任的網段開 8780。
- [ ] 跑 XBot 的帳號在 docker 群組＝這台的 root。這個帳號要有好密碼，不要給別人登入。
- [ ] 設全部合計的每月花費上限（預設沒有上限）。

### Tailscale（建議的遠端方式）

- [ ] 用內建 Tailscale（「設定 → 進階 → 網路與 Tailscale」），不要用外面的 `tailscale serve`（那樣來的請求要靠反向代理規則判斷）。
- [ ] Tailscale 後台打開 MagicDNS、HTTPS Certificates。
- [ ] 用 Tailscale 的存取規則（ACL）限制誰能連 XBot 這台。tailnet 裡的人一樣要登入碼，但多一層比較好。
- [ ] 用 Tailscale 時可以考慮 `XBOT_BIND=127.0.0.1`，只留 tailnet 這條路（內建 Tailscale 不受 `XBOT_BIND` 影響）。

### 外部入口：Tailscale Funnel

- [ ] 真的有 tailnet 外的代理或 Webhook 要用才開。打開要你本人允許。
- [ ] 能限制來源 IP 就設白名單（`xbot network funnel_set --enabled --allow_ips …`）。
- [ ] Tailscale ACL 的 `nodeAttrs` 只給這台 `funnel`。
- [ ] 外部代理的權杖：最小權限、限定 Bot、設失效天數。
- [ ] 了解只開放 `/mcp`、`/api/v1/agents.pair`、`/hooks/<id>`，速率上限每 IP 每分鐘 120、每代理每分鐘 20。

### 外部入口：自己的反向代理

- [ ] 設 `XBOT_PUBLIC_URL=https://你的網域`，HTTPS 由反向代理負責。
- [ ] 反向代理**一定要帶 `X-Forwarded-For`**。不帶的話，同一台機器上的反向代理送來的請求看起來像「這台電腦的你」。
- [ ] 反向代理不在同一台時，設 `XBOT_TRUSTED_PROXIES`。
- [ ] 只轉發上面那三條路徑更好（XBot 自己也會擋其他路徑）。
- [ ] 設了反向代理模式後，經外部 `tailscale serve` 進來的請求也會被當成外部；tailnet 內請改用內建 Tailscale。

### 從家裡出去（exit node）

- [ ] Tailscale ACL 只允許 `tag:xbot-egress` 連 `autogroup:internet`，否則 Bot 經家裡那台連得到你家的區網。

### 遠端主機

- [ ] 整台專給 XBot。`xbot` 帳號在 docker 群組＝root。
- [ ] 資料目錄的 `ssh/id_ed25519` 能登入所有主機，保護好，也注意備份檔。
- [ ] 主機的 SSH 只對 tailnet 或你的 IP 開放。
