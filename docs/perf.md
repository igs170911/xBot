# 效能與無障礙（v1.0）

這份記錄 v1.0「效能、無障礙」的量測方法、改了什麼、前後數字，以及 PRD §8 的哪些目標達到了。
重量測：`make bench`（後端）、`make bundle`（前端大小）、`make e2e`（含無障礙與記憶體檢查）。

> 量測環境：AMD Ryzen 5 3600（6 核）、Linux。**量的時候同一台機器上還有其他工作在跑（load average 15～36）**，
> 所以絕對數字偏慢、變動大；前後比較是同一時間交錯跑三輪（`internal/*/bench_test.go` 同一份程式碼放在改之前與改之後的樹上），取中位數。

## 1. 後端

### 基準（`make bench`）

| 熱門路徑 | 情境 | 改之前 | 改之後 | 改了什麼 |
|---|---|---|---|---|
| 操作中介層（讀） | `ops.Invoke`，你本人讀東西 | 108 µs | 35 µs | 讀的操作不再開寫入交易（見下） |
| 操作中介層（讀，6 個同時） | `RunParallel` | 68 µs | 31 µs | 同上：讀不再排在單一寫入連線後面 |
| 操作中介層（寫） | 寫稽核＋事件 | 419 µs | 426 µs | 不變（本來就要寫） |
| SSE 扇出 | 50 個訂閱者，每事件寫表再發布 | 672 µs／事件 | 654 µs／事件 | 不變（已經夠快） |
| SSE 扇出（4 個同時發布，會跳號補讀） | 50 個訂閱者 | 3.0 ms／4 事件 | 3.1 ms／4 事件 | 不變 |
| SSE 延遲 | 50 個訂閱者、每秒 100 事件，發布→收到 | p50 0.10 ms、p99 0.26 ms | p50 0.15 ms、p99 0.29 ms | 不變（雜訊範圍內）；遠低於 200 ms |
| 斷線續傳 | 從 event 表補送 1 萬筆 | 131 ms | 91 ms | 不變（差異是雜訊） |
| `chat.read` 最新一頁 | 1 萬則訊息的對話串（另一條也 1 萬則） | 3.7 ms | 3.1 ms | — |
| `chat.read` 往前翻到最舊 | 同上，`before` 很小 | 14.3 ms | 3.1 ms | `(? = 0 OR seq < ?)` 改成一般範圍條件，用得到索引 |
| `inbox.list`（還沒處理） | 5000 張卡 | 53 ms | 2.5 ms | 一頁滿就停，不整批讀出 |
| `inbox.list`（通知中心，status=all＋分類） | 5000 張卡 | 53 ms | 2.6 ms | 同上＋`inbox_created` 索引 |
| `inbox.counts`（徽章） | 5000 張卡 | 113 ms | 23 ms | 在資料庫裡依種類、Bot 統計 |
| `usage.summary` 30 天 | 10 萬筆（90 天、20 個 Bot），範圍內約 3.3 萬筆 | 1065 ms | 346 ms | 條件用得到索引、只讀要合計的欄位、不排序、沒價格的模型用部分索引 |
| `usage.summary` 7 天、一個 Bot | 同上 | 212 ms | 10.5 ms | `usage_bot` 索引終於用得到 |

### 找到並修掉的問題

1. **每個讀的操作都開一個寫入交易**（`internal/ops/invoke.go` 的 `finish`）：你本人讀東西時不寫稽核、不發事件，
   卻還是 `BEGIN IMMEDIATE` 再 `COMMIT`。寫入只有一條連線，所以所有讀都排在寫的後面、和真正的寫互搶。現在沒東西寫就直接回。
2. **`(? = '' OR col = ?)` 這種「可選條件」讓 SQLite 用不到索引**：用量統計（含花費上限檢查每次模型呼叫都會跑的 `SumUsage`）、
   對話往前翻頁都是整段掃。改成依條件組 SQL。
3. **待我處理整批讀出再在 Go 裡篩**：`inbox.list` 讀全部卡片（5000 張要 50 ms 以上）再取前 50；`inbox.counts` 讀全部卡片來數。
   改成邊讀邊停（`EachInbox`，對話串篩選也放進 SQL）與 `GROUP BY` 統計（`TallyInbox`）。
4. **用量統計逐筆建 `UsageEntry`、逐筆格式化日期**：改成只讀 11 個欄位、用二分搜尋找是哪一天、依（日、Bot、供應商、模型）合計。
   也試過在 SQLite 裡 `GROUP BY`（需要排序，純 Go 的 driver 上反而比較慢），沒有採用。
5. **新增 migration `0026_perf_indexes.sql`**：`usage_task`（工作／工作鏈用了多少 token，協作每回合都查）、`usage_engine`（快判每日用量）、
   `usage_unpriced`（部分索引）、`event_task`（某件工作最新的某種事件）、`inbox_created`（通知中心依時間排）。
6. **前端靜態檔沒有壓縮**（`internal/api/gzip.go`）：JS／CSS 原樣送，手機首頁要下載約 880 KB。現在 `assets/` 底下可壓縮的檔第一次被要時 gzip 一次、
   留在記憶體（全部約 1 MB），之後直接送；首頁實際下載降到約 300 KB。
7. N+1：`chat.read` 的判定改正、`inbox.list` 的復原判斷本來就只對一頁做，沒有再發現 N+1。`bots.list`、`rooms.list` 都是單一查詢。

還可以再快的（沒做，列為後續）：用量 30 天統計在 3 萬筆以上還要數百 ms（瓶頸是純 Go SQLite driver 每列的成本），
真的需要時可以加「每小時彙總表」在寫入時累加。

## 2. 前端

### 建置大小（`make bundle`，首頁一打開要下載的檔：index.html 的入口與 modulepreload）

| | master（合併前） | v1.0 |
|---|---|---|
| 首頁 JS | 1111 KiB（gzip 320 KiB），1 個檔 | 618 KiB（gzip 192 KiB），10 個檔 |
| 首頁 CSS | 247 KiB（gzip 100 KiB） | 250 KiB（gzip 100 KiB） |
| 實際傳輸（有 gzip 之後，Lighthouse 量） | 約 1.36 MB | 約 300 KB（不含字型） |
| 按需載入 | 2 個（noVNC、程式碼高亮） | 42 個 |

- **待我處理與對話**（最常開的兩頁）跟著主程式載入；設定（179 KiB）、noVNC（183 KiB）、程式碼高亮語言（82 KiB）、行事曆／定時任務、
  檔案、辦公室與電腦、團隊、應用程式、語音、匯入、首次設定、手機專用頁、所有對話框都改成 `React.lazy` 按需載入（`web/src/App.tsx`）。
  對話框第一次打開時才下載，之後一直掛著（焦點歸還照舊）。
- Rolldown 預設把主程式和各頁共用的小模組切成上百個小檔（每個圖示一個檔），首頁要發上百個請求；`vite.config.ts` 用
  `codeSplitting.groups` 併成 `vendor`（React、lucide、marked）和 `core`（元件、資料層、對話）兩個共用 chunk。
- PWA：service worker 先存入口檔，按需載入的 chunk 第一次用到時存（原本就這樣處理 `/assets/`）；沒開過的頁面離線時打不開（列在後續）。
- 剩下最大的是**粉圓字型**：fontsource 的 CSS（約 100 KiB gzip，120 多個 `@font-face` 子集）會擋住第一次繪製，
  中文子集字型檔一頁約 475 KB。之後可以只留 woff2、或預先產生常用字子集。

### 長列表

對話（每頁 50 則，往前翻才多載）、待我處理（一次最多 50～200 張）、通知中心、檔案（分組、預設只顯示前幾個）、稽核紀錄都由服務端分頁，
畫面上的節點數有上限，所以沒有加虛擬捲動（加了會讓串流中的訊息高度、捲到底、截圖測試都變複雜）。
用 50 則訊息＋待我處理來回切換 30 次量過，沒有累積（見下）。

### SSE 重連與記憶體（`web/e2e/memory.spec.ts`）

假服務模擬斷線 15 次（網頁自己重連）＋對話與待我處理來回換頁 30 次，強制 GC 後量 JS heap：
**5.07 MB → 5.71 MB（+0.6 MB，主要是對話快取）**，開著的 EventSource 永遠只有 1 條。測試門檻：增加 < 2 MB。

### Lighthouse（`lighthouse@12.8.2`，Playwright 的 Chromium，對著 `make build` 出來的執行檔、一個 Bot、本機連線）

手機是 Lighthouse 預設的模擬（慢速 4G、CPU 降速 4 倍）；桌面用 `--preset=desktop`。

| 頁面 | master 效能 | v1.0 效能 | v1.0 無障礙 | v1.0 最佳做法 | v1.0 FCP／LCP／TBT |
|---|---|---|---|---|---|
| 待我處理・桌面 | 87 | **99** | 100（master 95） | 100 | 0.8 s／0.9 s／10 ms |
| 待我處理・手機 | 56 | **75** | 100 | 100 | 2.9 s／3.3 s／470 ms |
| 對話・桌面 | 79 | **99** | 100 | 100 | 0.7 s／0.8 s／20 ms |
| 對話・手機 | 39 | **64** | 100 | 100 | 5.7 s／6.0 s／0 ms |

量的時候機器負載很高，手機分數上下會差 5～10 分。

## 3. PRD §8 目標

| 目標 | 結果 |
|---|---|
| 首次可互動 ≤ 1.5 s | **桌面達到**（Lighthouse TTI 0.8～0.9 s）。手機在 Lighthouse 的慢速 4G 模擬下 3.6～6 s 沒達到，主要卡在粉圓字型；區網／Wi-Fi 下的手機實測沒量。 |
| SSE 事件延遲 ≤ 200 ms | **達到**：50 個訂閱者、每秒 100 事件，服務內 p99 約 0.3 ms（最差一次 5.7 ms，機器滿載時）。 |
| Box 喚醒 ≤ 20 s | 這張卡沒量（要 Docker 與真的 Bot 電腦，見 `make test-docker`）。 |
| 無障礙：WCAG AA 對比（CI 檢查） | **達到**：axe-core 在 CI（`make e2e`）檢查，深色與淺色主題都掃，有 serious／critical 就失敗。 |
| 無障礙：鍵盤全操作 | **達到**（核心流程）：`web/e2e/keyboard.spec.ts` 只用鍵盤走過跳過側欄、建 Bot、對話、核准、接手、設定。 |
| 無障礙：減少動態效果 | **達到**：系統設定「減少動態效果」時關掉 CSS 動畫與轉場，頭像眨眼（SVG 動畫）也停。 |

## 4. 無障礙（WCAG 2.2 AA）

### 怎麼檢查

- `web/e2e/axe.ts`：axe-core（`wcag2a`～`wcag22aa`＋`best-practice`）＋自己的手機 44×44 可點範圍檢查（PRD §11；比 WCAG 2.2 AA 的 24 px 嚴）。
  serious／critical 算失敗；每次掃描的完整結果（含 moderate／minor）寫在 `web/e2e-results/a11y/`。
- `web/e2e/a11y.spec.ts`：主要頁面（待我處理、辦公室、等你的事、團隊、組織、應用程式、檔案、行事曆、定時任務、設定各區、Bot 設定各分頁、
  對話、電腦、Bot 檔案；手機另加對話清單、通知、新增 Bot／群組、Bot 資料、分享、第一次設定）＋對話框，桌面與手機、深色與淺色各掃一次。
- `web/e2e/fixtures.ts`：其他所有 e2e 規格都改用這個 `test`，**每次截圖（和畫板比對的那些畫面，約 400 個狀態）也順便掃一次**，
  包含首次設定、L01～L21 手機畫面、各種卡片與對話框的中間狀態。`A11Y_SKIP=1` 可暫時關掉。
- `web/e2e/keyboard.spec.ts`：只用鍵盤的核心流程，另外檢查焦點框看得到、對話框裡 Tab 不會跑出去、Esc 關掉後焦點回到原本的按鈕。

### 修掉的違規（第一次全掃時的數量，依規則）

| 規則 | 處數 | 怎麼修 |
|---|---|---|
| touch-target-44（手機可點範圍） | 208＋145（輸入欄） | `index.css` 手機寬度下所有按鈕、連結、分頁用 `::before` 撐到 44×44（外觀不變）；文字欄位至少 44 高；個別元件（輸入列、金額欄、上限欄）調整 |
| color-contrast | 173 | 淺色主題：`accent-ink`、`success`、`danger`、`warning-ink` 調深；程式碼高亮改用變數、淺色另一組；使用者泡泡淺色時用深字；深色主題：文字用的紅由 #ff5667 調成 #ff7a87，實心紅按鈕改用 `danger-solid`、藍底白字改用 `accent-strong`；一直是深底的電腦畫面加 `surface-dark`；拿掉讓文字對比不足的整塊 `opacity` |
| link-in-text-block | 8 | 句子裡的連結加底線 |
| aria-progressbar-name | 7 | 進度條加名稱 |
| aria-toggle-field-name | 6 | Markdown 待辦清單的勾選框加名稱 |
| scrollable-region-focusable | 6 | 會捲動的程式碼、表格可以用鍵盤聚焦 |
| aria-prohibited-attr | 4 | 沒有角色的 `span` 不能用 `aria-label`，改成 `role="img"` |
| page-has-heading-one | 14 | 對話、群組、通話頁加頁面標題（對話與群組是給螢幕閱讀器的隱藏標題） |
| landmark（one-main／no-duplicate-main／main-is-top-level／unique）、region | 19（region 1 處還在） | 外部代理頁不再自己包一個 `main`；Bot 檔案頁加 `main` |
| heading-order | 8（Markdown 的 2 處還在） | 定時任務卡片的標題由 h3 改 h2 |
| empty-table-header | 2 | 補上隱藏文字 |

### 其他改進（axe 抓不到的）

- **螢幕閱讀器播報串流回覆**：原本整個訊息列表是 `aria-live`，串流時會一直念到一半的句子。改成列表不播報、回覆講完時在隱藏的 `role="status"`
  念一次「小秘回覆：…」（最多 300 字，`web/src/chat/Announcer.tsx`）；串流中的訊息標 `aria-busy`。
- **跳到主要內容**：每頁第一個 Tab 停在「跳到主要內容」，按下焦點移到這一頁的 `main`。
- **分頁標題**：瀏覽器分頁標題跟著頁面換（「小秘 · XBot」），換頁時螢幕閱讀器會念。
- **方向鍵**：介面裡自己畫的單選組與分頁（主題、形狀、顏色、檔案分組、篩選…約 25 處）統一支援 ←→↑↓、Home、End（`web/src/ui/rovingKeys.ts`）。
- **減少動態效果**：見上。
- 已經有的：`<html lang="zh-Hant-TW">`、對話框焦點陷阱與歸還、`:focus-visible` 外框。

### 還沒處理（moderate／minor，不會讓測試失敗）

- Bot 回覆裡的 Markdown 標題從 h3 起跳（`#` → h3），在對話頁（h1）底下會跳過 h2（heading-order，moderate）。
- 手機示範教學全螢幕時，noVNC 的 canvas 不在任何 landmark 裡（region，moderate）。
- 純裝飾的頭像狀態點（等你／出錯）顏色沿用畫板的 #FF5667／#FF9800，旁邊都有文字狀態，不算資訊只靠顏色。
