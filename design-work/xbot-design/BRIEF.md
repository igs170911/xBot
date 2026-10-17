# XBot 設計稿 — 共用規格（每個畫板都必須遵守）

Canvas: https://claude.ai/artifact/DmfRHh7dM9EsSLsMVkHYS6
Root folder: /tmp/claude-1000/-home-daniel-Projects/119fe167-bf85-4b35-9f2a-d88d25ba20e9/scratchpad/xbot-design
Files go in `<root>/project/<File>.dc.html`. Format rules = the Design type's SKILL + format.md already in context (x-dc, helmet, data-dc-script, sc-for/sc-if, `{{hole}}` dotted lookups only, close every tag, quote every attr, no innerHTML, no emoji as UI glyphs, inline stroke SVG icons, real <button>/<a href>/<input>+<label>).

## 方向
XBot = OpenMausBot 的功能廣度 + LazyBoy 的地端電腦與 Bot 互聊 + **LazyBoy 的視覺語言**：
- 「答案是主角」、「狀態安靜但一眼看得到」、「只在需要你時搶注意力（警示色只給等你的事）」、「電腦和對話並排」、「黑白為底，Bot 是顏色」、「中文優先排版」。
- 用語（LazyBoy 文案規範）：台灣正體；用「你」；Bot（不是 Agent）、電腦（不是容器）、交辦、補充、停止、交還給 Bot、待你核定、等你的事、提案、討論、定時任務、技能、記憶、連接應用程式、對話串、群組對話、分區。全形標點「」，（）；中英之間空一格；進行中用「…」結尾。
- 文案優先抄 inventory 裡 LazyBoy 的 zh-TW 原文；OMB 功能沒有中文的，用同樣語氣翻成台灣用語。

## 字型
- `<helmet>` 第一行一律：
  `<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Huninn&amp;family=JetBrains+Mono:wght@400;500&amp;display=swap">`
- `body{margin:0;font-synthesis:none}`（粉圓只有一種字重，禁止合成粗體）。
- 全站 `font-family: 'Huninn', 'PingFang TC', 'Noto Sans TC', sans-serif`；**所有 font-weight 都是 400**，階層用字級、顏色、留白做，不用粗體。
- 等寬：`'JetBrains Mono', monospace`（路徑、指令、token 數、快捷鍵、棒次）。
- 字級刻度（LazyBoy v2）：11.5 / 12.5 / 13.5 / 14.5 / 15（內文，行高 1.6；對話 Markdown 1.75）/ 18（h2）/ 20（h1）；大標題（頁面主標、空狀態）可用 24–30。

## 色彩 token（直接寫 hex，inline style）
淺色（設計稿預設）：
- bg #ECEEF1（頁面） · surface #E3E5E9（側欄） · panel #F6F7F9（卡片、輸入框、對話框） · elev #DCDFE4（浮起、選中） · hover #D5D8DE
- line #D0D3DA · line-strong #B9BDC6
- text #0F0F13 · muted #3C3D47 · faint #5C5E69
- accent（黑白）#18181B，accent-ink #FFFFFF，accent-soft rgba(0,0,0,.07)
- ok #17A56A（文字用 #0F7A4D）soft rgba(23,165,106,.11)
- warn #B97700（文字用 #7A4F00）soft rgba(185,119,0,.12)  ← 只給「等你」的事
- danger #D43C3C soft rgba(212,60,60,.09)
- link #1A56DB · unread #3B82F6 · star #B97700 · backdrop rgba(30,30,40,.32)
- shadow: 0 18px 50px rgba(20,20,40,.16), 0 2px 8px rgba(20,20,40,.08)；shadow-s: 0 4px 16px rgba(20,20,40,.1)
深色（辦公室、接手、深色版對話、noVNC 舞台）：
- bg #0E0E11 · surface #141418 · panel #1A1A1F · elev #222228 · hover #26262D · line #2A2A31 · line-strong #3A3A44
- text #ECECF1 · muted #A0A0AB · faint #8A8A96 · accent #ECECF1 / ink #111114 · soft rgba(255,255,255,.09)
- ok #3ECF8E · warn #F5B546 soft rgba(245,181,70,.14) · danger #F26D6D · link #8AB4F8 · stage #050507
次要強調色（設定可選「紫色」）：#6B5AE6（淺）/#8B7CF6（深）——設計稿不用，只在外觀設定裡展示。

**Bot 的顏色**（頭像調色盤 11 色）：白 #F1F2F2、棕 #936439、紅 #FF263C、橘 #FF6700、黃 #FF9800、綠 #00C972、青 #00BCA6、藍 #1084FE、紫 #9159FE、洋紅 #FF309B、灰 #777777。
**你對某個 Bot 說話的泡泡用該 Bot 的顏色**（bubbleTone：亮色保留原色配深字 #16161A；其他調暗到白字 4.5:1）。預先算好：藍 #0B6BD0/白字、紫 #7A45E0/白字、洋紅 #D41F7D/白字、紅 #D41F31/白字、灰 #666666/白字、棕 #936439/白字、橘 #FF6700/深字、黃 #FF9800/深字、綠 #00C972/深字、青 #00BCA6/深字、白 #F1F2F2/深字。群組裡你的泡泡用介面強調色（#18181B/白字）。

## 結構
- 圓角：6 / 8（控制項）/ 12（卡片、側欄列）/ 18（大卡、對話框）/ 20（輸入框、泡泡）/ 999（膠囊）。
- 間距 4 的倍數（2,4,6,8,12,16,20,24,32）。
- 版面：側欄 280（Sidebar 元件）、標題列 56、對話內容最寬 780、輸入區最寬 828、右側面板 560（電腦／檔案）。
- 控制項：文字欄位高 40、按鈕高 36（小 30）、icon 按鈕 36×36 圖示 18、送出鈕 34 圓；手機觸控 ≥44。
- 分段控制：外 10px 圓角 + 3px 內距，內 8px 圓角。
- 對話框尺寸：s 480 / m 560 / l 640 / xl 760 / 2xl 980（＋設定類 1100）。標題列＝h2 + 關閉 X。手機變底部 sheet。
- 狀態呈現：側欄是膠囊（busy 用 accent-soft、attention 用 warn-soft、queued 用 elev；閒置顯示時間）；標題列是圓點；**頭像表情就是狀態**（不加狀態圓點）。
  狀態文字：工作中、正在停止、排隊中、等你回覆、等你允許、教學中、背景工作中（N）、待命。

## 共用元件（dc-import，同資料夾）
1. **Avatar**（Blobatar 風格 Bot 頭像）：
   `<dc-import name="Avatar" color="#1084FE" shape="round" mood="working" size="36" hint-size="36px,36px"></dc-import>`
   shape: round organic boxy capsule nub cloud droplet hexagon sun triangle
   mood（狀態→表情）: idle（待命）ack（收到，驚訝）working（工作中，眼睛往上想）waiting（等你，往下看）done（完成，開心）failed（失敗，難過）sleepy（休息，閉眼 Zz）wink
   也可直接給表情：happy sad mad surprised smug unsure scared love shy sick thinking。可加 tilt（-12..12）。
   在 sc-for 裡用 `color="{{b.color}}"` 等傳入。
2. **Mochi**（品牌吉祥物：懶洋洋的奶油色麻糬，半瞇眼、腮紅、小手、紫色 Zz）：
   `<dc-import name="Mochi" size="120" pose="lazy" hint-size="120px,120px"></dc-import>` pose: lazy sleep happy wave；`zz="false"` 關掉 Zz。
   用在：品牌列、登入、首次流程、空狀態、出錯畫面、關於、載入中。Bot 本身永遠用 Avatar，不用 Mochi。
3. **Sidebar**（桌面側欄，280 寬，含品牌列、搜尋、待我處理、辦公室、分區、Bot 列、底部連結）：
   外層包一層 `<div style="flex: 1 1 280px; max-width: 280px; ...">`？不——頁面骨架照這樣寫：
   ```
   <div style="min-height: 960px; display: flex; flex-wrap: wrap; background: #ECEEF1; ...font...">
     <div style="flex: 1 1 280px; border-right: 1px solid #D0D3DA; background: #E3E5E9"><dc-import name="Sidebar" active="home" theme="light" hint-size="100%,960px"></dc-import></div>
     <main style="flex: 999 1 560px; min-width: 0; ...">…</main>
     <aside style="flex: 1 1 360px; …">…（右側電腦／檔案面板，需要時）</aside>
   </div>
   ```
   active: home | office | xiaomi | azhe | room | none；theme: light | dark（深色頁面外層底色改 #141418，border #2A2A31）。

## 示範角色（全站一致，不要另外發明）
你＝擁有者，在組織裡是「你（董事長）」。
| Bot | 職稱 | 顏色 | 造型 | 分區 | 常見狀態 |
|---|---|---|---|---|---|
| 小秘 ★主要 Bot、幕僚長（主管） | 幕僚長 · 每日重點與協調 | #1084FE | round | 已釘選 | 工作中：彙整三家競品定價 |
| 阿哲 | 選品研究員 | #FF6700 | organic | 營運部 | 工作中：在蝦皮比價 |
| 小編 | 社群小編 | #FF309B | cloud | 營運部 | 等你回覆：簡訊驗證碼（需要你接手電腦） |
| 阿凱 | 業務開發 | #00C972 | boxy | 營運部 | 待命：3 封開發信草稿 |
| 帳房 | 費用管理 | #FF9800 | hexagon | 後勤 | 等你允許：付款 |
| 數據喵 | 數據分析師 | #9159FE | triangle | 後勤 | 排隊中：週報資料 |
| 信差 | 收件匣管家 | #777777 | sun | 後勤 | 休息（sleepy） |
| 抓蟲 | Bug 重現 | #FF263C | nub | 工程 | 待命 |
群組對話：「新品上架專案」（小秘、阿哲、小編）、「每週一早會」（6 位）。
示範任務：比較蝦皮三家競品定價 → 小秘交辦阿哲抓價、問數據喵算差距 → 提案調價 → 帳房付款前問你；小編發新品貼文遇到驗證碼要你接手。
金額、商品名等不知道的事實用 [商品 A]、[$] 這種方括號占位，不要捏造精確數據；介面數字（步驟數、時間、token）可以示意。

## XBot 新增（相對兩個舊專案）
- 首頁「待我處理」：核准、提問、需要你的手（接手電腦）、提案、討論、預算暫停，都能就地處理。
- 辦公室：所有 Bot 電腦的即時縮圖牆（深色），可接手、休眠、喚醒；顯示主機容量。
- Bot 互聊可見：事件列「已傳請求給 X / 來自 X 的回覆」、交棒（第 N/6 棒）、群組「第 N/4 輪」、停止連鎖。
- 花費上限：全域／每 Bot／每條工作鏈；80% 警示、100% 暫停並發卡。
- 新 Bot 預設權限「先問我」。電腦閒置 20 分鐘休眠。
- 多引擎（Claude Code、Codex、Hermes、Grok、Gemini、OpenAI 相容 API、本機模型）。

## 畫板規格
- 桌面頁面：`expand: fill`，根元素 fluid（不寫 px 寬度；min-height 用 px），flex-wrap 版面讓手機寬度時能堆疊；寬表格放 overflow-x:auto 容器。$preview 與 canvas.json 的 w×h 一致。
- 對話框／卡片集：固定寬高根元素（就是對話框本身，含 18px 圓角、panel 底、shadow），背景不畫整個工作區。卡片集（gallery）畫板：bg #ECEEF1 底，上面一張張卡片，每張卡片上方用 12.5px faint 小字標註狀態名稱（例如「核准 · 等待中」「核准 · 已允許」）。
- 手機：390×844 固定，沒有假狀態列、沒有假鍵盤；觸控 ≥44；底部 sheet 只有上方圓角。
- `<title>` 用畫面名稱；`lang="zh-Hant-TW"`。
- 互動（Play）：重要按鈕做真實狀態切換（例：核准→已允許、交還→已交還、分頁切換）；頁面間用 `<a href="X.dc.html">` 連結。可互動的畫板在 canvas.json 已標 is_interactive。
- 不要 tweaks 給文案；最多 1–2 個有意義的 tweak（如 theme）。
- 不要驗證、不要截圖、不要讀回檔案。寫一個檔案→立刻 publish 該檔→再寫下一個。

## 發佈
每寫完一個檔案就 publish（只送自己的檔）：
Artifact publish { url: canvas URL, root: <root>, file_path: "<root>/project/<File>.dc.html" }
**不要送 canvas.json、Avatar/Mochi/Sidebar 或別人的檔案。**被拒絕時：照拒絕訊息重讀該檔再送一次；第三次失敗就跳過並在回報中說明。
完成後回報：寫了哪些檔、有沒有失敗、任何設計上的取捨（簡短）。
