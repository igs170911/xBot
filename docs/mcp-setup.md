# 把代理接上 XBot MCP

XBot 的每個功能都是一個操作（`xbot.<領域>.<動作>`），同一份定義產生 MCP 工具、HTTP API 和 `xbot` 指令列（PRD MCP-1、MCP-2）。外部代理（Claude Code、Codex、Cursor…）有兩種接法：

| 入口 | 網址／指令 | 什麼時候用 |
|---|---|---|
| Streamable HTTP | `http://127.0.0.1:8780/mcp`（tailnet 內：`https://xbot.<tailnet>.ts.net/mcp`） | 代理和服務在不同機器，或用戶端支援 HTTP MCP |
| stdio | `xbot mcp` | 用戶端只支援 stdio；或在服務那台機器上用 |
| 外部入口（預設關閉） | Funnel：`https://xbot.<tailnet>.ts.net/mcp`；反向代理：`<XBOT_PUBLIC_URL>/mcp` | 代理不在你的 tailnet 裡（見 §8） |

要用哪個網址，看網頁「設定 → 進階 → 網路與 Tailscale」，或 `xbot network status`（`base_url`：給其他電腦用的網址；`public_url`：tailnet 外連得到的網址，沒開外部入口時是空白）。

兩個入口都要**代理權杖**（用配對碼換來，見下）。stdio 只是把 stdin／stdout 接到服務的 `/mcp`，所有呼叫一樣在服務裡執行、記稽核、走核准。

## 1. 配對：產生配對碼 → 換權杖（MCP-6）

1. **你（使用者）** 產生配對碼，決定這個代理能做什麼（權限可疊加：`read` 查看、`operate` 交辦工作、`manage` 管理 Bot／團隊、`admin` 系統設定）：

   ```sh
   xbot agents pair_start --name "Claude Code" --scopes read --scopes operate
   # 只讓它碰某幾個 Bot：--resources.bots <bot id> --resources.bots <bot id>
   # 權杖 90 天後失效：--token-days 90
   ```

   會得到 `{"code": "RY3F-Z6YP", "expires_at": …}`。配對碼**只能用一次、5 分鐘內有效**。網頁的「設定 → 進階 → 外部代理與 MCP」→「連接新的代理」是同一件事。只有你本人能產生配對碼或改代理權限；代理就算有 `admin` 也不行。

2. **代理那台機器**用配對碼換權杖（不需要登入）：

   ```sh
   XBOT_URL=http://127.0.0.1:8780 xbot mcp pair RY3F-Z6YP
   ```

   或直接打 HTTP：

   ```sh
   curl -s -X POST http://127.0.0.1:8780/api/v1/agents.pair \
     -H 'Content-Type: application/json' -d '{"code":"RY3F-Z6YP","name":"Claude Code"}'
   # → {"token":"xbot_…","agent":{"id":"…","name":"Claude Code","scopes":["read","operate"],…}}
   ```

   權杖**只會顯示這一次**，服務只存它的雜湊。換錯 10 次會鎖 60 秒（所有來源合計錯 50 次也會鎖）。

3. 管理已連線的代理：

   ```sh
   xbot agents list                                   # 不會顯示權杖
   xbot agents update_scopes --id <id> --scopes read  # 整組取代
   xbot agents revoke --id <id>                       # 下一個請求就會被拒絕（401）
   ```

## 2. Claude Code

HTTP（建議）：

```sh
claude mcp add --transport http xbot http://127.0.0.1:8780/mcp \
  --header "Authorization: Bearer xbot_你的權杖"
```

加 `--scope project` 會寫進專案的 `.mcp.json`（裡面有權杖，**不要提交到 git**；也可以在 `.mcp.json` 裡寫 `"Authorization": "Bearer ${XBOT_TOKEN}"`，讓 Claude Code 從環境變數展開）。預設 `--scope local` 只存在你自己的設定裡。

stdio：

```sh
claude mcp add xbot --env XBOT_TOKEN=xbot_你的權杖 --env XBOT_URL=http://127.0.0.1:8780 -- xbot mcp
```

檢查：`claude mcp list` 應該顯示 `xbot … ✔ Connected`；在對話裡 `/mcp` 可以看到 `xbot.system.ping` 等工具。權杖錯或被撤銷時會顯示 `HTTP 401 … 權杖無效、已撤銷或已過期`。

## 3. Codex CLI

`~/.codex/config.toml`：

```toml
# stdio（任何版本都可以）
[mcp_servers.xbot]
command = "xbot"
args = ["mcp"]
env = { XBOT_TOKEN = "xbot_你的權杖", XBOT_URL = "http://127.0.0.1:8780" }

# 或 Streamable HTTP（Codex 支援 HTTP MCP 的版本）
# [mcp_servers.xbot]
# url = "http://127.0.0.1:8780/mcp"
# bearer_token_env_var = "XBOT_TOKEN"
```

## 4. 其他用戶端（Cursor、自己寫的程式…）

- **HTTP**：Streamable HTTP，端點 `<服務網址>/mcp`，每個請求帶 `Authorization: Bearer <權杖>`。一條 MCP 連線（`Mcp-Session-Id`）綁定建立它的權杖；閒置 30 分鐘關閉。
- **stdio**：執行 `xbot mcp`，環境變數 `XBOT_TOKEN`（或 `--token`）、`XBOT_URL`（或 `--url`，預設 `http://127.0.0.1:8780`）。

Cursor 的 `~/.cursor/mcp.json`：

```json
{ "mcpServers": { "xbot": { "url": "http://127.0.0.1:8780/mcp", "headers": { "Authorization": "Bearer xbot_你的權杖" } } } }
```

## 5. 呼叫規則（所有入口都一樣）

- 工具名稱就是操作名稱，例：`xbot.bots.create`。`xbot ops` 列出全部。
- 會改東西的工具多兩個參數：`request_id`（重送只做一次，回第一次的結果）、`dry_run`（只回傳會發生什麼）（MCP-4）。工具標註：查看類 `readOnlyHint`，要核准的 `destructiveHint`。
- 錯誤一律是工具錯誤（`isError: true`），`structuredContent` 固定是 `{code, message, retryable, needs_approval_id?}`（MCP-5）。`forbidden` 是權限不夠；`pending_approval` 表示已送到你的「待我處理」，代理要用 `xbot.approvals.wait` 等，不要重送。
- 任何工具都不會回傳金鑰或權杖（MCP-11）；每次呼叫都記稽核：誰、哪個工具、參數（密鑰遮蔽）、結果（MCP-13）。

## 6. 你自己在本機用

- 指令列：`xbot <領域> <動作> --欄位 值`，例：`xbot system ping --echo hi`、`xbot agents list`。它讀這台機器資料目錄裡的登入碼（權限 600 的 `access-token`），以你本人的身分呼叫；設 `XBOT_TOKEN` 則改用那個代理權杖。陣列欄位可重複旗標或給 JSON（`--tags a --tags b`／`--tags '["a","b"]'`），巢狀欄位用點（`--resources.bots …`），整份輸入用 `--json '{…}'`（`--json -` 從 stdin 讀）。每個指令都有 `--help`。
- `xbot mcp --owner`：stdio MCP 以你本人的身分連線（同樣是讀本機登入碼，所以只能在服務那台機器、同一個使用者執行）。給你完全信任的本機工具用；一般代理請配對。
- `XBOT_MCP_LOCAL_OWNER=1`（啟動服務時設定，預設關閉）：從這台機器直接連 `/mcp`、沒帶權杖的請求都當成你本人。同一台機器上的任何程式都能因此拿到完整權限，只建議在開發時用。經 Tailscale serve 或反向代理進來的請求（Host 不是 localhost）不適用。

## 7. 第一次對話（從配對到拿到回覆）

前提：服務在跑（`make start` 或 `xbot serve`），Bot 電腦映像建好了（`make box`）。

1. **配對**（你本人，在服務那台機器上）：

   ```sh
   xbot agents pair_start --name "Claude Code" --scopes read --scopes operate --scopes manage
   xbot mcp pair <配對碼>          # 在代理那台機器上；記下印出的權杖
   ```

   再照 §2～§4 把權杖接到代理。建 Bot 要 `manage`，對話要 `operate`，讀回覆要 `read`。

2. **設定模型**（你本人；金鑰只能寫、不能讀，存在服務端的 Model Proxy，不會進 Bot 的電腦）。金鑰用 `--json -` 從標準輸入給，不會留在指令紀錄裡：

   ```sh
   xbot models set_provider --json - <<'JSON'
   {"id":"openai","base_url":"https://api.openai.com/v1","api_key":"sk-…","default_model":"gpt-4.1-mini"}
   JSON
   xbot models get                 # ready: true 就可以了；只回 api_key_set，不回金鑰
   ```

   之後要換預設模型：`xbot models set_default --provider openai --model <模型>`。代理就算有 `admin` 也不能設金鑰。

3. **建 Bot**（代理呼叫 MCP 工具，或你用指令列）：

   ```sh
   xbot bots create --name 小秘 --title 幕僚長
   # → {"id":"<bot id>","approval_mode":"ask",…}   新 Bot 預設「先問我」（SEC-1）
   ```

4. **對話**：`xbot.chat.send` 收下就回傳，Bot 在背景做事（第一次會先開機，約半分鐘）；再用 `xbot.chat.wait_until_settled` 等它做完或需要人：

   ```sh
   xbot chat send --bot_id <bot id> --text "幫我列出三個週末活動點子"
   # → {"thread_id":"…","task_id":"…","message_id":"…"}
   xbot chat wait_until_settled --task_id <task id> --timeout_s 120
   # → {"settled":true,"state":"done","verdict":"answer","text":"…"}
   ```

   - `settled: false`：等到逾時、Bot 還在做，再呼叫一次（每次最多 300 秒）。
   - `needs_person: true`：Bot 在等人（`request` 是它問的內容），到網頁上回覆。
   - 同一條對話串接著說：`chat send` 加 `--thread_id`；看整段對話：`xbot chat read --thread_id …`；停下來：`xbot chat stop --bot_id …`。
   - Bot 正在忙時 `chat send` 回 `conflict`（`retryable: true`）。

5. **需要核准的動作**：例如代理呼叫 `xbot.bots.delete` 會回 `pending_approval` 和 `needs_approval_id`，並在「待我處理」出一張卡。代理用 `xbot.approvals.wait --approval_id …` 等；你用 `xbot inbox list` 看卡片、`xbot approvals decide --approval_id … --decision allow`（或 `deny`）決定。預設只有你本人能決定。你可以授權某個代理「代為核准」（`xbot agents set_delegation`，或網頁「設定 → 進階 → 外部代理與 MCP」→ 代理 →「代為核准」），它就能用 `xbot.approvals.decide` 代你允許授權類別內的動作，受每日次數、金額、期限限制；能代為核准哪些動作見 [待我處理與核准](guide/04-inbox-approvals.md#代為核准讓代理幫你按允許)。刪除 Bot 永遠不能代為核准。

6. **查紀錄**：`xbot audit list` 列出誰、從哪裡、做了什麼（參數裡的密鑰已遮蔽）；代理只看得到自己的紀錄。

## 8. 從 tailnet 外連：內建 Tailscale 與外部入口（C35、MCP-2）

**內建 Tailscale**（預設關閉）：服務自己就是 tailnet 上的一台機器，不用另外裝 `tailscale serve`。

```sh
xbot network tailscale_set --enabled                       # 互動登入：xbot network status 會給 login_url，打開核准
xbot network tailscale_set --enabled --json - <<'JSON'     # 或給登入金鑰（加密存在服務端，之後不會再顯示）
{"enabled": true, "auth_key": "tskey-auth-…", "hostname": "xbot"}
JSON
xbot network status                                         # tailscale.url → https://xbot.<tailnet>.ts.net
```

- 狀態存在資料目錄的 `tailscale/`，重開服務不用再登入。tailnet 要開 MagicDNS 與 HTTPS Certificates（Tailscale 後台 DNS 頁），否則 `tailscale.cert` 是 `missing`。
- tailnet 來的請求**一律要登入碼或權杖**，永遠不會當成「這台電腦的你」（即使設了 `XBOT_MCP_LOCAL_OWNER=1`）。

**外部入口：Tailscale Funnel**（預設關閉；開啟要你本人確認，代理不能代為核准）：

```sh
xbot network funnel_set --enabled --allow_ips 203.0.113.7 --allow_ips 198.51.100.0/24   # 白名單可省略（不限 IP）
xbot network funnel_set --enabled=false
```

tailnet 的存取規則要允許這台用 Funnel（ACL 的 `nodeAttrs` 加 `"attr": ["funnel"]`）。開啟後網際網路上連得到同一個網址 `https://xbot.<tailnet>.ts.net`，但**只開放三條路徑**，其他（網頁、對話、`/api/v1/*` 的其他操作、SSE、模型代理）一律 404：

| 路徑 | 驗證 |
|---|---|
| `/mcp` | 每個請求都要代理權杖（`Authorization: Bearer xbot_…`）；不收登入碼、不認 cookie |
| `/api/v1/agents.pair` | 一次性配對碼（錯 10 次鎖 60 秒） |
| `/hooks/<id>` | 定時任務網址觸發的金鑰或簽章（網頁上的網址會自動換成對外網址） |

速率上限比 tailnet 內嚴：每個來源 IP 每分鐘 120 個請求（驗證前就擋）；每個代理經外部入口每分鐘最多 20 次呼叫（另一個計數，代理自己的上限更低時用它的）。稽核紀錄的來源標成 `mcp-external`。

配對外部代理：照 §1 產生配對碼，代理那台機器用對外網址換權杖：

```sh
XBOT_URL=https://xbot.<tailnet>.ts.net xbot mcp pair <配對碼>
claude mcp add --transport http xbot https://xbot.<tailnet>.ts.net/mcp --header "Authorization: Bearer xbot_你的權杖"
```

**另一種外部入口：你自己的反向代理**（nginx、Caddy、Cloudflare Tunnel…，自己負責 HTTPS）：

```sh
XBOT_PUBLIC_URL=https://xbot.example.com xbot serve          # 服務仍只綁 127.0.0.1
# 反向代理不在這台機器上時：XBOT_TRUSTED_PROXIES=10.0.0.5,10.0.1.0/24
```

從反向代理進來的請求（Host 是 `XBOT_PUBLIC_URL` 的網域，或從信任的代理來、帶 `X-Forwarded-For`）和 Funnel 一樣只開放上面三條路徑、要權杖、套白名單與比較嚴的速率上限；來源 IP 用信任代理標的最後一個 `X-Forwarded-For`。反向代理一定要帶 `X-Forwarded-For`。設了反向代理模式時，經 `tailscale serve`（外部的 Tailscale）進來的請求也會被當成外部；要 tailnet 內完整使用請改用內建 Tailscale。

