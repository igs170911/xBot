# XBot 使用手冊

一個主題一份，照「想做什麼」來找。畫面上的路徑寫成「設定 → AI 模型」這樣。

| 想做什麼 | 看這份 |
|---|---|
| 安裝、更新、備份、還原、移除、部署到另一台 | [安裝與更新](01-install.md) |
| 裝好後第一次打開 | [第一次使用](02-first-run.md) |
| 建 Bot、交代工作、看進度、停下來 | [Bot 與對話](03-bots-chat.md) |
| Bot 問你能不能做某件事；讓代理幫你核准 | [待我處理與核准](04-inbox-approvals.md) |
| 看 Bot 的畫面、接手、教它、休眠、搬家、從家裡出去 | [Bot 的電腦](05-computer.md) |
| 把雲端主機（VPS）加進來放 Bot 的電腦 | [雲端主機](06-cloud-hosts.md) |
| 讓幾個 Bot 一起做事、建團隊 | [群組與團隊](07-groups-teams.md) |
| 每天固定做的事、外部服務觸發 | [定時任務與 Webhook](08-routines-webhooks.md) |
| 上傳、預覽、下載 Bot 的檔案 | [檔案](09-files.md) |
| 教 Bot 做法、讓它記住事情 | [技能與記憶](10-skills-memory.md) |
| 接上模型、看花費、設上限、快速判斷 | [模型與花費](11-models-spend.md) |
| 用 Claude Code、Codex、Gemini CLI 等當 Bot 的大腦；額度用完怎麼辦 | [引擎](12-engines.md) |
| 讓 Bot 用 GitHub、Notion 等服務 | [連接應用程式](13-apps.md) |
| 在手機上用、收推播 | [手機](14-phone.md) |
| 讓 Claude Code 等代理指揮 XBot | [外部代理與 MCP](15-agents-mcp.md) |
| 從 LazyBoy 或 OpenMausBot 搬過來（指令列） | [匯入舊資料](16-import.md) |
| XBot 怎麼保護你的金鑰、主機、區網 | [安全模型](17-security.md) |
| 出問題了 | [疑難排解](18-troubleshooting.md) |

管理者另外看 [管理手冊](../ADMIN.md)；這一版有哪些限制，看 [v1.0 版本說明](../RELEASE-NOTES-v1.0.md)。

## 幾個名詞

- **Bot**：一個 AI 同事。有名字、職稱、頭像、技能、記憶，還有自己的電腦。
- **Bot 的電腦**：一個 Docker 容器，裡面有桌面、Chromium、終端機。Bot 在裡面做事，你可以看畫面或接手。
- **主機**：放 Bot 電腦的機器。可以是這台，也可以是你自己的雲端主機。
- **引擎**：Bot 用來思考和動手的程式。預設是 Hermes；也可以用 Claude Code、Codex、Gemini CLI、OpenCode、Grok。
- **待我處理**：所有要你決定的事都在這裡，例如付款前的允許、Bot 的提問。
- **權限等級**：Bot 可以自己做多少事。新 Bot 一律從「先問我」開始。
- **代理**：外部的 AI 工具（Claude Code、Codex、Cursor…），經 MCP 操作 XBot。
