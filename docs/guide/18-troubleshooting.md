# 疑難排解

## 先做這三件事

```bash
make doctor     # 缺什麼工具、Docker 能不能用、磁碟、埠號；每項附修正指令
make status     # 服務有沒有在跑
make logs       # 即時看服務紀錄（Ctrl+C 離開）
```

`make logs` 是 `journalctl --user -u xbot.service -f`。要看更早的：`journalctl --user -u xbot.service --since "1 hour ago"`。

Bot 電腦本身的紀錄：

```bash
docker ps -a --filter label=xbot.managed=true     # 列出所有 Bot 的電腦（名稱是 xbot-box-<Bot id>）
docker logs --tail 200 xbot-box-<Bot id>
docker logs --tail 50 xbot-netguard               # 網路防護
```

## 安裝與服務

| 看到 | 原因與修法 |
|---|---|
| `make doctor` 顯示 ❌ | 照那一行的「修正」做，通常是 `make setup` |
| 「這台機器沒有可用的 systemd 使用者服務」 | 在容器或沒有 systemd 的系統。改用 `bin/xbot serve` 直接跑 |
| 重開機後服務沒起來 | 沒開 linger：`loginctl enable-linger $USER`（或 `sudo loginctl enable-linger 你的帳號`） |
| 「服務埠 0.0.0.0:8780」顯示被占用 | 別的程式在用 8780。停掉它，或 `XBOT_PORT=8781 make install` |
| `make update` 說「已換回上一版」 | 新版起不來，已自動回到舊版，服務正常。看 `make logs` 找原因 |
| 更新後只聽本機／埠號變了 | `make update` 沿用服務檔裡的設定，只有這次明確帶的變數（例：`XBOT_BIND=…`）才會改；更新時會印出「服務設定 … → …」。看 `~/.config/systemd/user/xbot.service`，要改回來就帶正確的值再 `make update`。見 [管理手冊](../ADMIN.md#環境變數) |
| 加入 docker 群組後還是不能用 Docker | 要登出再登入（或 `newgrp docker`） |

## 連不上網頁

| 看到 | 原因與修法 |
|---|---|
| 別台裝置打不開 | 確認服務聽 `0.0.0.0`（`systemctl --user cat xbot` 看 `XBOT_BIND`）；這台的防火牆（ufw 等）要開 8780 |
| 「登入碼不對」 | `make token` 看正確的登入碼 |
| 試太多次被擋 | 同一個來源錯 10 次會鎖 15 分鐘，等一下再試 |
| 手機不能「加入主畫面」、收不到推播 | 用的是 `http://` 區網網址。要用 Tailscale 的 `https://` 網址，見 [手機](14-phone.md) |
| Tailscale 網址打不開或憑證錯 | Tailscale 後台 DNS 頁要開 MagicDNS 和 HTTPS Certificates；「設定 → 進階 → 網路與 Tailscale」要顯示「已連上 tailnet」 |
| 外部入口（Funnel）開不起來 | tailnet 的存取規則要給這台 `funnel` 屬性（ACL 的 `nodeAttrs`） |

## Bot 的電腦

| 看到 | 原因與修法 |
|---|---|
| 「無法連線到 Docker，請確認 Docker 有在執行」 | `sudo systemctl start docker`；確認目前帳號能跑 `docker ps` |
| 「找不到 Bot 電腦映像 xbot-box:dev，請先執行 make box」 | 執行 `make box` |
| 「這台主機上沒有 Bot 電腦映像」 | 遠端主機缺映像：主機頁按「重新檢查」或 `make host-check HOST=… UPDATE_IMAGE=1` |
| 「Bot 電腦的網路防護沒有生效（SEC-4），為了安全先不開機」 | 防火牆規則套不上。常見原因：rootless Docker（不支援，`make doctor` 會顯示 ❌ 和改法）、主機沒有 iptables。看 `docker logs xbot-netguard`。不建議，但真的沒辦法時可設 `XBOT_NETGUARD=off` |
| 「電腦尚未就緒，請查看電腦日誌」 | 開機超過 75 秒還沒好。`docker logs xbot-box-<Bot id>`；記憶體不夠時也會這樣 |
| 「這個 Bot 正在忙，等它做完或先停止」 | 休眠、重開、重建、搬家、示範都要 Bot 閒著。先按停止 |
| Bot 連不到區網的某台機器 | 這是故意的。要開放見 [Bot 的電腦](05-computer.md#讓它連區網的某台機器) |
| 畫面一直「重新連線」 | 會自動重試 2 次；失敗按「重新連線」。遠端主機時先確認主機在線上 |
| 電腦標「過期」 | 映像或安全設定更新了：`xbot computers update` |

## 遠端主機

| 看到 | 原因與修法 |
|---|---|
| 「主機離線」「這個 Bot 的電腦所在的主機連不上」 | 工作會排隊等主機回來。`make host-check HOST=<名稱>` 看原因 |
| 「主機金鑰和第一次連線時不同」 | 主機重灌或被換掉。XBot 不會自動重試。確認是你自己重灌的，刪掉資料目錄的 `hosts/<主機 id>/known_hosts` 再檢查 |
| 「不能在主機上建立 socket 轉發（AllowStreamLocalForwarding）」等 | sshd 設定被蓋掉。照 [雲端主機](06-cloud-hosts.md#sshd-要的設定) 改好，重新載入 sshd |
| 「Docker 版本太舊」 | 主機上 `curl -fsSL https://get.docker.com \| sudo sh` |
| 「安裝腳本沒有完成」 | 照上面印出的原因修好，再跑一次 `make host-add`（做過的步驟會跳過） |

## 模型與花費

| 看到 | 原因與修法 |
|---|---|
| 「還沒設定模型：請先設定模型供應商與預設模型」 | 「設定 → AI 模型」接一個供應商並選預設模型 |
| 「…超過今天／這個月的花費上限」 | 到了你設的上限。「設定 → 用量與花費」提高上限（要你允許），或等明天、下個月 |
| 「模型供應商太久沒有送資料（串流閒置逾時）」 | 供應商 5 分鐘沒回應。XBot 會在 2、10、30 分鐘後重試；一直這樣就換模型或設備援模型 |
| OpenCode 回「Invalid credential」 | 用 `auth.json` 裡 `opencode` 那把金鑰；或到 opencode.ai 看額度 |
| OpenCode Zen 回「Model is unavailable」 | 用了 `-free` 模型，XBot 不能用免費版。改用付費模型或 OpenCode Go |
| 快速判斷出現「Insufficient account funds」 | 快速判斷設成 Zen 的 `jev-1.13`，但 Zen 沒儲值。在快速判斷設定按一鍵改用預設模型 |
| 快速判斷說「opencode-zen 還沒設定」 | 選了 Jev，但沒加 OpenCode Zen 的金鑰。按「改用」換成你的預設模型，或把快速判斷關掉 |
| 快速判斷沒效果 | 用途預設是「影子」或「關」。看快速判斷統計，準了再改「生效」 |
| Bot 換了供應商沒生效 | 重開它的電腦：`xbot computers restart --bot_id <id>` |

## 引擎與訂閱

| 看到 | 原因與修法 |
|---|---|
| Claude Code、Codex 等引擎的 Bot 不肯工作 | 還沒同意使用訂閱。Bot 設定「進階」勾選同意；並確認這台電腦登入了（`make doctor`） |
| 「額度用完」卡 | 訂閱或 API 額度用完、限速、登入過期。等卡上寫的恢復時間，或按卡上的換引擎／模型並重送 |
| Gemini 的 Bot 一直等，最後說沒有登入 | 這台電腦的 Gemini 登入失效。在這台執行 `gemini` 重新登入 |
| 訂閱登入一直失效 | 這台電腦和 Bot 的電腦共用登入，可能互相踢掉。在這台電腦重新登入，再重開 Bot 的電腦 |
| 改了權限等級沒生效 | 下次電腦重開才生效 |

## 工作卡住

- Bot 15 分鐘沒動靜（跑工具時 40 分鐘）會自動停下並通知你。
- 想立刻停：對話裡按 ■，或 `xbot chat stop --bot_id <id>`。
- 想知道它卡在哪：「更多」→「目前進度」，或 `xbot tasks progress --bot_id <id>`。
- Bot 之間聊太久：同一條往來 6 次、群組 4 輪會自動停。

## 還是不行

把這些附上回報：`make doctor` 的輸出、`journalctl --user -u xbot.service --since "30 min ago"`、出問題的 Bot 電腦的 `docker logs`。紀錄裡的金鑰已經遮蔽，但貼出去前還是看一下。
