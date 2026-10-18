# XBot 實作計畫（交給 Agent 執行）

> 每個 Agent 開工前先讀 `AGENTS.md`，再讀自己的任務卡。
> 依據：`docs/PRD.md`、`docs/TECH-DESIGN.md`、`design/`（135 張畫板＋規格卡）。

---

## 0. 怎麼分給 Agent

### 4 條平行工作線
| 線 | 負責 | 主要目錄 |
|---|---|---|
| **核心（Core）** | 操作登錄表、資料庫、HTTP／MCP／CLI、權限、事件、待我處理 | `internal/ops`、`store`、`api`、`mcpserver`、`auth`、`events`、`inbox` |
| **電腦（Box）** | Bot 電腦映像、主機、SSH、Docker、看畫面 | `box/`、`internal/box`、`internal/hosts` |
| **引擎（Engine）** | Hermes 驅動、ACP 驅動、Model Proxy、Runtime | `internal/engine`、`proxy`、`runtime` |
| **前端（Web）** | 所有畫面 | `web/` |

M2 之後再加一條 **協作（Collab）**，負責 `internal/collab`、`org`、`limits`、`judge`。

### 執行原則
- **同時最多 4 個 Agent，每條線一個**。同一條線的卡依序做。
- 每張卡標有「依賴」；依賴的卡合併後才開始。
- 每個里程碑結束有一個**關卡（Gate）**，由人（或指定的驗收 Agent）照驗收步驟實際操作一遍，通過才進下一個里程碑。
- **風險優先**：M1 第一週先做 product.sock 經 SSH 反向轉發的原型（B11）。它不通，整個雲端主機設計要改。

### 任務卡格式
每張卡：**目標 → 讀這些 → 做這些 → 不要做 → 驗收**。Agent 照卡做，驗收的每一條都要能用指令或截圖證明。

---

## 1. 檢查點（Checkpoint）

一個階段一個階段做，每個檢查點由使用者親自檢查。**Agent 做到檢查點就停下**，交出 `docs/checkpoints/CPn.md`，等使用者回覆「通過」、「通過但要改 X」或「不通過」才繼續。

| 檢查點 | 時間點 | 使用者要看什麼 |
|---|---|---|
| **CP0** | C00＋C06 完成（約 3 天） | `make doctor`、`make setup`、`make check` 在乾淨環境跑得起來；專案結構與規範 |
| **CP1** | M0 結束（G0） | Claude Code 透過 MCP 建 Bot、對話，網頁同步出現；`make install && make start` 裝得起來 |
| **CP2** ⚠ | M1 第 1 週（B11） | 雲端主機連線原型報告。**決策點**：不通就改設計 |
| **CP3** | M1 中 | 本機 Bot 有電腦、看得到畫面、能接手；Claude Code 也能當大腦 |
| **CP4** | M1 結束（G1） | 遠端主機（`make host-add`）、`make deploy`、核准流程、花費上限 |
| **CP5** | M2 中 | 兩個 Bot 互相傳話，限制數字照 LazyBoy |
| **CP6** | M2 結束（G2） | 3 個 Bot 群組完成報告、團隊、代為核准 |
| **CP7** | M3 結束（G3） | 手機版與所有 P1 功能；`make update`、`make backup/restore` |

### CPn.md 固定格式
1. **示範步驟**：照做 5–10 分鐘就能親眼看到，不用讀程式。
2. **證據**：`make check` 輸出、測試結果、截圖（畫面要和畫板並排）。
3. **和規格不一樣的地方**與原因。
4. **需要使用者決定的事**（寫成選擇題）。
5. **下一階段計畫**。

規則：
- 每個檢查點打 git tag（`cp0`、`cp1`…），不滿意可以回到上一個檢查點。
- 等待回覆期間，Agent 只能做下一階段「不會被推翻」的準備（讀規格、寫測試草稿），不合併進主線。

---

## 2. Makefile 與部署（所有指令都要有）

`make` 是唯一入口：使用者和 Agent 都只用 make，不用記其他指令。make 只是薄薄一層，實際邏輯放在 `xbot` 執行檔的子指令（`xbot doctor`、`xbot host add`…），方便測試，也讓沒有 make 的主機也能用。

| 指令 | 做什麼 | 哪張卡 |
|---|---|---|
| `make help` | 列出所有指令與一句說明（預設目標） | C00 |
| `make doctor` | 檢查所需工具與環境：Go、Node、pnpm、Docker（版本 ≥ 26、目前使用者能不能用）、docker compose、git、ssh、Tailscale（選用）、磁碟空間、埠是否被占用。每一項顯示 ✅／❌，以及**怎麼修**的一行指令 | C00 |
| `make setup` | **自動安裝缺少的東西**：使用者層級的工具（Go、Node、pnpm、staticcheck、Playwright 瀏覽器）用 mise 直接裝；需要 sudo 的（Docker、Tailscale）先列出要執行的指令並詢問，或 `YES=1` 時直接執行。可重複執行（已裝的跳過）。最後自動跑 `make doctor` | C00 |
| `make deps` | 只裝專案相依套件（`go mod download`、`pnpm install`） | C00 |
| `make gen` | 由操作登錄表產生前端型別等程式碼 | C03 |
| `make dev` | 開發模式：後端熱重載＋前端 Vite | C00 |
| `make check` | gofmt、go vet、staticcheck、go test、前端 typecheck／lint／單元測試；全過才算完成 | C00 |
| `make test` / `make e2e` | 單元測試／Playwright 端對端測試（e2e 每次截圖也跑 axe 無障礙檢查，另有 `a11y.spec`、`keyboard.spec`） | C00 |
| `make bench` / `make bundle` | 後端效能基準／前端建置大小報告（數字記在 `docs/perf.md`） | v1.0 |
| `make test-docker` | Docker 整合測試（`go test -tags docker`）：真的開一台 Bot 電腦走完生命週期；也開一台測試用遠端主機（dind＋sshd）走 SSH 轉發（B11）；沒有 Docker 或映像時略過 | B02 |
| `make build` | 建出單一執行檔 `bin/xbot`（前端打包後嵌入），amd64 與 arm64 | C06 |
| `make box` | 建 Bot 電腦映像 | B01 |
| `make install` | 一鍵安裝到這台機器：`setup` → `build` → `box` → 初始化 `.data/`（或 `~/.local/share/xbot`）→ 安裝 systemd **使用者**服務 → 印出網址與登入碼 | C06 |
| `make start` / `stop` / `restart` / `status` / `logs` | 管理本機服務（systemd user service）；`status` 顯示服務、Bot 電腦、主機狀態 | C06 |
| `make up` / `down` | 另一種方式：用 docker compose 跑整套服務 | C06 |
| `make update` | 拉新版 → 重建 → 備份資料庫 → 執行 migration → 重啟 → 健康檢查；失敗自動回到上一版 | C06（M3 補完整） |
| `make backup` / `restore FILE=…` | 備份與還原資料（含資料庫與 Bot 資料，可選擇是否含登入資料） | C06 |
| `make deploy HOST=…` | 部署到另一台機器（例如雲端主機）：建出對應架構的執行檔 → 透過 SSH（Tailscale）複製 → 安裝或更新 systemd 服務 → 健康檢查；失敗自動回滾 | B12 |
| `make host-add HOST=…` | 把一台機器加入成 Bot 電腦主機：安裝 Docker、Tailscale，建 `xbot` 帳號、放公鑰，拉映像，做完自動檢查（等同 J11 的 3 步） | B12 |
| `make host-check HOST=…` | 檢查某台主機的狀態 | B12 |
| `make release` | 打包發佈檔（執行檔＋映像標籤＋版本說明） | v1.0 |
| `make clean` / `uninstall` | 清掉建置產物／移除服務（**保留資料**，除非 `PURGE=1` 並再次確認） | C06 |

共同規則：
- 每個指令都可以重複執行（冪等）。
- 失敗時印出白話原因與修正方法，不要只丟堆疊。
- 會刪資料或動到系統（sudo）的指令一定要先確認；`YES=1` 可略過確認，給 CI 與 Agent 使用。
- 在「乾淨的 Debian／Ubuntu／Arch 容器」裡跑 `make setup && make check` 要能通過，寫成 CI 測試。

---

## M0 骨架（約 2 週）

**關卡 G0**：在 Claude Code 裡加入 XBot MCP，呼叫 `xbot.bots.create` 建一個 Bot，再用 `xbot.chat.send` 加上 `xbot.chat.wait_until_settled` 得到回覆（Bot 用 Hermes＋一個 API 模型，跑在本機 Docker）。網頁上同時看得到這段對話即時出現。

### C00 環境與專案骨架 〔Core〕依賴：無
- **目標**：任何人或 Agent clone 下來，跑 `make setup` 就能把缺的東西裝好，接著 `make dev`、`make check` 都能用。
- **做這些**：
  - `mise.toml` 固定 Go（最新穩定版）、Node、pnpm 的版本（本機目前沒有 Go）。
  - `make help`、`doctor`、`setup`、`deps`、`dev`、`check`、`test`、`e2e`（規格見 §2）；`doctor` 與 `setup` 的邏輯寫在 `cmd/xbot` 的 `xbot doctor`／`xbot setup` 子指令裡。
  - `go.mod`；目錄照 TECH-DESIGN §2 建好，空模組放 `doc.go`。
  - `web/` 用 Vite＋React＋TypeScript＋Tailwind v4 初始化。
  - `Makefile`：`dev`、`check`（gofmt、go vet、staticcheck、go test、pnpm typecheck、pnpm lint）、`box`、`e2e`。
  - `docs/PROGRESS-IMPL.md` 空表格。
  - `.gitignore`，`.data/` 放執行資料。
- **驗收**：
  - 在乾淨的 Debian 容器裡跑 `make setup YES=1 && make check` 通過。
  - 在缺 Go 的環境跑 `make doctor`，會顯示 ❌ 和修正指令。
  - `make dev` 後 `curl localhost:<port>/healthz` 回 ok，瀏覽器開得到空白前端。

### C06 建置、安裝與本機服務 〔Core〕依賴：C00
- **讀**：PRD ON-1；LazyBoy `Makefile`、`INSTALL.md`（參考它的 install、start、up、logs）。
- **做**：
  - `make build`：前端打包嵌入 Go 執行檔，產出 amd64 與 arm64。
  - `make install`、`start`、`stop`、`restart`、`status`、`logs`：systemd 使用者服務。
  - `make up`、`down`：docker compose 版本。
  - `make backup`、`restore`、`clean`、`uninstall`；`make update` 先做最小版（重建＋migration＋重啟），M3 再補自動回滾。
- **驗收**：在乾淨的 VM 或容器裡，`make install` 之後開機自動啟動，`make status` 正確；`make backup` 後刪掉資料再 `make restore`，資料完整回來。

### C01 資料庫與 schema 〔Core〕依賴：C00
- **讀**：PRD §10 資料模型。
- **做**：
  - 開 SQLite（WAL、foreign keys），加上 migration 機制（嵌入 SQL 檔、版本表）。
  - 第一版資料表：bot、box、host、thread、message、task、event、audit_log、agent_token、approval、inbox_item、usage_entry、setting。其餘之後由各卡加 migration。
  - 每張表有對應的 Go 查詢函式與測試（用暫存 DB）。
- **不要做**：ORM。用 SQL＋小型 helper 就好。
- **驗收**：`go test ./internal/store/...` 通過；重跑 migration 不出錯（冪等）。

### C02 操作登錄表與中介層 〔Core〕依賴：C01
- **讀**：PRD §22（MCP-1～MCP-13）、TECH-DESIGN §3。
- **做**：
  - `ops.Register` 泛型定義：名稱、Scope（read/operate/manage/admin）、Risk（safe/confirm）、輸入輸出 struct（由 struct 產生 JSON Schema）、Handle。
  - 中介層依序處理：身分 → 權限範圍 → 資源範圍 → `request_id` 去重（同 id 回同結果）→ `dry_run` → 風險（confirm 先建立 approval 與 inbox_item，回 `pending_approval`）→ 執行 → 寫 audit（遮蔽密鑰）→ 發事件。
  - 錯誤格式固定為 `{code, message, retryable, needs_approval_id?}`（MCP-5）。
- **驗收**：單元測試涵蓋：權限不足、重送同一 request_id、dry_run 不寫資料、confirm 動作進待我處理、audit 有遮蔽。

### C03 HTTP 出口、SSE、前端型別 〔Core〕依賴：C02
- **做**：
  - 由登錄表產生 `POST /api/v1/<op>` 路由。
  - 單一 SSE 端點 `/api/v1/events`（依身分過濾、斷線續傳用 Last-Event-ID）。
  - 由登錄表產生 `web/src/api/generated.ts`（型別＋呼叫函式），`make gen` 指令。
  - 本機 localhost 免登入，其他要登入碼（SEC-7 的最小版）。
- **驗收**：新增一個測試操作，前端不用手寫就能呼叫；SSE 斷線重連不漏事件（測試）。

### C04 MCP 與 CLI 出口、代理配對 〔Core〕依賴：C02
- **讀**：MCP-2、MCP-6、MCP-7。
- **做**：
  - 用官方 MCP Go SDK，由登錄表產生工具，提供 Streamable HTTP（`/mcp`）與 stdio（`xbot mcp`）兩種入口。
  - 實作配對碼換權杖：一次性、5 分鐘有效，權杖記在 agent_token，可撤銷。
  - CLI：`xbot <domain> <action> --flags`，同樣由登錄表產生。
- **驗收**：寫一份 Claude Code 的 MCP 設定範例放 `docs/mcp-setup.md`，實際接上後 `tools/list` 看得到工具；權限不足的代理呼叫 manage 操作會被擋。

### C05 第一批核心操作 〔Core〕依賴：C03、C04、E02
- **做**：`bots.list/get/create/update/archive`、`chat.send/stop/read/list_threads/wait_until_settled`、`approvals.list/wait`、`inbox.list`、`audit.list`，共約 15～20 個。
- **驗收**：每個操作都有測試；用 CLI 跑一遍建 Bot → 對話 → 讀回覆。

### B01 Bot 電腦映像搬入 〔Box〕依賴：C00
- **讀**：LazyBoy `box/`、`Dockerfile`、`docker/`。
- **做**：
  - 把 LB 的 box 映像原樣搬到 `box/`，`make box` 建出 `xbot-box:dev`。
  - 移除 `product_mcp.py` 的掛載點，預留給 XBot MCP。
  - 確認 Hermes 版本固定（沿用 `HERMES_VERSION`）。
- **驗收**：`docker run` 後桌面、Chromium、noVNC（6080）、Hermes gateway 都起得來（寫一支 smoke test 腳本）。

### B02 本機 Box Manager 最小版 〔Box〕依賴：B01、C01
- **讀**：BX-1～BX-8、LB `server/computers.py`。
- **做**：用 Docker Go client 對本機 socket 建立、開機、停止、刪除 `xbot-box-<id>`；home、workspace、hermes 各用一個 volume；狀態寫回 box 表。
- **驗收**：Go 整合測試（需要 Docker，標記 `-tags docker`）建立 → 開機 → 停止 → 重開，資料保留。

### E01 Model Proxy 最小版 〔Engine〕依賴：C01
- **讀**：EN-3、LB 的代理相關程式（`server/main.py`、`box/hermes_host.py`）。
- **做**：
  - 支援一個 OpenAI 相容供應商（金鑰放在設定檔或 setting 表，加密存放）。
  - 每個 Bot 一把 worker-key（HMAC），只允許 chat、responses、messages、models 這幾個路徑。
  - 用量寫進 usage_entry。
- **驗收**：用 worker-key 打得通，沒有 key 或用錯 key 會被拒；容器的環境變數裡看不到真正的金鑰（測試檢查）。

### E02 Hermes 驅動最小版 〔Engine〕依賴：E01、B02
- **讀**：LB `server/runtime.py`、`server/rpc.py`（Hermes JSON-RPC 的用法）、TECH-DESIGN §4。
- **做**：
  - 定義 `engine.Driver` 介面與正規化事件型別。
  - Hermes 驅動：連進容器內的 Hermes gateway，處理送訊息、串流事件、中斷。
  - 事件寫入 message 和 event，並推上 SSE。
- **驗收**：整合測試：對 Bot 說「回覆 OK」，收到串流文字和完成事件。

### W01 前端殼與設計系統 〔Web〕依賴：C00
- **讀**：`design/N01-Tokens`、`N02-Mascot`、`N03-Components`、`Sidebar`、`Main` 畫板與規格卡。
- **做**：
  - 把 Tailwind tokens 換成 Grok Midnight 色票，字型用粉圓（自己 host）。
  - `Avatar` 元件：平面、只有眼睛、10 種形狀、10 色、6 種眼睛、狀態小點。直接移植 `design/Avatar.dc.html` 裡的 SVG 路徑。
  - 基本元件：按鈕、輸入框、膠囊按鈕、卡片、切換開關、分段控制、對話框、Toast，以及 lucide 圖示（stroke 1.75）。
  - 側欄＋主畫面版型。
- **驗收**：`/dev/components` 頁面列出所有元件；Playwright 截圖和 N02、N03 並排看起來一致。

### W02 對話畫面最小版 〔Web〕依賴：W01、C03
- **讀**：`design/C01`～`C05`、`Main`、`Sidebar`。
- **做**：Bot 清單、建 Bot（先做最簡表單）、對話串流、輸入框、停止按鈕；全部用生成的 API 和 SSE。
- **驗收**：G0 的網頁部分。

---

## M1 一個 Bot＋自己的電腦（約 4 週）

**關卡 G1**：
1. 在 XBot 新增一台遠端 Docker 主機（照 J11 的 3 步流程）。
2. 建一個放在那台主機上的 Bot，請它「用瀏覽器查三家競品價格，做成表格」；能在網頁上即時看它的畫面、接手操作再交還。
3. 同一件事改用 Claude Code（使用者同意掛載訂閱）也能完成。
4. 過程中 Bot 想對外發送時，會出現在「待我處理」，你按允許後才發出。

### B11 ⚠ 風險原型：SSH 反向轉發 product.sock 〔Box〕依賴：B02（**M1 第一週優先**）
- **讀**：PRD §16.2、TECH-DESIGN §5。
- **做**：用 `x/crypto/ssh` 連遠端主機，把遠端的 Unix socket 反向轉發回本機服務；同一條連線上也轉發 noVNC 和 Docker API。
- **驗收**：寫一份實測報告 `docs/spike-ssh-forward.md`：連線斷了能不能自動重連、延遲多少、同時 10 個 Bot 的表現。**結果不理想就停下來回報，不要硬做下去。**

### B12 主機管理 〔Box〕依賴：B11
- **讀**：CL-1～CL-10、J06、J11、E10 畫板。
- **做**：
  - 新增主機：產生一行安裝指令（裝 Docker、Tailscale、建 xbot 帳號、放公鑰、拉映像）、自動檢查、取名、設上限。
  - 主機清單與健康檢查；主機離線時的處理（CL-6）。
  - 操作：`hosts.list/add/remove(confirm)/status`。
  - `make host-add HOST=…`、`make host-check HOST=…`、`make deploy HOST=…`（規格見 §2）。
- **驗收**：
  - 在一台真實的遠端主機上走完 3 步，主機清單顯示正確的資源用量。
  - `make host-add` 對同一台主機跑兩次不會出錯。
  - `make deploy` 到一台雲端主機後服務正常；故意部署壞掉的版本會自動回滾。

### B13 Box 生命週期與強化 〔Box〕依賴：B12
- **做**：閒置 30 分鐘休眠、有工作自動叫醒、重建（保留資料）；照 SEC-3、SEC-4 強化容器；`computers.status/wake/sleep/restart`。
- **驗收**：測試涵蓋休眠和叫醒；容器 inspect 結果符合 SEC-3 清單。

### B14 看畫面與接手 〔Box〕＋〔Web〕依賴：B13
- **讀**：E01、E02、E10、L04 畫板；LB 的 noVNC 皮膚。
- **做**：noVNC 經服務代理（只對已登入的使用者開放）；接手時暫停 Bot、交還時恢復。
- **驗收**：G1 第 2 步。

### E11 Model Proxy 完整版 〔Engine〕依賴：E01
- **讀**：EN-3～EN-7、US-1～US-3、§6.13。
- **做**：多個供應商、模型自動探測（EN-4）、每個 Bot、每日、每月花費上限、超過上限時進待我處理、用量查詢操作。
- **驗收**：設 1 元上限的 Bot 超過時會停下並出現卡片。

### E12 ACP 驅動與 Claude Code 〔Engine〕依賴：E02、B13
- **讀**：EN-2、EN-2a、EN-2b；OpenMausBot `server/drivers/acp*`、`claude*.ts`（只看行為）。
- **做**：
  - ACP 用戶端（JSON-RPC over stdio，經 docker exec）。
  - Claude Code 和 Codex CLI 預先裝進映像。
  - 憑證掛載要使用者逐 Bot 同意，可收回，介面上有標示。
  - 把 desktop_mcp 和 XBot MCP 掛給 CLI 使用。
- **驗收**：G1 第 3 步；收回同意後 Bot 不能再用該訂閱。

### E13 Hermes 訂閱登入驗證 〔Engine〕依賴：E11
- **做**：驗證 Hermes 的 `openai-codex`（ChatGPT 方案）OAuth 能否走 Model Proxy，讓權杖不進容器。
- **驗收**：報告 `docs/spike-hermes-oauth.md`。做不到就列出替代方案，交給人決定。

### C11 Bot 用的 XBot MCP 〔Core〕依賴：C04、B11
- **讀**：MCP-2（Bot 經 product.sock）、LB `box/product_mcp.py`。
- **做**：product.sock 上的 MCP 伺服器，身分就是那個 Bot；先開放卡片、檔案、要求核准幾個操作。
- **驗收**：Bot 在對話中能發出核准卡。

### C12 待我處理與核准 〔Core〕＋〔Web〕依賴：C05
- **讀**：IN-1～、SEC-1/2/10、B04、F08、C08 畫板。
- **做**：待我處理清單、核准卡（允許這次、退回、復原）、通知事件。
- **驗收**：G1 第 4 步。

### W11 對話完整版 〔Web〕依賴：W02、C12
- **讀**：C01～C14 畫板與規格卡。
- **做**：Markdown、程式碼、表格、工作步驟摺疊、各種卡片、模型選擇、進度與上下文。
- **驗收**：每張 C 畫板都有對應截圖比對。

### W12 電腦與設定頁 〔Web〕依賴：B12、E11
- **讀**：E01～E10、J01～J06、J11 畫板。
- **驗收**：截圖比對。

---

## M2 Bot 互聊與團隊（約 4 週）

**關卡 G2**：建 3 個 Bot 的群組，請它們合作完成一份研究報告。過程中：
- 往來次數、輪數、字數限制都照 LB 的數字生效（測試證明）；
- 你在群組裡講話會立刻插隊；
- 迴圈會被停下；
- 任務完成判定正確（完成、要人、交回、只承諾）。

### X01 限制常數 〔Collab〕依賴：C00
- **讀**：PRD §12、§18；**以 LB DEV 程式為準**：`server/collab.py`、`org.py`、`runtime.py`。
- **做**：所有數字集中在 `internal/limits`，每個常數註解它來自 LB 的哪個檔案、哪一行。
- **驗收**：對照表 `docs/limits.md`，列出 PRD 和程式不一致的地方。

### X02 Bot 傳話與群組 〔Collab〕依賴：X01、C11、E02
- **讀**：AA-*、RM-*、LP-*；LB `server/collab.py`；D01～D05、C13 畫板。
- **做**：把 collab.py 改寫成 Go，**LB 的對應測試全部翻成 Go 測試**。
- **驗收**：翻譯過來的測試全過，再加上 G2 的情境測試。

### X03 團隊、匯報線、提案、議程 〔Collab〕依賴：X02
- **讀**：TM-*；LB `org.py`、`agenda.py`；F01～F10、H04 畫板。

### X04 任務完成判定 〔Collab〕依賴：X02
- **讀**：PRD §19；LB 的 NEEDS_PERSON、HANDS_BACK、PROMISE 規則。
- **做**：先只用規則；預留 judge 介面（X05 再接快判）。

### C21 代為核准與外部 MCP 權限 〔Core〕依賴：C12
- **讀**：MCP-8～MCP-12；J12、C16 畫板。
- **做**：代為核准授權（類別、每日上限、期限）、永遠要本人的清單、10 分鐘內可復原。
- **驗收**：測試證明刪除類動作無論如何都不能代為核准。

### W21 群組與團隊畫面 〔Web〕依賴：X02、X03
- **讀**：D、F、H04 畫板。

---

## M3 補齊（約 4 週）

**關卡 G3**：PRD 所有 P1 需求都有對應的實作和測試，手機 PWA 走完 L01～L21。

| 卡 | 線 | 內容 | 讀 |
|---|---|---|---|
| W31 | Web | 手機 PWA（L01～L21）、安裝、推播 | L 畫板、MB-* |
| X05 | Collab | 快判：規則加 Jev，先開影子模式，每個用途各自開關 | §20、J10、P05、P06、C15、L21 |
| C31 | Core | 技能、記憶 | SK-*、MM-*、G05～G07 |
| C32 | Core | 定時任務、Webhook、觸發 | RT-*、H01～H06 |
| C33 | Core | 檔案 | FL-*、E07、M01、L19 |
| C34 | Core | 連接應用程式與登入 | I01～I05 |
| B31 | Box | 搬家、exit node | CL-5、CL-7 |
| B32 | Box | 教 Bot 操作（teaching） | E04、E05；LB `teaching.py` |
| C35 | Core | tsnet 內建、Funnel 外部 MCP 入口 | §17、MCP-2 |
| E31 | Engine | Gemini CLI、OpenCode 等 ACP 引擎、引擎額度用完換引擎 | EN-2、EN-7 |
| W32 | Web | 入門流程（A01～A10）、Bot 設定（G01～G14） | A、G 畫板 |
| E32 | Engine | 語音 P1 | VC-*、K01、K02、L18 |

## v1.0（約 2 週）
效能、無障礙、安裝文件、從 OMB 和 LB 匯入（BT 匯入相關需求）、§3.2 指標。

---

## 給派工的人：每張卡的提示範本

```
你負責 XBot 任務卡 <卡號>。
1. 先讀 /home/daniel/Projects/XBot/AGENTS.md 和 docs/PLAN.md 裡 <卡號> 那一段。
2. 照卡片的「讀這些」讀完規格，開分支 task/<卡號>-<短名>。
3. 只做卡片範圍內的事；完成時 make check 要全過，並附上驗收證據（測試輸出、截圖）。
4. 更新 docs/PROGRESS-IMPL.md；有和規格不一致或不確定的地方，列在 PR 說明。
```

## 依賴總覽

```
C00 ─┬─ C06
     ├─ C01 ─ C02 ─┬─ C03 ─┐
     │             └─ C04 ─┼─ C05 ── C12 ── C21
     ├─ B01 ─ B02 ─┬─ B11 ─ B12 ─ B13 ─ B14
     │      E01 ───┼─ E02 ─────────── E12
     │             │   └ E11 ─ E13
     ├─ W01 ─ W02 ─ W11 / W12
     └─ X01 ─ X02 ─ X03 / X04 ─ (M3) X05
```
