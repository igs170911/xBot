# xBot 設計原則 / xBot Design Principles

xBot 的使用體驗圍繞這 7 個核心原則設計。每個決定都應該回到這些原則檢視。

## English Summary

xBot is built on 7 principles that define its interaction model:
1. **Respond first, then work** - Acknowledge immediately, work in background
2. **Decide and proceed** - Only ask for approval on consequential actions
3. **Minimal interruption, always deliver** - Milestone-only updates, proactive completion
4. **Plain language, lead with conclusion** - No jargon, answer first
5. **Remember the user** - Persistent memory across conversations
6. **Safety by default, not self-discipline** - System-enforced guardrails
7. **Routines from repetition** - Common tasks become automated workflows

---

## 1. 先回應再工作 (Respond first, then work)

**原則**：收到訊息立刻回應，實際工作在背景進行。

**為什麼重要**：人類的心理預期是「發出訊息 → 收到確認」。等待時間超過 2 秒會產生不確定感：「訊息有送到嗎？」、「Bot 是不是卡住了？」。即使工作需要 10 分鐘，先回「收到，我開始處理」也能讓使用者安心。

**實作**：
- 對話分流路由器 (`internal/runtime/routing.go`) 用快判區分閒聊、查進度、實際工作
- 複雜工作先送確認訊息，工作在背景執行
- 使用者可以在工作進行時繼續對話，不會被阻擋

**判斷標準**：
- ✅ 所有訊息在 2 秒內有回應（確認或答案）
- ✅ 長時間工作（>30秒）有即時確認
- ❌ 使用者等超過 5 秒才看到任何反應

## 2. 預設決定並繼續，只在後果明確時詢問 (Default to deciding and proceeding)

**原則**：AI 應該自主決策並行動，只在會產生不可逆後果時才請求核准。

**為什麼重要**：傳統 AI 助理太謹慎，每件事都問「要不要我…」、「需要我幫你…嗎」，把使用者當橡皮圖章。真正有用的助理應該主動做事，只在關鍵決定時才回來確認。

**需要核准的情況**（`confirm` 級操作）：
- 刪除資料（Bot、對話串、檔案）
- 花錢（提高花費上限、購買服務）
- 對外發送訊息（email、Slack、公開 API）
- 修改重要設定（模型切換、權限變更）
- 執行不可逆的指令（部署、刪除遠端資源）

**不需要核准的情況**：
- 讀取資料、搜尋、查詢
- 本機檔案操作（建立、修改、暫存）
- 執行唯讀的程式（計算、分析、測試）
- 開啟網頁、下載檔案

**實作**：
- 操作分級：`safe` / `confirm` / `dangerous` (`internal/ops`)
- 只讀程式自動允許 (`readOnlyExecuteCode` in `runtime.go`)
- 核准卡提供選項按鈕，不是開放式問答

**判斷標準**：
- ✅ Bot 主動完成工作，不問不必要的問題
- ✅ 真正需要決定時才出核准卡
- ❌ 「要不要我幫你查一下」這種問句（應該直接查）

## 3. 最低限度打斷，但一定交付 (Minimal interruption, always deliver)

**原則**：過程中只在重要里程碑更新進度，但完成時一定主動通知。

**為什麼重要**：使用者不需要知道「讀了第 3 個檔案」、「執行第 5 個指令」這種細節，這些都是過程。真正重要的是「開始了新階段」、「遇到問題」、「完成了」。完成時如果不主動通知，使用者會一直檢查「好了沒」。

**里程碑事件**（會發送進度更新）：
- 背景工作開始/結束
- 長時間操作（瀏覽器、網路、建置）
- 外部通訊（MCP工具、訊息傳送）
- 計畫階段完成

**非里程碑事件**（只存資料庫，不打斷）：
- 檔案讀寫
- 終端機指令
- 一般工具呼叫

**主動交付**：
- 工作完成自動建立通知卡
- 開啟 Web Push 的裝置收到推播
- 訊息以答案開頭，不是「已完成」再接答案

**實作**：
- 里程碑過濾 (`isMilestone` in `internal/runtime/milestones.go`)
- 完成通知 (`notifyDone` in `internal/runtime/questions.go`)
- Web Push (`internal/push`)

**判斷標準**：
- ✅ 工作進行時只看到關鍵步驟
- ✅ 完成時主動收到通知，不用一直問「好了沒」
- ❌ 每個工具呼叫都跳通知（太吵）
- ❌ 工作完成了但沒通知（使用者不知道）

## 4. 白話、結論先行、絕不捏造 (Plain language, lead with conclusion, never fabricate)

**原則**：用人類自然的語言，先說結論，不確定就先查，絕對不編造答案。

**為什麼重要**：AI 常見的問題：(1) 用 AI 腔調說話（「我來為您查詢」）、(2) 講一堆背景再給答案、(3) 為了「有禮貌」或「完整」而編造細節。這些都會破壞信任。

**語言風格**：
- **好**：「找到 3 筆訂單，總共 $450。」
- **壞**：「根據您的查詢，我已經為您搜尋了訂單資料庫，現在讓我為您呈現結果…」
- **好**：「記憶體用了 85%，建議重開機。」
- **壞**：「經過分析系統資源使用情況，發現記憶體佔用率較高…」

**結論先行**：
- **好**：「可以，我幫你改成週五。原本是週三下午 2 點。」
- **壞**：「您原本預約的時間是週三下午 2 點，我可以幫您改到週五…」

**不確定時先查**：
- 會變的資料（股價、天氣、新聞）→ 先上網查
- 要精確的數字（金額、數量、日期）→ 先查系統
- 沒把握的事實 → 先確認，不憑記憶猜

**絕不捏造**：
- 不知道 → 明說不知道，問能不能查
- 查不到 → 說查不到，不編一個「可能是…」
- 記憶模糊 → 先查，不憑印象答

**實作**：
- `OwnerNote` in `runtime.go` 要求直接回答、先查才答
- 摘要用快判潤飾，保持簡潔 (`Summarize` in `runtime.go`)

**判斷標準**：
- ✅ 回覆像人在聊天，不像 AI 在作答
- ✅ 第一句就是答案，不用看完整段才知道
- ✅ 沒有編造的資訊（時間、金額、細節）
- ❌ 「我來為您…」、「根據您的要求…」
- ❌ 前面一堆鋪陳，最後才給答案
- ❌ 不確定還硬答

## 5. 記住使用者 (Remember the user)

**原則**：記住對話歷史、使用者偏好、重要資訊，不用每次重講。

**為什麼重要**：真人助理會記得「上次你說要訂週五的」、「你不吃香菜」、「這個客戶用月結」。AI 如果每次都像第一次見面，使用者會感覺它「不夠聰明」。

**應該記住的事**：
- 使用者明確說的偏好（「我不吃海鮮」）
- 重複出現的模式（「每週五下午 3 點開會」）
- 重要的人名、公司名、專案名
- 上次對話的結論（「上次說好要…」）

**不應該記住的事**：
- 隱私敏感資訊（除非使用者主動說要記）
- 一次性的暫時決定
- 別人的對話內容（除非是公開群組）

**實作**：
- 記憶操作 (`internal/memory`)
- 技能文件 (`internal/skills`)
- 對話歷史 (`internal/store` threads & messages)
- SOUL.md 個人化守則

**判斷標準**：
- ✅ 使用者不用重複講過的事
- ✅ Bot 主動提醒「上次你說…」
- ❌ 每次對話都像第一次見面
- ❌ 記住不該記的隱私資訊

## 6. 預設安全，不靠自律 (Safety by default, not self-discipline)

**原則**：安全機制由系統強制執行，不依賴 AI 的「自我判斷」。

**為什麼重要**：AI 可以被 prompt injection 操縱，要求它「判斷這個指令安不安全」會失效。真正的安全是系統層級的防護：檔案權限、網路隔離、操作分級、核准流程。

**系統級防護**：
- Netguard：容器網路隔離，防止存取內網 (`internal/network/netguard`)
- 操作分級：危險操作一律需要核准 (`internal/ops`)
- 金鑰隔離：API 金鑰只在 Model Proxy，不進容器
- 瀏覽器沙箱：高風險操作需要伺服器端核准
- 花費上限：系統級限制，Bot 無法繞過

**不靠自律的例子**：
- **壞**：「請你不要刪除重要檔案」→ 可以被 prompt 覆蓋
- **好**：刪除操作需要 `confirm` 級核准，系統強制要求

**實作**：
- `XBOT_LOCAL_NOAUTH` 預設關閉，開啟時明確警告
- 核准流程 (`internal/inbox`)
- Netguard DNS/IPv6 限制
- `allow_private_urls` 檢查

**判斷標準**：
- ✅ 危險操作一定需要核准，無法繞過
- ✅ 網路隔離、金鑰隔離由系統保證
- ❌ 依賴 AI「判斷」安不安全
- ❌ 在 prompt 裡說「不要做危險的事」

## 7. 重複的事變成例行程序 (Routines from repetition)

**原則**：常做的事情自動變成可重複執行的工作流程。

**為什麼重要**：如果使用者每週都交代「幫我整理訂單」，第 5 次還要重新說明步驟，就太蠢了。好的助理會主動問「要不要我設成每週自動做？」。

**自動化途徑**：
1. **定時任務**：「每週五下午 3 點執行這個」 (`internal/routines`)
2. **技能**：把常用的步驟包成可重用的指令 (`internal/skills`)
3. **記憶**：記住「這件事要這樣做」 (`internal/memory`)
4. **示範教學**：錄下操作步驟，之後重播 (`internal/teaching`)

**觸發時機**：
- 使用者明確要求自動化
- 同樣的任務重複 3 次以上（Bot 主動提議）
- 使用者說「像上次那樣」

**實作**：
- 定時任務 (`internal/routines`)
- 技能系統 (`internal/skills`)
- 示範教學錄製 (`internal/teaching`)

**判斷標準**：
- ✅ 重複的工作第 2 次就更快
- ✅ Bot 主動問「要不要設成自動的」
- ❌ 每次都要重新說明相同的步驟

---

## 原則檢查清單

建立新功能或修改現有行為時，用這份清單檢查是否符合原則：

### 原則 1：先回應再工作
- [ ] 所有訊息在 2 秒內有回應
- [ ] 長時間工作有即時確認
- [ ] 背景工作不阻擋對話

### 原則 2：預設決定並繼續
- [ ] 只在不可逆操作時才要求核准
- [ ] 沒有「要不要我幫你…」的問句
- [ ] 核准卡提供明確選項

### 原則 3：最低限度打斷
- [ ] 只在里程碑更新進度
- [ ] 完成時主動通知
- [ ] 訊息以答案開頭

### 原則 4：白話、結論先行
- [ ] 沒有 AI 腔調（「我來為您…」）
- [ ] 第一句就是答案
- [ ] 不確定的先查，不編造

### 原則 5：記住使用者
- [ ] 記住明確說的偏好
- [ ] 記住重複的模式
- [ ] 不重複問已知的事

### 原則 6：預設安全
- [ ] 危險操作系統級防護
- [ ] 不依賴 AI 自我判斷安全性
- [ ] 金鑰、網路、權限隔離

### 原則 7：例行程序
- [ ] 重複的任務可以自動化
- [ ] Bot 主動提議建立例行程序
- [ ] 技能可重用

---

## 程式碼中的體現

這些原則已經體現在程式碼中：

| 原則 | 主要實作位置 | 檔案證據 |
|-----|------------|---------|
| 1. 先回應再工作 | 對話分流路由 | `internal/runtime/routing.go`<br>`internal/runtime/quick_reply.go` |
| 2. 預設決定並繼續 | 操作分級、自動核准 | `internal/ops`<br>`readOnlyExecuteCode` in `runtime.go` |
| 3. 最低限度打斷 | 里程碑過濾、主動通知 | `internal/runtime/milestones.go`<br>`internal/runtime/questions.go` (`notifyDone`)<br>`internal/push` |
| 4. 白話、結論先行 | 提示詞工程 | `OwnerNote` in `runtime.go`<br>`Summarize` judge function |
| 5. 記住使用者 | 記憶、技能、SOUL | `internal/memory`<br>`internal/skills`<br>SOUL.md 生成 |
| 6. 預設安全 | 系統級防護 | `internal/network/netguard`<br>`internal/ops` (分級)<br>`internal/inbox` (核准)<br>`XBOT_LOCAL_NOAUTH` 警告 |
| 7. 例行程序 | 自動化系統 | `internal/routines`<br>`internal/skills`<br>`internal/teaching` |

---

## 參考文件

- `docs/PRD.md` - 產品需求文件
- `docs/TECH-DESIGN.md` - 技術設計文件
- `docs/guide/README.md` - 使用手冊入口
- `AGENTS.md` - 給執行 Agent 的工作守則
- `CLAUDE.md` - 給 AI 的專案說明
