# B11 實測報告：經 SSH 反向轉發 product.sock

**結論：可以照原設計做下去（PRD §16.2、TECH-DESIGN §5 不用改）。**
一條 SSH 連線同時帶 Docker API、noVNC／Hermes 埠轉發和 10 個 Bot 的 product.sock 反向轉發，都正常；
斷線後 0.1～0.6 秒自動接回；SSH 本身幾乎不加延遲，實際多出來的時間就是網路往返（每個模型請求約 1 個往返）。

測試日期：2026-10-18。數字來自 `internal/hosts/ssh_bench_docker_test.go`（`XBOT_B11_BENCH=1`），可以重跑。

## 怎麼測的

- **沒有真的遠端主機**，所以開一台「測試遠端主機」容器：`docker:29-dind`＋OpenSSH，自己的 Docker daemon，只有 `xbot` 帳號（在 docker 群組）和測試產生的公鑰。
  映像在 `internal/hosts/sshtesthost/image/`，第一次會建，之後沿用。Bot 電腦映像（xbot-box:dev）用 `docker save | docker load` 載進去，放在快取 volume `xbot-test-sshhost-docker`，之後不用再載。
- XBot 這端跑在本機：資料庫、Model Proxy、每個 Bot 的 product.sock、假模型（`hermestest.FakeModel`）都在本機；Bot 電腦在測試主機裡。
- 完整路徑：Hermes（遠端容器）→ hermes_host.py → 遠端 `/var/lib/xbot/bots/<id>/ipc/product.sock` → SSH 反向轉發 → 本機 product.sock → Model Proxy → 假模型。
- 「網路延遲」用 `tc netem` 在測試主機的網卡加 20 ms，模擬 tailnet 上同區域的主機。
- 機器：6 核、15 GB 記憶體，本機和測試主機在同一台，所以沒加延遲時的數字只代表 SSH 本身的成本。

## 結果

### 1. 斷線會不會自動接回？會。

| 情況 | 多久發現 | 多久接回 | 期間的模型請求（每 50 ms 一個，共 120 個） |
|---|---|---|---|
| 本機這端直接切斷 TCP | 立刻 | **0.07 秒** | 120 個全部成功 |
| 遠端 sshd 整個被殺掉（含這條連線），0.3 秒後重啟 | 立刻 | **0.55 秒** | 10 個失敗（連不上 socket）、110 個成功 |
| 主機凍結 20 秒（`docker pause`，TCP 沒斷但沒回應） | **約 21 秒**（keepalive：每 10 秒一次，15 秒沒回應就斷） | 恢復後立刻 | 凍結期間全部卡住 |

- 接回後三種轉發都恢復：Docker API、noVNC／Hermes 的本機轉發埠（**埠號不變**，box 表記的位址繼續有效）、每個 Bot 的 product.sock。
- Hermes 驅動的 WebSocket 會斷一次，下一輪對話自動重連；端對端測試裡「切斷 TCP」和「殺掉 sshd」之後的對話都正常完成。
- 斷線那一瞬間正在跑的模型請求會失敗（hermes_host.py 回錯誤給 Hermes），之後的請求正常。沒有自動重播（符合 TS-7「沒有自動重播」）。
- 主機金鑰不同（被冒充、或主機重灌）時拒絕連線，**不會自動重試**，狀態是 `failed`，要人處理。

### 2. 延遲多少？

**模型請求**（curl 經 product.sock 打 `/internal/providers/openai/v1/chat/completions`，假模型立刻回覆）：

| 路徑 | 每次新連線 | 沿用連線（hermes_host.py 的 httpx 就是這樣） |
|---|---|---|
| 本機 socket（沒有 SSH，比較基準） | 0.7～0.9 ms | 0.4～0.5 ms |
| 遠端經 SSH，網路 0 延遲 | 0.9～1.1 ms | 0.55～0.6 ms |
| 遠端經 SSH，往返 +20 ms | 41 ms（2 個往返） | **21 ms（1 個往返）** |

→ SSH 本身每個請求只多約 0.1～0.2 ms。真正的成本是網路：沿用連線時每個請求 1 個往返，新連線 2 個往返（多一次開 channel）。
一輪 Hermes 對話通常要好幾個模型請求，所以「往返時間 × 模型請求數」就是額外延遲；和模型本身動輒數秒的回應時間比，影響很小。

**noVNC**（經本機轉發埠）：

| 項目 | 0 延遲 | 往返 +20 ms |
|---|---|---|
| GET /vnc.html（遠端直接連） | 4.5～5.9 ms | — |
| GET /vnc.html（經 SSH 轉發，每次新連線） | 4.9～7.3 ms | 44 ms |
| WebSocket 連上到收到 RFB 版本 | 4.4～6.7 ms | 44 ms |
| 下載 100 MB | 430～550 MB/s | **約 80 MB/s** |

→ 開畫面要多 2 個往返；之後畫面串流的頻寬遠大於 VNC 需要的量（1280×720 畫面一般 < 5 MB/s）。
往返 20 ms 時的 80 MB/s 是 SSH channel 視窗（x/crypto/ssh 預設 2 MB）限制的，跨洲高延遲線路會更低，但對 VNC 仍然夠。

**一輪對話**（Hermes＋假模型＋事件串流，同一個 Bot、同一條對話串）：0 延遲中位數 60～67 ms；往返 +20 ms 中位數約 120 ms。

### 3. 同時 10 個 Bot（一條 SSH 連線）

- 10 台電腦同時開機到全部就緒：13～15 秒（瓶頸是 CPU，不是 SSH）。
- 一條連線上同時有：10 個 product.sock 反向轉發、20 個本機轉發埠（每台 noVNC＋Hermes）、10 條 Hermes WebSocket、Docker API。
- 10 台同時各送 50＋50 個模型請求（共 1000 個，0.7 秒內）：新連線中位數 2.6～2.8 ms、p95 5～6 ms；沿用連線中位數 1.6 ms、p95 4～5 ms，**全部成功**。
- 10 台同時 GET noVNC（200 次）：中位數 12～15 ms、p95 20～25 ms，全部成功。
- 10 台同時第一次對話：每台 10～18 秒，和本機第一次對話（約 9 秒，Hermes 啟動 agent）同一個原因，6 核機器同時啟動 10 個 Hermes 是 CPU 瓶頸；之後每輪約 0.1 秒。
- 遠端 sshd 這條連線的兩個行程共約 11.5 MB 記憶體。

### 4. 有沒有碰到上限？

| 測試 | 結果 |
|---|---|
| 200 個同時的 product.sock 請求（同一個 Bot） | 200 個全部 HTTP 200 |
| 200 條同時開著的 noVNC WebSocket（經轉發埠） | 200 條全部成功 |
| 20 個同時的「執行指令」session（sshd `MaxSessions 10`） | **10 個成功、10 個失敗** |

→ `MaxSessions` 只限制 session（執行指令、shell），**不限制轉發**。XBot 只在建立 Bot 的 ipc 資料夾時短暫開一個 session，碰不到；
但 B12 的健康檢查、CL-5 搬家打包如果要同時跑很多指令，要排隊或在主機設定提高 `MaxSessions`。

## 設計（`internal/hosts.SSH`，實作 `Host` 介面）

- **一條連線**（`golang.org/x/crypto/ssh`）：
  - Docker API：`client.WithDialContext` 每次都經 SSH 的 `direct-streamlocal` 連遠端 `/var/run/docker.sock`。Docker socket 不對網路開放。
  - `Addr(port)`：在本機 `127.0.0.1:<隨機埠>` 開一個轉發埠，每條連線經 `direct-tcpip` 連遠端 `127.0.0.1:port`。同一個遠端埠固定同一個本機埠，重連後不變。
  - `PrepareIPC(botID)`：在遠端建 `/var/lib/xbot/bots/<id>/ipc`（0711），用 `streamlocal-forward@openssh.com` 讓 sshd 聽 `product.sock`；每條進來的連線都接到本機那個 Bot 的 product.sock（`ipc.Server.Path`）。本機的 product.sock 和本機主機時完全一樣，worker-key 驗證不變。
- **主機金鑰**：每台主機一個 known_hosts 檔（例：`<data>/hosts/<id>/known_hosts`）。第一次連線記下金鑰；之後不同就拒絕、停止重連（`ErrHostKeyMismatch`）。握手時只接受已記下的那種金鑰類型。
- **重連**：連線穩定超過 2 秒後斷掉時先立刻重試一次，之後 0.5 秒起加倍退避、最多 15 秒。重連成功後先恢復所有反向轉發，再標記在線。主機離線時 Docker 呼叫、轉發最多等 10 秒就回報「主機離線」，不會卡死。
- **健康狀態**：`Health()` 回傳 `connecting / online / reconnecting / failed / closed`、最後的錯誤、連線次數、keepalive 往返、轉發數量（B12 的主機清單用）。
- 所有數字在 `internal/limits/hosts.go`。
- 為了服務重啟後能接回轉發，Box Manager 現在**每次開機前**都呼叫 `PrepareIPC`（以前只在建立容器時）；runtime 的 Prepare 對遠端主機也會開本機的 product.sock。

## 主機需求（B12 的一行安裝指令要設定）

遠端主機的 `/etc/ssh/sshd_config`：

```
AllowTcpForwarding yes          # noVNC／Hermes 埠轉發
AllowStreamLocalForwarding yes  # Docker socket 與 product.sock
StreamLocalBindUnlink yes       # 重連時換掉上一條連線留下的 product.sock 檔（沒有這行，重連後反向轉發會失敗）
StreamLocalBindMask 0111        # socket 開成 0666：容器裡的 Bot 使用者（UID 1000）不是 xbot 帳號
```

其他：
- `xbot` 帳號在 `docker` 群組，只放 XBot 的公鑰；`/var/lib/xbot` 由 `xbot` 擁有。
- 容器只發布在遠端的 127.0.0.1（B02 已經這樣做）。
- 參考設定：`internal/hosts/sshtesthost/image/sshd_config`。
- 安全提醒：`StreamLocalBindMask 0111` 讓 socket 對主機上所有帳號開放，但它在 0711 的資料夾裡、名稱要知道才連得到，而且每個請求都要那個 Bot 的 worker-key，和本機主機一樣。主機整台專給 XBot 時沒有額外風險。

## 和原本規格的差異

- PRD §16.2 寫「`docker context` 的 `ssh://xbot@<主機>`」：實作不用 docker CLI 的 ssh helper（`docker system dial-stdio`），而是同一條 SSH 連線直接開 `direct-streamlocal` 到 Docker socket。好處是一條連線全包、不用在 XBot 主機裝 docker CLI；代價是主機要開 `AllowStreamLocalForwarding`（反向轉發本來就需要）。
- 映像現在是測試裡 `docker save | docker load` 進去；正式的「主機自己拉映像」是 B12。

## 後續（範圍外）

1. **E02 驅動的問題（不是 SSH 造成的，本機也重現）**：一輪的 `message.complete` 之後**立刻**在同一個 Bot 送下一個 prompt，Hermes 會回覆 `prompt.submit` 卻不執行，這一輪永遠等不到結束（只能等連線中斷）。隔 2 秒再送就正常。正式使用時人打字不會這麼快，但 Bot 之間互傳、排程工作可能會碰到。建議 E02 在送出前等 Hermes 這個 session 真的閒下來，或加上「送出後 N 秒沒有 message.start 就重送／報錯」。~~測試目前用 3 秒間隔避開（`turnGap`）。~~ **已修（task/E02-fix-backtoback）**：原因是 Hermes 在 `message.complete` 之後還沒放開 `running`（收尾完才送 `session.info` running=false），這時的 `prompt.submit` 依 `busy_input_mode: steer` 被當成插話（回 `{"status":"steered"}`），不會開新的一輪。驅動現在先等 session 閒下來、送出時帶 `queued: true`，並加了等不到 `message.start` 的看門狗；`turnGap` 已移除。
2. 斷線那一瞬間的模型請求會失敗：要確認 Hermes 對 product.sock 回的錯誤會不會自己重試（目前看起來會在下一輪恢復）。
3. 容器重建後舊埠的本機轉發埠不會自動關（`ReleaseAddr` 已有，還沒接上）；刪除 Bot 時要呼叫 `ReleaseIPC` 並刪遠端的 `bots/<id>` 資料夾。B13 處理。
4. 主機凍結要約 20 秒才發現；要更快就把 keepalive 調短（代價是網路抖動時容易誤判斷線）。
5. 高延遲線路下 noVNC 頻寬受 SSH 視窗限制；如果之後要跨洲，可以考慮讓 noVNC 直接走 tailnet（仍只綁 tailnet IP）。
6. 健康狀態寫回 `host.status`、主機離線時 Bot 顯示「主機離線」（CL-6）是 B12。

## 怎麼重跑

```
make box                      # 還沒有 xbot-box:dev 時
make test-docker              # 含 TestSSHHostEndToEnd（建立 → 開機 → 對話 → 切斷 TCP → 殺 sshd → 再對話 → 主機金鑰不符）
XBOT_B11_BENCH=1 XBOT_B11_BOTS=10 go test -tags docker -count=1 -timeout 40m -run TestSSHForwardBench -v ./internal/hosts/
```

測試結束會刪掉測試主機容器、網路，以及裡面的 Bot 電腦和 volume；只留下建置快取：映像 `xbot-test-sshhost:<雜湊>` 和 volume `xbot-test-sshhost-docker`（裡面是載入的 Bot 電腦映像）。不要了可以 `docker volume rm xbot-test-sshhost-docker`。
