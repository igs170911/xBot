# UI 簡化計畫

> 整合到聊天體驗改進工作中
> 目標：乾淨、簡約、以聊天為中心的個人助理應用

## 設計原則

1. **Chat-First**：聊天是核心，其他都是輔助
2. **Minimal**：只顯示當下需要的，其餘收起
3. **Breathing Room**：大量留白，讓內容呼吸
4. **Progressive Disclosure**：逐步展開細節
5. **Consistent**：統一的視覺語言和交互模式

## 布局改進

### 現狀分析
- 左側邊欄（260px）：Bot 列表、群組、各種導航
- 中央區域：聊天/內容
- 右側面板（可選）：電腦畫面、檔案等
- 多處重複的導航和狀態顯示

### 目標布局

```
┌─────────┬──────────────────────────┬──────────────┐
│ 細側欄   │   大聊天區域               │ 資訊面板     │
│(56-80px)│                           │(可摺疊)      │
│         │                           │              │
│ Bot圖示 │   對話內容                 │ 電腦畫面     │
│ 對話    │   (大量留白)               │ 檔案         │
│         │                           │ 記憶         │
│         │   ─────────────────       │ 設定         │
│         │   輸入框                   │              │
└─────────┴──────────────────────────┴──────────────┘
```

#### 左側欄（簡化版）
- **寬度**: 56-80px（固定，不可調整）
- **內容**: 
  - Bot 頭像列表（當前對話高亮）
  - 最近對話（最多 5 條，只顯示頭像）
  - 底部：新增 Bot、設定
- **移除**: 
  - 冗長的 Bot 名稱
  - 狀態徽章（正在工作/閒置等）
  - 團隊分組標題（移到右側面板）
  - 多餘的按鈕和圖示

#### 中央聊天區
- **最大寬度**: 960px（內容居中，避免文字過寬難讀）
- **留白**: 訊息間距增加到 24-32px
- **內容**: 
  - 頂部：Bot 名稱 + 職稱（輕量級，不是工具列）
  - 對話內容
  - 底部：固定的輸入框

#### 右側資訊面板（可摺疊，預設關閉）
- **寬度**: 320-400px
- **觸發**: 
  - 點擊頂部的標籤/按鈕
  - Bot 需要顯示電腦畫面時自動打開
- **內容分類**:
  - 電腦畫面（Computer）
  - Bot 資訊（檔案、記憶、技能）
  - 團隊結構（Teams）
  - 定時任務（Routines）
  - 設定（Settings）

## 訊息改進

### 工具活動摺疊（重點！）
**現狀**: 每個工具呼叫都顯示完整的 `tool.start` 和 `tool.complete` 事件

**改進**:
```tsx
// 預設顯示（摺疊狀態）
┌─────────────────────────────┐
│ 小研                         │
│ 正在查資料… (3 個步驟)       │  ← 單行，可點擊展開
└─────────────────────────────┘

// 點擊展開後
┌─────────────────────────────┐
│ 小研                         │
│ ▼ 正在查資料… (3 個步驟)     │  ← 展開指示器
│                              │
│   ✓ web_search              │  ← 完成的步驟
│   ⟳ browser                 │  ← 進行中
│   ⋯ web_extract             │  ← 等待中
│                              │
└─────────────────────────────┘

// 完成後自動摺疊回
┌─────────────────────────────┐
│ 小研                         │
│ 工作了 2 分鐘 · 5 個步驟     │  ← 摺疊摘要
│                              │
│ 找到三家的價格如下…          │  ← 實際回覆
└─────────────────────────────┘
```

### 訊息排版
- **留白**: 訊息間距 24-32px
- **泡泡**: 最大寬度 70%（避免過寬）
- **字型**: 16px 正文，行高 1.6
- **Markdown**: 保持清晰的渲染（代碼塊、表格、清單）

### 決策按鈕
重用現有的 approval 卡機制，但更簡潔：

```tsx
┌─────────────────────────────┐
│ 需要你決定：                  │
│                              │
│ 要刪除團隊「研發部」嗎？      │
│                              │
│ [允許] [拒絕] [查看詳情]      │  ← 按鈕並排
└─────────────────────────────┘
```

## 移除/簡化的元素

### 完全移除
- [ ] 側欄的寬度拖動手柄（固定寬度）
- [ ] 重複的狀態指示器
- [ ] 多餘的工具列按鈕
- [ ] 過多的徽章和標籤
- [ ] 複雜的下拉選單（改為簡單按鈕）

### 移到右側面板
- [ ] Bot 詳細設定
- [ ] 檔案瀏覽器
- [ ] 記憶查看
- [ ] 技能列表
- [ ] 團隊結構
- [ ] 定時任務管理

### 保持但簡化
- [ ] Bot 列表（只顯示頭像）
- [ ] 對話串（摺疊，只顯示標題）
- [ ] 輸入框（保持所有功能，但視覺更簡潔）
- [ ] 頂部導航（只保留必要項目）

## 實作優先順序

### Phase A: 工具活動摺疊（與 Phase 2 重疊）
**檔案**: `web/src/chat/WorkSteps.tsx`、`web/src/chat/Message.tsx`

1. ✅ 建立摺疊狀態管理
2. ✅ 實作摺疊/展開 UI
3. ✅ 預設摺疊，可點擊展開
4. ✅ 完成後自動摺疊成單行摘要
5. ✅ 測試各種工具組合

**預估**: 3-4 小時

### Phase B: 側欄簡化
**檔案**: `web/src/shell/Sidebar.tsx`

1. 減小側欄寬度到 80px
2. Bot 列表改為只顯示頭像
3. 移除重複的狀態顯示
4. 簡化底部按鈕
5. 移除寬度拖動功能

**預估**: 2-3 小時

### Phase C: 聊天區留白與排版
**檔案**: `web/src/chat/ChatPage.tsx`、`web/src/chat/Message.tsx`

1. 增加訊息間距
2. 設定最大內容寬度
3. 改進泡泡樣式
4. 優化字型和行高
5. 簡化頂部工具列

**預估**: 2-3 小時

### Phase D: 右側面板重組
**檔案**: `web/src/shell/panes.ts`、新增 `web/src/shell/RightPanel.tsx`

1. 建立統一的右側面板
2. 標籤列切換不同內容
3. 電腦畫面整合
4. 檔案/記憶/設定整合
5. 預設關閉，可快速切換

**預估**: 4-5 小時

### Phase E: 響應式與主題
**檔案**: 各 CSS 檔案、主題設定

1. 手機版適配
2. 平板版布局
3. 深色主題優化
4. 動畫過渡
5. 無障礙改進

**預估**: 3-4 小時

### Phase F: 測試與截圖
1. 更新 e2e 測試
2. 截取改進前截圖
3. 截取改進後截圖
4. 建立對比文件
5. 驗證所有功能可達

**預估**: 2-3 小時

## 技術細節

### 工具活動摺疊實作

```tsx
// web/src/chat/WorkSteps.tsx
import { useState } from 'react'

export function WorkSteps({ a, bot }: { a: Activity; bot?: Bot }) {
  const [expanded, setExpanded] = useState(false)
  const active = isActive(a)
  const steps = a.steps || []
  
  // 進行中：顯示單行狀態
  if (active && !expanded) {
    return (
      <button
        onClick={() => setExpanded(true)}
        className="flex items-center gap-2 px-4 py-2 text-sm text-ink-3 hover:text-ink-2"
      >
        <Loader className="h-4 w-4 animate-spin" />
        <span>正在處理… ({steps.length} 個步驟)</span>
      </button>
    )
  }
  
  // 完成：摺疊摘要
  if (!active && !expanded) {
    return (
      <button
        onClick={() => setExpanded(true)}
        className="flex items-center gap-2 px-4 py-2 text-sm text-ink-3 hover:text-ink-2"
      >
        <span>工作了 {formatDuration(a.duration)} · {steps.length} 個步驟</span>
        <ChevronRight className="h-4 w-4" />
      </button>
    )
  }
  
  // 展開：顯示完整步驟
  return (
    <div className="px-4 py-2 space-y-1">
      <button
        onClick={() => setExpanded(false)}
        className="flex items-center gap-2 text-sm text-ink-3 hover:text-ink-2"
      >
        <ChevronDown className="h-4 w-4" />
        <span>{active ? '正在處理' : '已完成'} ({steps.length} 個步驟)</span>
      </button>
      
      <div className="pl-6 space-y-1">
        {steps.map((step, i) => (
          <StepRow key={i} step={step} />
        ))}
      </div>
    </div>
  )
}
```

### 側欄寬度控制

```tsx
// web/src/shell/panes.ts
export const SIDEBAR = {
  name: 'sidebar',
  min: 80,    // 固定寬度，不可調整
  max: 80,
  default: 80,
} as const

// web/src/shell/Sidebar.tsx
// 移除 ResizeHandle
// 改為固定寬度的圖示列表
```

### 右側面板標籤

```tsx
// 新增: web/src/shell/RightPanel.tsx
type Tab = 'computer' | 'files' | 'memory' | 'team' | 'settings'

export function RightPanel() {
  const [tab, setTab] = useState<Tab>('computer')
  const open = useStore(rightPanelStore)
  
  if (!open) return null
  
  return (
    <div className="flex flex-col h-full border-l border-edge">
      {/* 標籤列 */}
      <div className="flex border-b border-edge">
        <Tab icon={Monitor} active={tab === 'computer'} onClick={() => setTab('computer')} />
        <Tab icon={FolderOpen} active={tab === 'files'} onClick={() => setTab('files')} />
        <Tab icon={Brain} active={tab === 'memory'} onClick={() => setTab('memory')} />
        <Tab icon={Users} active={tab === 'team'} onClick={() => setTab('team')} />
        <Tab icon={Settings} active={tab === 'settings'} onClick={() => setTab('settings')} />
      </div>
      
      {/* 內容區 */}
      <div className="flex-1 overflow-auto">
        {tab === 'computer' && <ComputerPanel />}
        {tab === 'files' && <BotFilesPage />}
        {/* ... 其他標籤內容 */}
      </div>
    </div>
  )
}
```

## 設計 Token 更新

```css
/* 增加間距 */
--chat-message-gap: 24px;      /* 原本可能是 12px */
--chat-bubble-padding: 16px;   /* 原本可能是 12px */
--chat-max-width: 960px;       /* 內容最大寬度 */

/* 字型 */
--chat-font-size: 16px;        /* 原本可能是 14px */
--chat-line-height: 1.6;       /* 原本可能是 1.5 */

/* 側欄 */
--sidebar-width: 80px;         /* 原本是 260px */
--sidebar-icon-size: 40px;
```

## 向下相容與漸進式改進

1. **不破壞現有功能**: 所有功能保持可達，只是重組
2. **漸進式實作**: 每個 Phase 獨立，可以分批合併
3. **功能開關**: 可以加入 feature flag 讓使用者選擇新舊 UI
4. **測試覆蓋**: 每個改動都更新對應的 e2e 測試

## 成功指標

- [ ] 側欄寬度減少到 80px
- [ ] 工具步驟預設摺疊
- [ ] 訊息留白增加 50%
- [ ] 頁面只有一個主要動作按鈕
- [ ] 所有次要功能移到右側面板
- [ ] 手機版無破壞性改動
- [ ] 所有 e2e 測試通過
- [ ] 前後對比截圖完成

## 風險與注意事項

1. **大量前端改動**: 需要謹慎測試，避免破壞現有功能
2. **使用者習慣**: 布局改變可能需要適應期
3. **響應式挑戰**: 確保手機/平板/桌面都正常
4. **無障礙**: 不能在簡化過程中降低無障礙性
5. **效能**: 摺疊/展開不能影響效能

## 下一步

1. ✅ 建立此計畫文件
2. 開始 Phase A: 工具活動摺疊
3. 提交進度並建立 PR
4. 逐步完成其他 Phase
5. 收集反饋並迭代
