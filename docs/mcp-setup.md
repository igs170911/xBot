# 把代理接上 XBot MCP

XBot 的每個功能都是一個操作（`xbot.<領域>.<動作>`），同一份定義產生 MCP 工具、HTTP API 和 `xbot` 指令列（PRD MCP-1、MCP-2）。外部代理（Claude Code、Codex、Cursor…）有兩種接法：

| 入口 | 網址／指令 | 什麼時候用 |
|---|---|---|
| Streamable HTTP | `http://127.0.0.1:8780/mcp`（tailnet 內：`https://xbot.<tailnet>.ts.net/mcp`） | 代理和服務在不同機器，或用戶端支援 HTTP MCP |
| stdio | `xbot mcp` | 用戶端只支援 stdio；或在服務那台機器上用 |

兩個入口都要**代理權杖**（用配對碼換來，見下）。stdio 只是把 stdin／stdout 接到服務的 `/mcp`，所有呼叫一樣在服務裡執行、記稽核、走核准。

## 1. 配對：產生配對碼 → 換權杖（MCP-6）

1. **你（使用者）** 產生配對碼，決定這個代理能做什麼（權限可疊加：`read` 查看、`operate` 交辦工作、`manage` 管理 Bot／團隊、`admin` 系統設定）：

   ```sh
   xbot agents pair_start --name "Claude Code" --scopes read --scopes operate
   # 只讓它碰某幾個 Bot：--resources.bots <bot id> --resources.bots <bot id>
   # 權杖 90 天後失效：--token-days 90
   ```

   會得到 `{"code": "RY3F-Z6YP", "expires_at": …}`。配對碼**只能用一次、5 分鐘內有效**。之後網頁的「設定 → 已連線的代理」也會有同一個按鈕。只有你本人能產生配對碼或改代理權限；代理就算有 `admin` 也不行。

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
