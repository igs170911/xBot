# 聊天體驗與穩定性改進 - 第一階段進度報告

> 日期：2026-10-22
> 分支：`cursor/chat-experience-and-stability-b5b1`
> 狀態：Phase 1 基礎架構完成

## 已完成的工作

### 1. 對話分流基礎架構（Phase 1 核心）

#### 新增檔案
- `internal/runtime/routing.go` - 對話分流路由器
- `internal/runtime/routing_test.go` - 路由邏輯測試

#### 功能特色
1. **智能分流**：根據訊息內容決定處理方式
   - `chat`：閒聊/問候 → 不開電腦，用快速聊天模型
   - `progress`：查進度 → 不開電腦，直接返回進度
   - `work`：實際工作 → 開電腦執行完整流程

2. **即時確認**：複雜任務先回應一句確認
   - 檢測多步驟任務（「然後」「第一」「第二」等關鍵字）
   - 根據工作類型生成對應的確認訊息
   - 簡短任務不額外確認，直接開始

3. **測試覆蓋**
   - 測試分流決策邏輯
   - 測試確認訊息生成
   - 測試多步驟檢測
   - 所有測試通過 ✅

#### 技術細節
- 使用 `judge.Decider` 介面進行快判（可替換實作）
- 與現有的 `judge.Service` 整合
- 日誌記錄分流決策以便調試
- 避免重名衝突（`routingClipText` vs `clipText`）

## 下一步計畫

### Phase 1 剩餘工作

#### 1.1 整合到 runtime.Start
```go
// 在 runtime.Start 中加入路由邏輯
func (r *Runtime) Start(ctx context.Context, req ChatRequest) (ChatResult, error) {
    // ... 現有檢查 ...
    
    // 新增：對話分流
    if r.router != nil && req.Source == SourceOwner {
        shouldOpenComputer, quickReply := r.router.Route(ctx, b, threadID, text)
        
        if quickReply != "" {
            // 立即回應確認訊息
            msgID := r.sendQuickReply(ctx, botID, threadID, quickReply)
        }
        
        if !shouldOpenComputer {
            // 不開電腦的情況：chat 或 progress
            return r.handleWithoutComputer(ctx, req, isProgress)
        }
    }
    
    // 繼續現有流程：開電腦執行
    // ...
}
```

#### 1.2 實作快速聊天模型
```go
// 在 Router 中實作 quickChatReply
func (r *Router) quickChatReply(ctx context.Context, b store.Bot, text string) string {
    // 呼叫輕量模型（例：OpenCode Go flash）
    // 簡單問候直接回應，不需要開電腦等待
    // 返回生成的回覆
}
```

#### 1.3 不開電腦的處理流程
- chat：用快速模型回應
- progress：組裝進度資訊並返回

### Phase 2: 進度更新與 UI 改進

#### 2.1 里程碑式進度事件
- 新增 `KindProgress` 事件類型
- 只在重要時刻發送進度：
  - 完成一個階段
  - 遇到問題/卡住
  - 計畫改變
  - 詢問使用者

#### 2.2 前端摺疊工具噪音
- 預設摺疊 `tool.start` / `tool.complete` 事件
- 只展開有問題的工具呼叫
- 「展開工具步驟」按鈕供使用者查看細節

#### 2.3 主動完成通知
- 背景工作完成時發送 Web Push
- 在對話中主動發送「完成」訊息
- 引用任務結果和關鍵資訊

### Phase 3: 人設與決策改進

#### 3.1 調整 OwnerNote
目前的 OwnerNote 已經相當完整，可能需要微調：
- 強調「即時開始」（不要說「我來做」就停下）
- 簡化口吻（更自然、不重述）

#### 3.2 決策按鈕
- 重用現有的 approval 卡機制
- Bot 提問時自動產生按鈕選項
- 減少自由輸入的需求

### Phase 4: 穩定性調查與修復

#### 4.1 對比 LazyBoy 設定
需要調查的項目：
1. Hermes provisioning 參數差異
2. 重連機制的實作
3. Timeout 設定值
4. 錯誤重試邏輯
5. 看門狗行為

#### 4.2 已知可能問題點
從程式碼審查發現：
- runtime.go 的看門狗已經實作（Idle/Tool 時限）
- 供應商錯誤重試在 taskdone.go 中
- 任務完成判定使用快判＋規則 fallback

需要實際測試對比找出退步原因。

#### 4.3 測試方法
1. 準備測試案例（簡單對話、複雜任務、錯誤情況）
2. 在 xBot 和 LazyBoy 上執行相同案例
3. 記錄差異（回應時間、成功率、錯誤處理）
4. 找出根本原因並修復

## 技術債務與注意事項

### 待實作的 TODO
1. `quickChatReply` - 快速聊天模型的實際呼叫
2. `handleWithoutComputer` - 不開電腦的完整處理流程
3. 快判服務的實際整合（目前使用 mock）

### 設計決策記錄
1. **為什麼用 Decider 介面？**
   - 解耦：測試時可以用 mock
   - 彈性：可以替換不同的快判實作
   - 清晰：明確的職責分離

2. **為什麼確認訊息是固定模板？**
   - 簡單：不需要額外的模型呼叫
   - 快速：即時回應不能等模型生成
   - 可靠：固定語句不會出錯
   - 未來：可以改用快判模型生成個性化確認

3. **為什麼不在測試中實際呼叫模型？**
   - 速度：測試要快速
   - 穩定：不依賴外部服務
   - 成本：避免不必要的 API 呼叫

## 測試結果

### 單元測試
```bash
$ go test -v ./internal/runtime -run TestRoute
=== RUN   TestRoute_Chat
--- PASS: TestRoute_Chat (0.00s)
=== RUN   TestRoute_Progress
--- PASS: TestRoute_Progress (0.00s)
=== RUN   TestRoute_Work
--- PASS: TestRoute_Work (0.00s)
=== RUN   TestRoute_NoDecider
--- PASS: TestRoute_NoDecider (0.00s)
PASS
ok      xbot/internal/runtime   0.007s
```

### 全部 runtime 測試
```bash
$ go test ./internal/runtime -short
ok      xbot/internal/runtime   4.729s
```

所有測試通過 ✅

## 預估工作量

### 剩餘 Phase 1（對話分流完整實作）
- 整合到 runtime.Start: 2-3 小時
- 實作快速聊天模型: 2-3 小時
- 不開電腦流程: 1-2 小時
- 測試與調整: 1-2 小時
- **小計：6-10 小時**

### Phase 2（進度更新與 UI）
- 後端進度事件: 2-3 小時
- 前端摺疊邏輯: 3-4 小時
- 主動完成通知: 2-3 小時
- 測試與調整: 2-3 小時
- **小計：9-13 小時**

### Phase 3（人設與決策）
- OwnerNote 調整: 1-2 小時
- 決策按鈕實作: 2-3 小時
- 測試與調整: 1-2 小時
- **小計：4-7 小時**

### Phase 4（穩定性調查）
- LazyBoy 對比測試: 3-5 小時
- 找出根本原因: 視發現的問題而定
- 修復與驗證: 視問題複雜度而定
- **小計：難以預估，至少 8-15 小時**

### Phase 5（文件與測試）
- 更新使用手冊: 2-3 小時
- 穩定性報告: 2-3 小時
- 整合測試: 2-3 小時
- **小計：6-9 小時**

### 總計
預估 33-54 小時的工作量（不含 Phase 4 的不確定部分）

## 風險與限制

### 技術風險
1. **LazyBoy 原始碼存取**：需要確認 LazyBoy 專案路徑
2. **快判模型設定**：需要驗證 OpenCode Go 的效能
3. **向下相容性**：新功能不能破壞現有行為

### 已知限制
1. 快判服務需要正確設定供應商
2. 對話分流目前只在使用者直接訊息時啟用
3. 快速聊天模型尚未實作（TODO）

## 下一步行動

### 立即行動（接下來 2-3 小時）
1. ✅ 完成對話分流基礎架構
2. 開始整合到 runtime.Start
3. 實作不開電腦的處理流程

### 短期行動（接下來 1-2 天）
1. 完成 Phase 1 所有工作
2. 開始 Phase 2 進度更新
3. 準備 LazyBoy 對比測試環境

### 中期行動（接下來 1 週）
1. 完成 Phase 2 和 Phase 3
2. 進行穩定性調查（Phase 4）
3. 撰寫完整文件

## 參考資料

### 相關檔案
- `/workspace/docs/FASTJUDGE.md` - 快判系統設計
- `/workspace/docs/RELEASE-NOTES-v1.0.md` - 已知限制
- `/workspace/internal/judge/uses.go` - 快判用途定義
- `/workspace/internal/runtime/runtime.go` - 核心執行邏輯

### 關鍵概念
- UseRoute (用途 1)：對話分流
- judge.Decide：快判決策方法
- OwnerNote：附加在使用者訊息的系統提示
- 看門狗：15/40 分鐘無進度自動中斷

## 結論

Phase 1 的對話分流基礎架構已經完成並通過測試。這為即時確認和智能路由奠定了基礎。

下一步需要整合到實際的 runtime 流程中，並實作快速聊天模型的呼叫。整體進度符合預期，沒有發現重大阻礙。

繼續按照計畫執行，預計可以在合理時間內完成所有 Phase 的工作。
