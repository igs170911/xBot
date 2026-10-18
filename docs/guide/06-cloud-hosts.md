# 雲端主機

Bot 的電腦預設放在跑 XBot 的這台。電腦不夠時，可以把你自己的雲端主機（VPS）或家裡另一台 Linux 加進來，專門放 Bot 的電腦。XBot 經一條 SSH 連線管理那台的 Docker、看畫面，Bot 也經這條連線回來拿模型和工具。那台**不用開任何公開埠**。

## 主機需要什麼

- Linux，amd64 或 arm64。
- root 或 sudo（只有安裝時要）。
- Docker 26 版以上（沒有會幫你裝）。
- Docker 的資料目錄至少 10 GB 可用（映像約 3 GB，加上每個 Bot 的資料）。
- 記憶體：每台同時開著的電腦約 3 GB。預設同時最多開 3 台，可以調（最多 64）。
- XBot 這台連得到它的 SSH。建議兩台都在同一個 Tailscale 網路（tailnet）裡。
- 建議整台主機專給 XBot 用（原因見下面「安全說明」）。

## 加一台主機

最簡單：在 XBot 這台執行

```bash
make host-add HOST=root@203.0.113.10
make host-add HOST=me@vps NAME=cloud-01 MAX=4 SSH_OPTS="-p 2222 -i ~/.ssh/id_vps"
make host-add HOST=root@vps NO_TAILSCALE=1          # 不裝 Tailscale，XBot 直接連它的 SSH
make host-add HOST=root@vps ADDRESS=cloud-01.tailnet-xx.ts.net   # XBot 之後用這個位址連
```

它會：

1. 用你自己的 SSH 登入那台，問你一次，再用 root 跑安裝腳本：
   1. 安裝 Docker（已經有 26 版以上就略過）。
   2. 安裝 Tailscale 並加入你的 tailnet（會請你登入；`NO_TAILSCALE=1` 不裝）。
   3. 建 `xbot` 帳號，放進 docker 群組，只放 XBot 的 SSH 公鑰，不設密碼。
   4. 設定 sshd（`/etc/ssh/sshd_config.d/10-xbot.conf`），見下一節。
   5. 建資料目錄 `/var/lib/xbot`。
2. 把主機加進 XBot，自動檢查 SSH、Docker、磁碟、架構、轉發。
3. 把 Bot 電腦映像經 SSH 傳過去（第一次要幾分鐘）。

安裝腳本可以重複執行，做過的步驟會跳過。XBot 服務沒在跑時，只會檢查、不會加入清單；`make start` 後再跑一次。

也可以在網頁做：「設定 → 電腦與裝置」→「新增主機」。三步：複製一行安裝指令到主機上貼上執行 → 填主機位址 → 確認名稱和同時開機上限。

檢查一台主機：

```bash
make host-check HOST=cloud-01
make host-check HOST=cloud-01 UPDATE_IMAGE=1     # 映像和這台不同時，順便傳新的過去
```

## sshd 要的設定

安裝腳本會寫好，並檢查真的生效。如果你自己管 sshd，要確認這四行對 `xbot` 帳號有效：

```
AllowTcpForwarding yes          # 看畫面、連 Hermes 用的埠轉發
AllowStreamLocalForwarding yes  # Docker socket 與 Bot 連回 XBot 用的 socket
StreamLocalBindUnlink yes       # 重連時換掉舊的 socket 檔
StreamLocalBindMask 0111        # socket 開成 0666，容器裡的 Bot 使用者才連得到
```

`/etc/ssh/sshd_config` 本身的設定（或 `Match` 區塊）可能把它們蓋掉。腳本發現沒生效時會告訴你。

## 用起來

- **新 Bot 放哪台**：「設定 → 電腦與裝置」設預設主機；或在 Bot 設定的「能力 → 應用程式與電腦 → 在哪裡跑」指定。不指定就自動擺放。
- **滿了或離線**：工作會排隊，主機回來或有名額時自動繼續。畫面上顯示「主機離線」或「排隊中」。
- **斷線**：自動重連。網路短暫中斷通常 1 秒內接回；主機凍結約 20 秒會發現。
- **主機金鑰變了**：XBot 拒絕連線，也不會自動重試。確認是你自己重灌的，再到主機頁重新檢查。
- **移除主機**：上面還有 Bot 的電腦時不能移除，先搬走（見 [Bot 的電腦](05-computer.md#搬家換到另一台主機)）。移除只是忘掉這台，不會動主機上的任何東西。要你本人允許。
- **映像更新**：本機 `make box` 換了映像後，主機頁會出現「更新電腦映像」按鈕（或 `make host-check HOST=… UPDATE_IMAGE=1`），把新映像傳過去。要把上面的電腦換成新映像，再執行 `xbot computers update --host_id <id>`。

## 安全說明

- XBot 用 `xbot` 帳號連主機。這個帳號在 docker 群組，**等於有那台的 root 權限**。請把整台主機專給 XBot，不要放其他重要服務或資料。
- XBot 這台的 `~/.local/share/xbot/ssh/id_ed25519` 是登入所有主機的私鑰。保護好資料目錄，備份檔也一樣。
- 每台主機的主機金鑰記在 `hosts/<主機 id>/known_hosts`，金鑰變了就拒絕連線。
- 容器的畫面埠和 Hermes 埠只發布在主機的 127.0.0.1，只有經 SSH 才連得到。
- `StreamLocalBindMask 0111` 讓 socket 對主機上所有帳號開放。它放在 0711 的資料夾裡、要知道名字才連得到，每個請求也要那個 Bot 自己的金鑰。主機專給 XBot 時沒有額外風險。
- 主機上的 Bot 電腦一樣有網路防護：連不到主機本身、私有網段、Tailscale 網段和彼此。
- 模型金鑰不會傳到主機上。Bot 要用模型時，經 SSH 回到 XBot 的 Model Proxy。
