# CP7＋v1.0：M3 補齊與 v1.0 收尾（關卡 G3）

> 依使用者「放手做」的指示，檢查點不停下等待；報告與 tag（`cp7`、`v1.0`）隨時可查。
> 完整功能說明見 `docs/RELEASE-NOTES-v1.0.md`，使用指南見 `docs/guide/`，維運見 `docs/ADMIN.md`。

## 1. 最終驗收（2026-10-19，master 上一次跑完）

| 項目 | 結果 | 內容 |
|---|---|---|
| `make check` | ✅ | 排版、vet、staticcheck、全部 Go 測試、前端型別／lint／單元測試 |
| `make test-race` | ✅ | race detector × 3 次 |
| `make build` | ✅ | 單一執行檔（amd64、arm64，前端內嵌） |
| `make e2e` | ✅ | Playwright 桌面＋手機，每個畫面同時跑 axe 無障礙檢查 |
| `make box`、`make box-test` | ✅ | Bot 電腦映像：唯讀根目錄、零 capability、無 sudo、5 種引擎都回應 |
| `make test-docker` | ✅ | 真 Bot 電腦：生命週期、網路隔離、快照、接手、教學、檔案、搬家、備份 |
| `make e2e-docker` | ✅ | 網頁建 Bot → 真 Bot 電腦＋Hermes 回覆（修正一個 W32 之後過時的測試） |
| `scripts/test-service.sh` | ✅ | systemd：安裝、聽 0.0.0.0、更新保留設定、壞版本連資料庫回滾、重開機自動啟動、移除 |
| `scripts/test-host.sh` | ✅ | 部署到另一台（含 Bot 電腦映像）、加入遠端主機 |
| `scripts/test-compose.sh` | ✅ | docker compose 版也能跑 Bot 電腦 |
| `scripts/test-cp1.sh` | ✅ | 真的 Claude Code 透過 MCP 建 Bot、對話，網頁同步看到 |
| `scripts/test-clean-install.sh` | ✅ | 乾淨 Debian 13 只裝 make → `make setup YES=1 && make check` |

真模型：CP1 用 OpenCode Go `deepseek-v4-flash` 跑過完整流程；快判模型 `deepseek-v4.1-flash` 實測過分類。

## 2. G3 驗收對照

| G3 項目 | 狀態 |
|---|---|
| PRD 所有 P1 需求都有對應實作和測試 | ✅ 大致完成；沒做的列在 Release Notes「已知限制」（例：群組語音通話、全雙工語音、多使用者邀請、分享連結、密碼庫） |
| 手機 PWA 走完 L01～L21 | ✅ 21 個畫面（L18 通話已接上語音）；PWA 安裝與推播需要 https（Tailscale） |

## 3. M3 與 v1.0 完成的卡

C31 技能記憶、C32 定時任務、C33 檔案、C34 連接應用程式、C35 內建 Tailscale、X05 快判、W31 手機 PWA、W32 入門與 Bot 設定、B31 搬家與 exit node、B32 教 Bot、E31 更多引擎、E32 語音；v1.0：從 OMB／LB 匯入、效能與無障礙、文件、營運修正、介面缺口、兩輪偶發測試修正。共 21 個 migration。

## 4. 使用者做過的決定（都已套用）

- 檢查點不停下、放手做；真模型可以跑但要省額度。
- 部署預設聽 0.0.0.0（非本機要登入碼）。
- 快判用 OpenCode Go 的 `deepseek-v4.1-flash`（Go 沒有 Jev 類模型；Zen 沒有餘額）。
- 訂閱憑證先不放進 Bot 電腦（入門流程改成明確勾選、預設不勾）。
- ChatGPT 方案經 Hermes 正式使用（保留條款說明）。

## 5. 我替你做的安全決定（可以改）

- 「完全放手」也不跳過危險動作審查（Claude Code 不用 bypassPermissions、Codex 不用 full-access）。
- 連接的應用程式一律 untrusted：會寫入的工具（發信、發文、改資料）一定先問你。
- 通話中用說的只能核准低風險項目；花錢、刪除、對外發送一定要在畫面上按。
- 預設每月花費上限 US$20。
- 設了反向代理就關閉「本機免登入」。

## 6. 等你決定的事（不急）

1. 代為核准目前沒有真正可代的操作；要不要讓 Bot 的核准請求帶「類別＋金額」，讓代理能代你核准小額花費？
2. 手機在慢速網路下首屏 3.6～6 秒，主因是粉圓字型；要不要改成只載常用字或系統字型備援？
3. Jev 快判要不要儲值 OpenCode Zen？目前用 Go 的 flash 模型，即時用途多半退回規則。
4. 要不要用你的 Claude／ChatGPT 訂閱真跑一次 Bot 任務？

## 7. 接下來

v1.0 已完成並打 tag。若要上線使用：`make install`（這台）或 `make deploy HOST=…`（另一台），手機走 Tailscale https；從 LazyBoy 搬過來可用「設定 → 一般 → 帶入舊設定」。
