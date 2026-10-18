# XBot

你自己的 AI 工作團隊。每個 Bot 都有自己的電腦（一個 Docker 容器，裡面有桌面和瀏覽器），會用網頁、寫程式、整理檔案。Bot 之間會互相討論、分工、回報。你在一個像通訊軟體的介面裡交代工作、看它們做事、需要時接手，花錢或對外發送這類事一定先問你。所有功能也能用 MCP 和指令列操作，所以 Claude Code、Codex 等代理也能指揮你的 Bot。

![對話中，Bot 要付款前先等你允許](docs/images/chat-approval.png)

| 辦公室：每個 Bot 的電腦 | 手機（PWA） |
|---|---|
| ![辦公室](docs/images/office.png) | ![手機的對話清單](docs/images/phone-chats.png) |

## 快速開始

需要一台 Linux 電腦（amd64 或 arm64），先有 `make`。其他工具 `make setup` 會補齊；要用 sudo 的（例如 Docker）會先問你。

```bash
make setup     # 補齊工具：Go、Node、pnpm、Docker…
make install   # 建置 → 建 Bot 電腦映像（第一次約 10 分鐘）→ 裝成開機自動啟動的服務
```

裝好後打開 http://127.0.0.1:8780 。第一次會請你接上一個 AI（訂閱、API 金鑰或本機模型），再建第一個 Bot。

- **區網其他裝置**：服務預設聽 `0.0.0.0:8780`，安裝完會印出區網網址。從別台裝置打開要輸入**登入碼**（`make token` 顯示）。只想本機用：`XBOT_BIND=127.0.0.1 make install`。
- **手機**：建議用 Tailscale 的 https 網址。區網的 http 網址不能安裝成 App，也收不到推播（瀏覽器規定）。做法見 [手機](docs/guide/14-phone.md)。

常用指令：`make status`、`make logs`、`make stop`／`make start`、`make backup`、`make update`、`make doctor`。全部指令：`make help`。

## 文件

- [使用手冊](docs/guide/README.md)：安裝、第一次使用、每個功能怎麼用、疑難排解
- [管理手冊](docs/ADMIN.md)：資料目錄、埠號、環境變數、備份與升級、對外開放前的安全檢查
- [v1.0 版本說明](docs/RELEASE-NOTES-v1.0.md)：功能、已知限制、已做的決定
- [把代理接上 XBot MCP](docs/mcp-setup.md)

給開發者：

- 產品需求 [docs/PRD.md](docs/PRD.md)、技術設計 [docs/TECH-DESIGN.md](docs/TECH-DESIGN.md)
- 實作計畫 [docs/PLAN.md](docs/PLAN.md)、進度 [docs/PROGRESS-IMPL.md](docs/PROGRESS-IMPL.md)、Agent 工作守則 [AGENTS.md](AGENTS.md)
- 限制值對照 [docs/limits.md](docs/limits.md)、UI/UX 設計原始檔 [design/](design/)（線上版：https://claude.ai/artifact/DmfRHh7dM9EsSLsMVkHYS6）
- 開發：`make dev`（前端 http://localhost:5181）、`make check`（完成前一定要過）

XBot 合併自 OpenMausBot（產品骨架、多引擎、技能、記憶、排程、連接應用程式）與 LazyBoyHermes（地端 Bot 電腦、Agent 互聊），並重新設計介面。
