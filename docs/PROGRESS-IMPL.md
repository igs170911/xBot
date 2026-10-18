# XBot 實作進度

每張任務卡完成時更新一列。卡片內容見 `docs/PLAN.md`。

| 卡 | 狀態 | 分支／commit | 和規格不一樣的地方 | 後續事項 |
|---|---|---|---|---|
| C00 環境與專案骨架 | ✅ 完成 | `task/C00-skeleton` | 和 C06 放在同一個分支（兩張卡都改 Makefile）；開發埠改 8782／5181（5173 被本機其他專案占用、8780 留給安裝版）；TypeScript 固定 6.x（typescript-eslint 還不支援 7） | — |
| C06 建置、安裝與本機服務 | ✅ 完成 | `task/C00-skeleton` | `make update` 已含自動回滾（原計畫 M3 才補）；`make install` 暫不建 Bot 電腦映像（B01 才有） | B01 完成後 `make install` 要加上 `make box` |
| B01 Bot 電腦映像 | ✅ 完成 | `task/B01-box` | Hermes 在建置時抓固定 commit（不放進 git）；`make install` 會一併 `make box`（首次約 10 分鐘）；未移植 Debian 鏡像測速 | B02 要用新的環境變數名稱 `XBOT_UID`／`XBOT_GID`／`XBOT_SCREEN_*`；容器內接 XBot MCP 的方式待 C11 決定 |
