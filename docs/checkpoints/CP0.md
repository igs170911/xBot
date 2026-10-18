# CP0：環境、建置與安裝（C00＋C06）

> 狀態：等待使用者檢查。回覆「通過」、「通過但要改 X」或「不通過」後才開始 M0 其他卡（C01、B01、E01、W01）。
> Git：tag `cp0`（`master` 上的合併點），分支 `task/C00-skeleton` 已合併。

---

## 1. 示範步驟（約 10 分鐘，不用讀程式）

在 XBot 目錄下：

| # | 做什麼 | 應該看到 |
|---|---|---|
| 1 | `make` | 所有指令和一句說明 |
| 2 | `make doctor` | 每項工具 ✅／⚠️／❌；Tailscale 沒裝會是 ⚠️ 並附安裝指令 |
| 3 | `make setup` | 已經裝好的會跳過，最後再跑一次 doctor，顯示「必要項目都通過了」 |
| 4 | `make check` | 最後一行「✅ make check 全部通過」 |
| 5 | `make dev`，打開 http://localhost:5181 | 黑底畫面「XBot」，下面寫「服務正常 · …」；Ctrl+C 會一起停掉 |
| 6 | `make build` | `bin/xbot`、`bin/xbot-linux-amd64`、`bin/xbot-linux-arm64` |
| 7 | `scripts/test-service.sh` | 在容器裡跑完安裝 → 狀態 → 備份還原 → 壞版本自動回滾 → 重開機自動啟動 → 移除，最後「✅ 服務安裝測試全部通過」（不會動到你的電腦） |
| 8 | `scripts/test-clean-install.sh` | 在只裝了 make 的乾淨 Debian 13 裡跑 `make setup YES=1 && make check`，約 5 分鐘 |
| 9 （選做）| `make install` | 真的裝到你的電腦，開 http://127.0.0.1:8780；試完可以 `make uninstall` |

---

## 2. 證據

| 項目 | 結果 |
|---|---|
| `make check` | ✅ 通過（gofmt、go vet、staticcheck、Go 測試 7 個套件、tsc、eslint、vitest 3 個測試） |
| 乾淨 Debian 13：`make setup YES=1 && make check` | ✅ 通過。curl、git、xz、libatomic1、ca-certificates、mise、Go、Node、pnpm、staticcheck、air、Playwright 都自動裝好 |
| 缺 Go 的環境跑 `make doctor` | ✅ 顯示「❌ Go 沒有安裝 ↳ 修正：make setup」並以非零結束 |
| `make dev` | ✅ 後端與前端都回應；Ctrl+C 後沒有殘留程序 |
| `make e2e` | ✅ 桌面與手機尺寸各 1 個測試通過（對著 `make build` 出來的執行檔） |
| `scripts/test-service.sh`（systemd 容器） | ✅ 安裝、狀態、備份→刪資料→還原（資料完整）、壞版本自動回滾、重開機自動啟動、移除後資料保留 |
| `make up`（docker compose） | ✅ 映像建置成功、`/healthz` 正常、容器健康檢查 healthy |

---

## 3. 和規格不一樣的地方

1. **C00 和 C06 在同一個分支**。兩張卡都改 Makefile 和 `cmd/xbot`，拆開反而容易衝突。
2. **開發埠**：`make dev` 用 8782（API）和 5181（前端）。你電腦上 LazyBoy2 已經占用 5173，8780 留給安裝版服務，兩者可以同時開。
3. **TypeScript 固定 6.x**：typescript-eslint 還不支援 TypeScript 7，等它支援再升級。
4. **`make update` 已經有自動回滾**（原計畫 M3 才補完整）。新版起不來會換回上一版並重啟。
5. **`make install` 先不建 Bot 電腦映像**：`make box` 要到 B01 才有。
6. **`make deploy`、`make host-add`、`make host-check`** 照計畫在 M1 的 B12 才做，這次還沒有。
7. **備份**：預設不含 Bot 瀏覽器的登入資料（`bots/*/home/chrome-profile`），`INCLUDE_LOGINS=1` 才包含。資料庫用 SQLite 的 `VACUUM INTO` 做快照，服務執行中也能備份。

---

## 4. 需要你決定的事

1. **安裝位置**：安裝版的執行檔放 `~/.local/bin/xbot`，資料放 `~/.local/share/xbot`，用 systemd **使用者**服務（不需要 sudo）。可以嗎？
   - A. 可以（目前做法）
   - B. 改成系統服務（`/opt/xbot`、`/var/lib/xbot`，需要 sudo）
2. **開機自動啟動**：需要 `loginctl enable-linger`，`make install` 會先問你。
   - A. 每次都問（目前做法）
   - B. 預設直接開
3. **Node 版本**：目前用 Node 26（今年 10 月才進入 LTS）。
   - A. 維持 26
   - B. 改用 Node 24 LTS（比較保守）

---

## 5. 下一階段（CP0 通過後）

同時開 4 條線，各做一張卡：

| 線 | 卡 | 內容 |
|---|---|---|
| 核心 | C01 | SQLite schema 與 migration |
| 電腦 | B01 | 把 LazyBoy 的 Bot 電腦映像搬進 `box/`，`make box` |
| 引擎 | E01 | Model Proxy 最小版（worker-key、一個 OpenAI 相容供應商） |
| 前端 | W01 | 設計系統：Grok 色票、粉圓字型、平面頭像、基本元件 |

接著是 C02～C05（操作登錄表、HTTP／MCP／CLI）、B02、E02、W02，做完到 **CP1**：Claude Code 透過 MCP 建 Bot 並對話。
