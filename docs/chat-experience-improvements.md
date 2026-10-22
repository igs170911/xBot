# 聊天體驗與穩定性改進計畫

> 目標：讓 xBot 單 Bot 聊天體驗像穩定的隨時在線助理，並找出相較 LazyBoy 退步的原因。

## 現況分析

### 已實作但未接上的功能
1. **對話分流（UseRoute）**：快判系統已經有 chat/progress/work 三種分類，但標記為「只留了掛勾」
   - 位置：`internal/judge/uses.go` UseRoute
   - 現在：所有訊息都走工作模型
   - 需要：在 runtime.Start 接上，簡單問候用聊天模型快速回應

### 需要新增的功能
2. **即時確認（Instant Acknowledgement）**：
   - 目標：2 秒內回應，長工作先給一句「我開始做 X」
   - 方案：快判分流 → chat 直接用便宜模型 → work 先回確認再開電腦

3. **里程碑式進度（Milestone Progress）**：
   - 現在：所有工具呼叫都即時顯示
   - 目標：預設摺疊工具噪音，只顯示「結果／卡住／計畫改變」
   - 方案：新增 progress 事件類型，前端預設摺疊非 progress 的工具步驟

4. **主動完成通知（Proactive Completion）**：
   - 現在：背景工作完成後不會主動通知
   - 需要：完成時發 Web Push + 寫一則「完成」訊息到對話

5. **人設與提示調整（Persona Tuning）**：
   - 檢查 OwnerNote 和 SOUL.md 的組合方式
   - 目標：簡潔、溫暖、直接回答、不重述問題

6. **決策按鈕（Decision Buttons）**：
   - 重用現有的 approval 卡機制
   - Bot 提問時自動產生按鈕選項（而非自由輸入）

## 穩定性調查（vs LazyBoy）

### 要檢查的項目
1. **Hermes 設定差異**：
   - provisioning 參數
   - 重連機制
   - timeout 設定
   - 錯誤重試邏輯

2. **任務完成判定（Judge）**：
   - LazyBoy 的 NEEDS_PERSON / HANDS_BACK / PROMISE 規則
   - xBot 的 judge.Rules 實作
   - 比對準確度

3. **自動續做（Auto-continue）**：
   - LazyBoy 的 2 次續做邏輯
   - xBot 的實作差異

4. **看門狗（Watchdog）**：
   - 時限設定
   - 中斷處理
   - 清理邏輯

5. **錯誤處理**：
   - 供應商錯誤重試
   - 網路斷線重連
   - 狀態復原

## 實作順序

### Phase 1: 對話分流與即時確認（P1）
- [ ] 在 runtime.Start 接上 UseRoute 快判
- [ ] chat 類訊息走輕量模型（不開電腦）
- [ ] work 類訊息先回一句確認再開始
- [ ] progress 類訊息快速回報進度

### Phase 2: 進度更新與前端改進（P2）
- [ ] 新增 progress 事件類型
- [ ] 前端預設摺疊工具步驟
- [ ] 里程碑標記：結果、卡住、改變計畫
- [ ] 完成後主動通知（Web Push）

### Phase 3: 人設與決策改進（P3）
- [ ] 調整 OwnerNote 文案
- [ ] 改進 SOUL 生成邏輯
- [ ] 提問時自動產生按鈕選項
- [ ] 減少無謂的確認與重述

### Phase 4: 穩定性修復（P4）
- [ ] 對照 LazyBoy 找出 Hermes 設定差異
- [ ] 修復重連機制
- [ ] 改進錯誤重試邏輯
- [ ] 加強看門狗的清理

### Phase 5: 測試與文件（P5）
- [ ] 新增對話分流測試
- [ ] 新增即時確認測試
- [ ] 更新使用手冊
- [ ] 撰寫穩定性修復報告

## 已知限制與待確認

1. **LazyBoy 原始碼位置**：`/home/daniel/Projects/LazyBoyHermes` 目錄不存在或無法存取
   - 需要使用者提供正確路徑或關鍵檔案
   - 或從 xBot 現有實作反推差異

2. **快判模型設定**：
   - 預設用 OpenCode Go 的 deepseek-v4.1-flash
   - 是否夠快？是否夠準？需實測

3. **Web Push**：
   - 現有實作在 `internal/notify/push.go`
   - 需確認觸發時機

4. **多訊息分段**：
   - LazyBoy 可能支援一輪多則訊息
   - xBot 需要確認是否支援

## 相關檔案

### 核心邏輯
- `internal/runtime/runtime.go` - 對話執行層
- `internal/engine/router.go` - 引擎路由
- `internal/judge/uses.go` - 快判用途定義
- `internal/judge/service.go` - 快判服務

### 前端
- `web/src/components/Chat/` - 聊天介面
- `web/src/components/Message/` - 訊息渲染

### 人設與提示
- `internal/runtime/instructions.go` - 系統提示生成
- `internal/engine/hermes/provision.go` - Hermes 設定

## 成功指標

1. **即時回應**：90% 的簡單問題 < 3 秒回應
2. **工作確認**：複雜任務 < 5 秒給出確認
3. **進度清晰**：工具步驟預設摺疊，只展開有問題的
4. **主動完成**：背景工作 100% 會主動通知
5. **穩定性**：連續 100 輪對話，中斷率 < 5%
6. **錯誤復原**：網路錯誤 100% 自動重試成功

## 下一步

1. 先實作 Phase 1（對話分流與即時確認）
2. 撰寫測試驗證功能
3. 進行穩定性測試對比
4. 根據結果調整後續 Phase
