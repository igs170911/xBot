# 安裝與更新

所有操作都從 `make` 開始，在 XBot 的專案資料夾裡執行。`make help` 列出全部指令。

## 需要什麼

- Linux（amd64 或 arm64），有 systemd。macOS、Windows 沒有支援。
- `make`。其他的（Go、Node、pnpm、Docker…）`make setup` 會裝。
- 磁碟：Bot 電腦映像約 3 GB，每個 Bot 再加它自己的檔案。建議留 20 GB 以上。
- 記憶體：每台開著的 Bot 電腦最多用 3 GB（2 CPU）。閒置 30 分鐘會自動休眠。

先看缺什麼：

```bash
make doctor
```

每一項會顯示 ✅／⚠️／❌，沒過的會附一行修正指令。

## 安裝

```bash
make setup     # 補齊工具；要用 sudo 的（Docker、Tailscale）會先問你。YES=1 不問直接裝
make install   # 補齊 → 建置 → 建 Bot 電腦映像 → 裝成 systemd 使用者服務
```

`make install` 做的事：

1. 再跑一次 `make setup`。
2. `make build`：建出單一執行檔 `bin/xbot`（前端已經包在裡面）。
3. `make box`：建 Bot 電腦映像 `xbot-box:dev`。第一次約 10 分鐘，之後有快取。
4. 把執行檔放到 `~/.local/bin/xbot`，資料放 `~/.local/share/xbot`，服務檔是 `~/.config/systemd/user/xbot.service`。不需要 sudo。
5. 問你要不要開 linger（`loginctl enable-linger`）。開了才會在開機時自動啟動，不用先登入。
6. 印出網址、區網網址、登入碼的位置。

裝好後：

- 這台電腦：打開 http://127.0.0.1:8780 ，不用登入。
- 區網其他裝置：打開安裝時印出的 `http://<區網 IP>:8780`，輸入登入碼。登入碼用 `make token` 看。登入後 30 天內不用再輸入。
- 手機：建議走 Tailscale 的 https，見 [手機](14-phone.md)。

只想讓這台電腦連：

```bash
XBOT_BIND=127.0.0.1 make install
```

> 注意：`make update` 會用當下的環境變數重寫服務檔。裝的時候用了 `XBOT_BIND`、`XBOT_PORT` 或 `XBOT_DATA_DIR`，更新時也要帶一樣的值，或改用服務的覆寫檔（見 [管理手冊](../ADMIN.md#環境變數)）。

## 管理服務

| 指令 | 做什麼 |
|---|---|
| `make status` | 服務狀態 |
| `make logs` | 即時看服務紀錄（Ctrl+C 離開） |
| `make start`／`make stop`／`make restart` | 啟動／停止／重新啟動 |
| `make token` | 顯示其他裝置用的登入碼 |

## 更新

```bash
make update
```

依序做：`git pull --ff-only`（有沒提交的修改就略過）→ `make build` → 備份 → 換新版重啟 → 健康檢查。新版起不來會自動換回上一版。資料庫的 migration 在服務啟動時自動執行。

`make update` **不會**重建 Bot 電腦映像。版本說明提到映像有改時，再做：

```bash
make box
```

然後更新電腦：「設定 → 電腦與裝置」上方出現「N 台電腦要更新」時，按「更新電腦」。指令列也可以：

```bash
xbot computers update --dry-run   # 先看會更新哪些
xbot computers update             # 所有主機；只更新一台加 --host_id <id>
```

XBot 會一台主機做完再做下一台：把新映像傳過去，再把電腦逐台重建。家目錄、工作資料夾、瀏覽器登入都保留；正在工作的 Bot 會跳過，下次再更新。

## 備份與還原

```bash
make backup                       # 備份到 ~/.local/share/xbot-backups/
make backup INCLUDE_LOGINS=1      # 連登入資料一起
make backup OUT=/mnt/usb/xbot.tar.gz
make restore FILE=~/.local/share/xbot-backups/xbot-backup-20261019-090000.tar.gz
```

- 服務執行中也能備份（資料庫用 SQLite 快照）。
- 網頁上也可以：「設定 → 進階 → 備份與更新」按「立即備份」（不含登入資料），下面列出最近的備份檔、目前版本，以及還原和更新要在終端機打的指令。
- 還原時服務會先停下，還原完再啟動，所以只能用指令列。原本的資料會改名保留，確認沒問題再自己刪。

備份**包含**：資料庫（Bot、對話、技能、定時任務、設定、用量）、加密金鑰 `model-proxy.key`、登入碼、SSH 金鑰、內建 Tailscale 狀態、共用技能庫。

備份**不包含** Bot 電腦裡的檔案（家目錄、工作資料夾、Hermes 紀錄、瀏覽器登入）。它們放在 Docker volume（`xbot-box-<Bot id>-home` 等），要另外備份，做法見 [管理手冊](../ADMIN.md#備份)。

`model-proxy.key` 和資料庫一定要放在一起。少了它，存著的 API 金鑰就解不開。

## 移除

```bash
make uninstall            # 移除服務與執行檔，資料保留
make uninstall PURGE=1    # 連資料一起刪，會要你輸入「刪除」確認
```

Bot 的電腦（容器與 volume）不會被 `make uninstall` 刪掉。要清掉請先在網頁上刪除 Bot，或用 `docker ps -a --filter label=xbot.managed=true` 找出來自己刪。

## 部署到另一台機器

```bash
make deploy HOST=me@server.example.com
make deploy HOST=me@server SSH_OPTS="-p 2222 -i ~/.ssh/id_vps" PORT=8780
```

用你自己的 SSH 帳號：看對方的 CPU 架構 → 傳對應的執行檔 → 第一次就安裝、裝過就更新（失敗自動回滾）→ 健康檢查。XBot 會裝成那個帳號的 systemd 使用者服務。可以重複執行。

注意：`make deploy` 只傳 XBot 本身，不傳 Bot 電腦映像。那台要開 Bot 的電腦，先把映像搬過去：

```bash
docker save xbot-box:dev | ssh me@server docker load
```

或在那台機器上也拿一份原始碼跑 `make box`。只是要把那台當成「放 Bot 電腦的主機」，不需要部署 XBot，用 `make host-add`，見 [雲端主機](06-cloud-hosts.md)。

## 另一種方式：docker compose

```bash
make up      # 用 docker compose 跑服務
make down
```

這個方式還在試驗階段：服務在容器裡，開 Bot 電腦的路徑對不上主機。目前只建議拿來看網頁，要真的讓 Bot 做事請用 `make install`。
