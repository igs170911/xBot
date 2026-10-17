# XBot 技術設計

> 狀態：文件階段（2026-10-18），尚未寫程式。對應 PRD §9、§13、§16–§22。
> 決定：**後端 Go、前端 React＋TypeScript**；不 fork OpenMausBot（OMB），只當功能參考；LazyBoy（LB）的 box 映像與容器內 Python 工具沿用；訂閱 CLI 採**方案 A**（跑在 Bot 容器內）。

---

## 1. 語言與技術選型

| 層 | 選擇 | 為什麼 |
|---|---|---|
| 後端 | Go（單一執行檔） | 主要工作是等待與協調（模型、Docker、SSH、串流），不是重運算；goroutine 適合「每個 Bot 一條工作線」；部署只要一個檔案 |
| HTTP | 標準庫 `net/http`（Go 1.22+ 路由）或 chi | 不需要大框架 |
| MCP | 官方 MCP Go SDK（`modelcontextprotocol/go-sdk`） | Streamable HTTP＋stdio |
| Docker | 官方 Docker Go client | 本機 socket 與 `ssh://xbot@主機` 用同一套 |
| SSH | `golang.org/x/crypto/ssh` | 埠轉發、Unix socket 反向轉發（product.sock） |
| Tailscale | `tailscale.com/tsnet` | 服務本身加入 tailnet：HTTPS 憑證、Funnel 外部入口，不用另外設定 |
| 資料庫 | SQLite（WAL），純 Go driver（例：`modernc.org/sqlite`） | 免 C 編譯器、單檔備份 |
| 排程 | Go cron 套件（例：`robfig/cron`） | 定時任務 |
| 設定與驗證 | JSON Schema 由 Go struct 產生 | 同一份定義給 MCP、HTTP、CLI |
| 前端 | React 19＋Vite＋Tailwind v4＋lucide＋TypeScript | 照現在的模式；字型粉圓；頭像照 N02 |
| 前端 API 型別 | 由後端的操作定義產生 TypeScript 型別 | 前後端不會對不起來 |
| 手機 | PWA | 之後要上架再包 Capacitor |
| Bot 的電腦 | LB `box/` Docker 映像 | 已驗證；容器內 Python（desktop_mcp、hermes_host）不改寫 |
| 測試 | Go `testing`＋Playwright（前端 E2E） | LB 的 pytest 案例翻成 Go 測試，保證行為一致 |

套件名稱在 M0 開始時再確認版本與維護狀態。

---

## 2. 專案結構（monorepo）

```
xbot/
  cmd/
    xbot/            服務＋CLI 同一個執行檔（xbot serve / xbot mcp / xbot bots list …）
    xbot-hostd/      （選用）遠端主機上的小幫手；v1 先不用，直接走 SSH＋Docker
  internal/
    ops/             操作登錄表：每個操作的名稱、輸入輸出、權限、風險、處理函式
    api/             HTTP 出口（由 ops 產生路由）＋ SSE 事件流
    mcpserver/       MCP 出口（由 ops 產生工具）＋ product.sock 給 Bot 用
    auth/            登入、裝置配對、代理權杖、權限範圍、代為核准授權
    store/           SQLite schema、migration、查詢
    events/          事件匯流排（給 SSE、MCP 資源通知、稽核）
    runtime/         任務佇列、看門狗、優先序、完成判定（§19）
    engine/
      driver.go      共同介面
      hermes/        Hermes 驅動（JSON-RPC）
      acp/           ACP 驅動（Claude Code、Codex CLI、Gemini CLI、OpenCode…）
    proxy/           Model Proxy：金鑰、OAuth 權杖、worker-key、用量帳本、花費上限
    hosts/           主機清單、加入主機、健康檢查、SSH 連線池、tsnet
    box/             Bot 電腦：建立、休眠、喚醒、重建、搬家、noVNC 轉發
    collab/          Bot 傳話、群組、迴圈防護、§18 限制
    org/             團隊、匯報線、核准、提案、議程
    routines/        定時任務、Webhook、觸發
    inbox/           待我處理、通知
    skills/ memory/ files/ apps/   對應 PRD 6.6–6.9
    judge/           快判（§20）：規則、Jev、影子模式
    limits/          所有固定數字（§12、§18）集中一處
  box/               LB 映像（Dockerfile、desktop_mcp.py、hermes_host.py、start-*.sh…）
  web/               前端（React＋TS）
  docs/
```

---

## 3. 操作登錄表（MCP 優先的核心）

每個功能只定義一次：

```go
ops.Register(ops.Op[CreateBotIn, CreateBotOut]{
    Name:   "xbot.bots.create",
    Scope:  ops.Manage,          // read / operate / manage / admin
    Risk:   ops.Safe,            // safe / confirm
    Handle: bots.Create,
})
```

由這份定義自動產生：
- **HTTP**：`POST /api/v1/bots.create`（前端只呼叫這個）
- **MCP 工具**：`xbot.bots.create`（外部代理、Bot 自己）
- **CLI**：`xbot bots create --name …`
- **前端 TypeScript 型別**

共用中介層依序處理：身分 → 權限範圍 → 資源範圍 → `request_id` 去重 → `dry_run` → 風險（`confirm` 轉「待我處理」，除非有代為核准授權且類別允許）→ 執行 → 稽核 → 發事件。

規則：「介面能做的，MCP 都能做」。前端不得呼叫登錄表以外的 API。

---

## 4. 引擎（agent harness）

XBot 不自己寫 agent harness。Go 後端只寫驅動，把各引擎的輸出正規化成同一種事件：

```go
type Driver interface {
    Start(ctx, bot, thread) (Session, error)
    Send(ctx, Session, Message) error
    Interrupt(ctx, Session) error
    Events(Session) <-chan Event   // text, thinking, tool_call, tool_result, needs_approval, done, error, usage
}
```

| 驅動 | 引擎 | 跑在哪 | 怎麼通 |
|---|---|---|---|
| `hermes` | Hermes Agent（預設） | Bot 容器內 | Hermes 原生 TUI gateway 雙向 JSON-RPC（LB 現行做法，不改 Hermes 核心） |
| `acp` | Claude Code、Codex CLI；P1：Gemini CLI、OpenCode、Grok | Bot 容器內（方案 A） | ACP（JSON-RPC over stdio），經 `docker exec` 或容器內小代理 |

注意：Hermes 的 `acp_adapter` 是「讓別人驅動 Hermes」，方向相反，不能用來驅動 Claude Code。

### 4.1 訂閱與金鑰

| 你有的 | 引擎 | 金鑰／權杖放哪 |
|---|---|---|
| API 金鑰（各家） | Hermes | 只在 Model Proxy；容器只有 worker-key |
| ChatGPT 方案 | Hermes（`openai-codex` OAuth）或 Codex CLI | Hermes 用：權杖由服務端保管（**待驗證**能否經代理）；Codex CLI：方案 A |
| Claude 訂閱 | **只用 Claude Code** | 方案 A：逐 Bot 同意、唯讀掛載 |
| Copilot、Nous Portal、Qwen、xAI、MiniMax | Hermes 各自 OAuth | 同 ChatGPT（Hermes） |
| OpenCode Go／Zen | Hermes（`opencode-go`／`opencode-zen`） | Model Proxy |
| 本機模型 | Hermes（Ollama、LM Studio、vLLM） | 不需要 |

不讓 Hermes 借用 Claude Code 的登入憑證（Anthropic 條款風險，PRD §14）。

### 4.2 方案 A：訂閱 CLI 在 Bot 容器內

- 憑證檔案逐 Bot 經你同意後唯讀掛載，可隨時收回；介面標示「這個 Bot 用你的 Claude 訂閱」。
- 容器強化照 SEC-3／SEC-4（cap-drop、no-new-privileges、禁止私有網段）。
- 換訂閱或收回：重建該 Bot 容器的掛載，不影響 home／workspace。

### 4.3 工具

不論引擎，工具都經 MCP：
- **desktop_mcp**（容器內 Python，沿用）：桌面、瀏覽器（CDP）、終端機、檔案。
- **XBot MCP**（經 product.sock，身分＝該 Bot）：傳話、群組、待我處理、卡片、檔案交換。取代 LB 的 `product_mcp.py`。
- 技能用共通 `SKILL.md`，Hermes 與 Claude Code 都讀得懂。

---

## 5. Bot 的電腦與主機

- **主機**：本機用 Docker socket；遠端用 `ssh://xbot@<tailnet 名稱>`。現階段先用一台現成的 Docker 主機快速驗證，正式使用時換成專用主機（PRD §16.1a）。
- **連線**：一條 SSH 連線同時處理：Docker API、noVNC（6080）埠轉發、Hermes 埠（8642）轉發、product.sock 反向轉發（Unix socket）。
- **生命週期**：建立 → 開機 → 閒置 30 分鐘休眠（`docker stop`）→ 有工作自動叫醒；重建保留 home／workspace／hermes。
- **搬家**：停止 → 經 SSH 打包 → 新主機還原 → 登入狀態跟著走。
- **待驗證（M1 第一週做原型）**：遠端容器經 SSH 反向轉發的 product.sock 是否穩定、延遲多少。

---

## 6. 即時事件

- 前端只開**一條 SSE**：所有 Bot 狀態、訊息串流、待我處理、通知都走它。
- MCP 用資源更新通知＋`chat.wait_until_settled`，代理不用輪詢。
- 事件同時寫入稽核表（遮蔽密鑰）。

---

## 7. 從 LazyBoy 搬什麼

| LazyBoy（Python） | XBot（Go） | 做法 |
|---|---|---|
| `box/`（Dockerfile、desktop_mcp、hermes_host、start-*.sh…） | `box/` | **原樣沿用** |
| `box/product_mcp.py` | `internal/mcpserver` | 由 XBot MCP 取代 |
| `runtime.py`、`scheduling.py` | `runtime/`、`routines/` | 改寫 |
| `collab.py`、`org.py`、`agenda.py` | `collab/`、`org/` | 改寫；測試案例一起翻 |
| `computers.py`、`teaching.py`、`logins.py`、`cleanup.py` | `box/`、`hosts/` | 改寫，加上遠端主機 |
| `routing.py`、`model_check.py`、`usage.py` | `proxy/`、`judge/` | 改寫 |
| `skills.py`、`memory.py`、`files.py`、`connectors.py`、`sharing.py` | 對應模組 | 改寫 |
| `store.py`、`config.py`、`tls.py`、`serve.py`、`main.py` | `store/`、`cmd/`、tsnet | 改寫（TLS 由 tsnet 取代） |
| `routing/`（本機意圖分類服務） | `judge/` | 先用規則＋Jev；本機分類器 P2 |
| `web/`（React） | `web/` | 重做成 Grok 風格，參考 135 張畫板 |

## 8. 從 OpenMausBot 參考什麼

OMB 的 TypeScript 程式不搬，只參考它的**行為與規格**：
- 聊天介面、卡片（核准、問題、登入、執行紀錄）、Markdown 呈現
- 技能、記憶、定時任務、連接應用程式市集
- 驅動事件正規化的做法（`server/drivers/`）、ACP 驅動
- 手機版互動、通知

---

## 9. 待驗證（實作階段）

| 項目 | 何時 |
|---|---|
| product.sock 經 SSH 反向轉發的穩定度與延遲 | M1 第 1 週 |
| Hermes 的 OAuth 供應商能否經 Model Proxy，權杖不進容器 | M1 |
| Claude Code／Codex CLI 的 ACP 介面在容器內的行為（中斷、核准、MCP 掛載） | M1 |
| OpenCode Zen／Go 的 API 端點與 Jev 選擇題格式 | M3（快判） |
| tsnet Funnel 當 MCP 外部入口 | M3 |
| Go 套件版本與維護狀態 | M0 開始時 |
