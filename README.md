# XBot

你自己的 AI 工作團隊：每個 Bot 都有自己的電腦，彼此會互相討論、分工、回報；你在一個像通訊軟體的介面裡指派、觀看、接手、核准。

合併自 OpenMausBot（產品骨架、多引擎、技能、記憶、排程、連接 App）與 LazyBoyHermes（地端 Bot 電腦、Agent 互聊），並重新設計 UI/UX。

- 產品需求：[docs/PRD.md](docs/PRD.md)
- 技術設計：[docs/TECH-DESIGN.md](docs/TECH-DESIGN.md)
- 實作計畫（給 Agent）：[docs/PLAN.md](docs/PLAN.md)；Agent 工作守則：[AGENTS.md](AGENTS.md)
- UI/UX 設計（Claude Design）：https://claude.ai/artifact/DmfRHh7dM9EsSLsMVkHYS6
- 設計原始檔：[design/](design/)

## 開始使用

只需要先有 `make`，其他缺的東西 `make setup` 會自動補齊（需要 sudo 的會先問你）。

```bash
make setup     # 補齊工具（Go、Node、pnpm…）
make install   # 建置並安裝成開機自動啟動的服務 → http://127.0.0.1:8780
make status    # 看狀態
```

開發：`make dev`（前端 http://localhost:5181）、`make check`。所有指令：`make help`。
