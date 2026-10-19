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

服務檔一定有 `XBOT_DATA_DIR`、`XBOT_BIND`、`XBOT_PORT`，再加上安裝或更新時你**明確帶了**的服務變數（下表「服務」那些，`XBOT_ACCESS_TOKEN` 除外，因為服務檔誰都讀得到）。

- **第一次安裝**：沒帶的用預設值。例：`XBOT_BIND=127.0.0.1 XBOT_PUSH_SUBJECT=mailto:you@example.com make install`。
- **`make update`、再跑一次 `make install`、`make deploy`**：沿用服務檔裡原本的值，只有這次明確帶的變數才改。例：`XBOT_PORT=8790 make update` 只改埠，其他照舊。有改到的會印出「服務設定 XBOT_PORT：8780 → 8790」。服務檔裡你自己加的其他 `Environment=`（例：`DOCKER_HOST`）也原樣保留。
- `xbot service status`、`make backup`／`restore`／`token` 也讀服務檔裡的資料目錄與埠，所以裝在別的資料目錄時不用每次都帶 `XBOT_DATA_DIR`。
- 想完全自己管：用覆寫檔，更新不會動到它：

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
| `XBOT_LOCAL_NOAUTH` | `1`（設了 `XBOT_PUBLIC_URL` 或 `XBOT_TRUSTED_PROXIES` 時是 `0`） | 從這台電腦（`localhost`、沒經過代理）來的請求不用登入。`0` 關掉；有反向代理時要明確設 `1` 才開 |
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
| `INCLUDE_LOGINS=1`、`NO_COMPUTERS=1`、`OUT=…` | backup | 見下面「備份」 |
| `FILE=…`、`NO_COMPUTERS=1`、`COMPUTERS_ONLY=1`、`BOT=…` | restore | 備份檔；不動電腦資料；只補還原電腦資料（`BOT=` 逗號分隔的 Bot id） |
| `PURGE=1` | uninstall | 連資料一起刪（會再確認） |
| `HOST=…`、`SSH_OPTS=…` | host-add、host-check、deploy | SSH 目的地與額外參數 |
| `NAME=`、`MAX=`、`ADDRESS=`、`NO_TAILSCALE=1` | host-add | 主機名稱、同時開機上限、XBot 之後連它用的位址、不裝 Tailscale |
| `UPDATE_IMAGE=1` | host-check | 順便傳新映像 |
| `PORT=`、`NO_IMAGE=1` | deploy | 遠端服務埠；不傳 Bot 電腦映像 |
| `BOX_IMAGE=`、`DEBIAN_MIRROR=` | box | 映像名稱、較快的 Debian 鏡像站 |

### docker compose（`make up`）

| 變數 | 預設 | 說明 |
|---|---|---|
| `XBOT_HOST_BIND` | `0.0.0.0` | 服務聽哪個位址（容器裡的 `XBOT_BIND`） |
| `XBOT_PORT` | `8780` | HTTP 埠 |
| `XBOT_COMPOSE_DATA` | `~/.local/share/xbot-compose` | 資料目錄（主機上的資料夾） |
| `XBOT_BOX_IMAGE` | `xbot-box:dev` | Bot 電腦映像 |
| `COMPOSE_PROJECT_NAME` | 專案資料夾名稱 | compose 的專案名稱 |

compose 版本和 `make install` 一樣能開 Bot 的電腦，做法上有三點不同：

- 資料目錄是主機上的資料夾，用**同一個絕對路徑**掛進容器（容器裡的 `XBOT_DATA_DIR` 就是 `XBOT_COMPOSE_DATA`）。Bot 電腦的通訊資料夾 `bots/<Bot id>/ipc/` 是由主機的 Docker 掛進電腦的，路徑兩邊要一樣。
- 服務用主機的網路（`network_mode: host`），所以連得到只發布在主機 `127.0.0.1` 的電腦畫面與 Hermes。compose 裡沒有 `ports:`，聽哪裡由 `XBOT_HOST_BIND`、`XBOT_PORT` 決定。
- 服務在容器裡用你的帳號（`id -u`／`id -g`）執行，並加入 docker.sock 的群組；資料檔案屬於你。這些值由 `make up` 算好傳給 compose，請用 `make up`／`make down`，不要直接 `docker compose up`。

`XBOT_COMPOSE_DATA` 的完整路徑別太長：`<資料目錄>/bots/<32 字元 id>/ipc/product.sock` 不能超過 108 個字元（Unix socket 的限制）。

舊版 compose（資料在 `xbot-data` volume、容器裡 `/data`）升上來時，先把資料搬到新的資料夾：

```bash
make down
mkdir -p ~/.local/share/xbot-compose
docker run --rm -v <專案名稱>_xbot-data:/from:ro -v ~/.local/share/xbot-compose:/to debian:13-slim \
  sh -c "cp -a /from/. /to/ && chown -R $(id -u):$(id -g) /to"
make up
```

驗證腳本：`scripts/test-compose.sh`（make up → 建 Bot → 經服務對話 → 休眠後再對話）。

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
make backup NO_COMPUTERS=1           # 只備份資料目錄（快，不含 Bot 電腦的資料）
make restore FILE=…                  # 服務會先停、還原後再啟動；原本的資料改名保留
```

網頁「設定 → 進階 → 備份與更新」的「立即備份」等於 `make backup`（資料目錄＋每個 Bot 電腦的資料，不含瀏覽器登入；`xbot.backup.create`，在背景做），也列出備份資料夾裡最近的備份（`xbot.backup.list`）。還原只能用指令列（要先停服務）。

備份檔裡有：

- **資料目錄**（扣掉 SQLite 暫存）。資料庫用 SQLite 的 `VACUUM INTO` 做快照，服務執行中也能備份。裡面有 `model-proxy.key`、登入碼、SSH 私鑰：**當成密碼一樣保管**。
- **每個 Bot 電腦的資料**：`home`、`workspace`、`hermes` 三個 volume，不管電腦在這台還是遠端主機。XBot 用一個不開機、沒有網路的輔助容器（同一個 Bot 電腦映像）掛上 volume，經 Docker API 串流出來（遠端主機走 XBot 自己的 SSH 連線，和搬家一樣），不落地；檔案的擁有者與權限照原樣。電腦開著也能備份，不會停下它（正在寫的檔案可能不是同一刻的）。
  - 訂閱登入憑證（`cred` volume）**永遠不備份**：每次開機前會依你的同意重新寫入。
  - 瀏覽器登入（`home` 裡的 `chrome-profile`）預設不備份，`INCLUDE_LOGINS=1` 才包含。
  - 主機不在線上（等 20 秒連不上）或讀到一半斷線：那個 Bot 記成「沒有備到」，其他照常，最後印出原因並以失敗結束，主機恢復後再備份一次。
- 備份檔格式是第 2 版（`xbot-backup.json` 的 `format`）：資料目錄在根目錄，電腦資料在 `xbot-computers/<Bot id>/<home|workspace|hermes>/…`，`computers` 列出每個 Bot 有沒有備到。v1.0 之前的備份檔（第 1 版，只有資料目錄）照樣能還原。

還原：

- 資料目錄整份換掉，原本的改名成 `xbot.before-restore-<時間>` 保留。這台上還開著的 Bot 電腦會先休眠（它們接的是舊資料目錄裡的 product.sock），有工作時自動開機。
- 每個 Bot 電腦的資料寫回它所在的主機（依還原後的資料庫）：先刪掉那台電腦的容器（下次開機重建），原本有資料的 volume 先整份複製到 `<volume>-before-restore-<時間>` 保留（同一台主機上，要有一樣多的磁碟空間），再換成備份裡的資料。確認沒問題後用 `docker volume rm` 刪掉保留的 volume。
- 主機不在線上的 Bot 會跳過，資料還在備份檔裡。主機恢復後補還原：

  ```bash
  make restore FILE=… COMPUTERS_ONLY=1 BOT=<Bot id>[,<Bot id>…]
  ```

- 只想還原資料目錄：`NO_COMPUTERS=1`。

`make update` 換新版前會自動做一份只有資料目錄的備份（`xbot-backup-<時間>-before-update.tar.gz`），新版起不來時用它換回資料庫（見「升級」）。

建議：每天 `make backup` 一次（例如用 systemd timer 或 cron），備份檔複製到別台機器。

## 升級

```bash
make update
```

1. `git pull --ff-only`（有沒提交的修改就略過）。
2. `make build`。
3. `xbot service update`：
   1. 備份資料目錄（含資料庫快照）到 `xbot-backups/xbot-backup-<時間>-before-update.tar.gz`。備份失敗就不更新。
   2. 換新執行檔、更新服務檔（沿用原本的設定，見上面「怎麼設給服務」）、重啟、等健康檢查（30 秒）。
   3. 新版起不來：停下服務 → 資料庫換回第 1 步的備份（新版可能已經套用了 migration，舊版看到比較新的資料庫會拒絕啟動）→ 換回 `xbot.prev` → 重啟。新版改過的資料庫保留成 `xbot.db.replaced-<時間>`。這段時間（約 30 秒）寫進去的資料會跟著還原。
4. Bot 電腦映像：`box/` 有改（比對映像上的 `xbot.box-src` 指紋）才 `make box` 重建。沒有指紋的舊映像會重建一次（有快取，很快）。
5. `xbot service update-computers`：叫服務「更新電腦」，把映像換過或安全設定改過的電腦逐台重建（資料保留），一台主機做完再做下一台、遠端主機先傳新映像；**正在工作的 Bot 跳過**，之後在網頁「設定 → 電腦與裝置」按「更新電腦」，或執行 `xbot computers update`。全部都是最新時印「所有 Bot 的電腦都是最新的」。

`XBOT_BOX_IMAGE` 改過映像名稱時，`make update BOX_IMAGE=<名稱>`。

服務重啟時 Bot 正在做的工作：你交辦的、隊友交辦的，服務起來後自動接著做一次（同一件工作重新排隊，對話裡多一行「服務剛重開，接著做」；隊友交辦的結果照常送回交辦人）。接著做時又遇到重啟，或是定時任務、Webhook、群組的一輪，就標成「做到一半被中斷了」，交辦的 Bot 會收到沒有完成；對話裡可以按「接著做」（`xbot chat resume --task_id …`）。按了停止、還沒停好就重啟的照停止收尾。

部署到另一台：`make deploy HOST=…`（見 [安裝與更新](guide/01-install.md#部署到另一台機器)）。`make deploy` 也會把 Bot 電腦映像傳過去：那台沒有、或和這台的不同時，用你的 SSH 串流 `docker save | docker load`（不落地）。那台的帳號要能用 Docker；不能用、架構不同、或這台沒有映像時只提醒、不擋部署。`NO_IMAGE=1` 不傳，`xbot deploy --host … --image-only` 只傳映像。

## 紀錄

- 服務：`make logs`（`journalctl --user -u xbot.service -f`）。結構化文字紀錄，金鑰已遮蔽。
- Bot 電腦：`docker logs xbot-box-<Bot id>`。
- 網路防護：`docker logs xbot-netguard`。
- 誰做了什麼：網頁「設定 → 進階 → 成員與紀錄」，或 `xbot audit list`（稽核紀錄存在資料庫；可加 `--actor`、`--op`、`--status`、`--since`、`--until`）。

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
- [ ] 看一下全部合計的每月花費上限（預設 20 美元；舊版升上來沒設過的也會套用，並在「待我處理」通知一次）。

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
- [ ] 反向代理**一定要帶 `X-Forwarded-For`**。設了 `XBOT_PUBLIC_URL`（或 `XBOT_TRUSTED_PROXIES`）後本機免登入預設關掉，所以就算同一台機器上的反向代理沒帶、又把 Host 改成 `127.0.0.1`，也不會被當成「這台電腦的你」；不要再設 `XBOT_LOCAL_NOAUTH=1`。
- [ ] 反向代理不在同一台時，設 `XBOT_TRUSTED_PROXIES`。
- [ ] 只轉發上面那三條路徑更好（XBot 自己也會擋其他路徑）。
- [ ] 設了反向代理模式後，經外部 `tailscale serve` 進來的請求也會被當成外部；tailnet 內請改用內建 Tailscale。

### 從家裡出去（exit node）

- [ ] Tailscale ACL 只允許 `tag:xbot-egress` 連 `autogroup:internet`，否則 Bot 經家裡那台連得到你家的區網。

### 遠端主機

- [ ] 整台專給 XBot。`xbot` 帳號在 docker 群組＝root。
- [ ] 資料目錄的 `ssh/id_ed25519` 能登入所有主機，保護好，也注意備份檔。
- [ ] 主機的 SSH 只對 tailnet 或你的 IP 開放。
