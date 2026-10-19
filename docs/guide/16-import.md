# 從 LazyBoy 或 OpenMausBot 匯入

在同一台電腦上用過 LazyBoy（LazyBoyHermes）或 OpenMausBot 的話，可以把 Bot 和設定帶進 XBot。**只讀舊資料，絕不修改它。**

## 從哪裡開始

畫面上已經沒有這個入口（2026-10-19 拿掉）。需要的話用指令列（或外部代理經 MCP）：

```bash
xbot import detect   # 看這台電腦找到哪些舊資料
xbot import plan     # 列出會帶進來的東西
xbot import run      # 開始帶入
```

## XBot 會去哪裡找

- LazyBoy：`LAZYHERMES_DATA_DIR`，以及家目錄下常見的位置（`~/LazyBoyHermes/.data`、`~/Projects/LazyBoyHermes/.data` 等）。
- OpenMausBot：`OMB_DATA_DIR`、`~/.openmausbot`、`~/.openmausbot-*`、更舊的 `~/.opengrokbot`。
- 放在別的地方：啟動服務時設 `XBOT_IMPORT_PATHS=/路徑一:/路徑二`（用 `:` 分隔；`off` 表示不找）。

## 步驟

1. **看計畫**：逐項列出會帶什麼，每項可以勾選。和現有 Bot 同名的會自動加編號，也可以改名。這一步不會建立任何東西。
2. **開始匯入**：在背景進行，畫面有進度。可以離開這頁。
3. **看結果**：每項成功、略過或失敗的原因。

可以重複執行。已經帶過的會標「已帶過」，不會重複建立；上次沒勾的（例如瀏覽器登入）可以之後再補帶。

## 會帶什麼

| 項目 | 預設 | 帶過來後 |
|---|---|---|
| Bot（名字、職稱、人設、外觀、引擎、模型） | 勾 | 權限一律從「先問我」開始 |
| 技能 | 勾 | 先**關閉**，看過內容再打開 |
| 記憶 | 勾 | 照原樣；另外的記憶檔放進工作資料夾 |
| 電腦裡的檔案 | 勾（太大的不勾） | 放進新電腦的家目錄、工作資料夾 |
| 瀏覽器登入 | **不勾**，要你勾 | 放進新電腦的瀏覽器 |
| 定時任務 | 勾 | 先**暫停**，你確認後再啟用 |
| 團隊、群組、「只有你能決定的事」 | 勾 | 照原樣 |
| 模型金鑰 | **不勾**，要你勾 | 加密後只放在服務端；你已經設定過的供應商不覆寫 |

**不帶**：對話紀錄、管理權限（團隊管理權限要你重新打開）。

## 已知限制

- OpenMausBot 的時間沒有時區資訊，一律當成台北時間。
- OpenMausBot 桌面版的金鑰存在系統鑰匙圈，讀不到，要自己重新貼。
- 搬過來的 Bot 第一次對話時才會開機。

指令列：

```bash
xbot import detect
xbot import plan --source <detect 的 id>
xbot import run  --source <id> --items <計畫裡的代號> --items … --wait
```
