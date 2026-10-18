# XBot 實作進度

每張任務卡完成時更新一列。卡片內容見 `docs/PLAN.md`。

| 卡 | 狀態 | 分支／commit | 和規格不一樣的地方 | 後續事項 |
|---|---|---|---|---|
| C00 環境與專案骨架 | ✅ 完成 | `task/C00-skeleton` | 和 C06 放在同一個分支（兩張卡都改 Makefile）；開發埠改 8782／5181（5173 被本機其他專案占用、8780 留給安裝版）；TypeScript 固定 6.x（typescript-eslint 還不支援 7） | — |
| C06 建置、安裝與本機服務 | ✅ 完成 | `task/C00-skeleton` | `make update` 已含自動回滾（原計畫 M3 才補）；`make install` 暫不建 Bot 電腦映像（B01 才有） | B01 完成後 `make install` 要加上 `make box` |
| B01 Bot 電腦映像 | ✅ 完成 | `task/B01-box` | Hermes 在建置時抓固定 commit（不放進 git）；`make install` 會一併 `make box`（首次約 10 分鐘）；未移植 Debian 鏡像測速 | B02 要用新的環境變數名稱 `XBOT_UID`／`XBOT_GID`／`XBOT_SCREEN_*`；容器內接 XBot MCP 的方式待 C11 決定 |
| E01 Model Proxy 最小版 | ✅ 完成 | `task/E01-proxy` | 路徑白名單照 LazyBoy＋EN-3（多了 embeddings、images、audio）；worker key 改 HMAC＋每 Bot nonce；請求上限 64 MB；用量逐筆記錄 | 尚未接進 `xbot serve` 與 SQLite（E02／C01 後接）；`model-proxy.key` 必須和資料庫一起備份（已在資料目錄內）；每 Bot 綁定供應商留給 E11；串流閒置逾時待補 |
| C01 資料庫與 schema | ✅ 完成 | `task/C01-store` | 依 §16 等章節補了 PRD §10 沒列的欄位（host、box.host_id、usage 成本以百萬分之一美元存）；message 用自動遞增 seq 排序 | 待確認：approval／inbox／host 狀態值、memory_enabled 預設；事件清理尚未排程；request_id 去重表留給 C02 |
| W01 前端殼與設計系統 | ✅ 完成 | `task/W01-design-system` | 多了淺色主題 token（N01 有定義，尚無切換）；自建輕量路由；字型自帶後執行檔約 20 MB | 待確認：手機抽屜斷點 768px 與頂列設計；送出鈕空白時是否停用；是否要做字型子集縮小體積；Main 右欄電腦縮圖目前是灰色佔位 |
| B02 本機 Box Manager | ✅ 完成 | `task/B02-boxmgr` | Docker client 改用 `moby/moby/client`（舊路徑已停止維護）；新增 migration 0002（box.hermes_port）；XBOT_UID 固定 1000；SEC-3 唯讀根目錄與 SEC-4 私有網段封鎖延到 B13 | compose 模式（make up）下服務連不到 127.0.0.1 的 Bot 埠，需要網路模式（B12/B13）；pids-limit 4096 待確認；Manager 尚未接進服務（E02／ops） |
| C02 操作登錄表與中介層 | ✅ 完成 | `task/C02-ops` | dry_run 不寫稽核；owner 的讀取不寫稽核（避免輪詢灌爆）；pending_approval 以 MCP-5 錯誤格式回傳；migration 0003 | 待確認：權限範圍是疊加還是巢狀；MCP-8「預設只能碰自己建立的」需要 created_by；刪除 Bot 時核准紀錄會連帶刪除（建議改 SET NULL）；核准逾期、approvals.wait、速率限制留給後續卡 |
| E02 Hermes 驅動最小版 | ✅ 完成 | `task/E02-hermes` | Bot 經 hermes_host.py → product.sock → Model Proxy 取模型（真金鑰不進容器，Docker 測試驗證）；migration 0004；每個 Bot 一次只跑一個工作（第二則回 busy） | 待確認：模型設定格式 `<provider>/<model>`；needs_approval 尚無人回應（C12）；沒有看門狗，回合卡住會一直等（CH-12）；遠端主機的 socket 留給 B11 |
| C04 MCP 與 CLI 出口、代理配對 | ✅ 完成 | `task/C04-mcp` | 配對完成是無需登入的 HTTP 端點（不是 op）；配對碼存在記憶體（重啟失效）；admin 代理也不能自己配對或改權限；`/mcp` 也接受擁有者登入碼 | 待確認：Claude Code 是否接受含點的工具名稱 `xbot.system.ping`（CP1 實測，不行就加底線別名）；tools/list 是否依權限過濾；MCP-8 預設範圍 |
| C03 HTTP 出口、SSE、前端型別 | ✅ 完成 | `task/C03-http` | 登入／登出／狀態是 HTTP 專用端點（處理 cookie，不是 op）；budget 用 402；CLI 不再需要 /mcp 備援 | 待確認：兩套「本機請求」判斷要不要合併；事件被清理後重連要不要送 reset；登出無法逐一撤銷 session |
| C05 第一批核心操作 | ✅ 完成 | `task/C05-core-ops` | 多了 `models.set_default`；approval_mode 值 ask/edits/auto/full（取自 OMB）；代理只看到自己的核准與稽核 | Bot 等人回答時還沒有回答的操作（chat.respond，C12）；核准後執行失敗時 approvals.wait 分不出來；刪除 Bot 沒關閉 product.sock；`xbot dev chat` 可移除 |
| W02 對話畫面最小版 | ✅ 完成 | `task/W02-chat` | 工作中再送訊息會顯示「忙碌中，先按停止」（伺服器不接受排隊）；Bot 的提問只顯示「在等你」（回答功能在 C12）；新增 `make e2e-docker`（真 Bot 電腦＋假模型） | 重新整理後看不到舊回合的步驟（需要 tasks.events 之類的 op）；開機中重新整理看不到進度；事件表沒存 thread_id |
| CP1（關卡 G0） | ✅ 通過 | tag `cp1` | 假模型與真模型（OpenCode Go deepseek-v4-flash）都通過；Claude Code 真的透過 MCP 建 Bot 並對話 | — |
| W11 對話完整版 | ✅ 完成 | `task/W11-chat-full` | Markdown 用 marked lexer 自建 React 元素（不經 innerHTML）；數學只顯示原始 TeX、Mermaid 當程式碼；「再試一次」用 chat.send 重送（會多一則訊息）；`registerCard` 給 C12 掛卡片 | 待補 op：重新命名／釘選／刪除對話串、模型清單、context 用量、retry；手機頁首是否照 L03 |
| E11 Model Proxy 完整版 | ✅ 完成 | `task/E11-proxy-full` | 花費以美元百萬分之一記；新增每供應商上限（給 OpenCode 省用）；OpenCode 自動帶 x-opencode-session；預設價格除 Claude 外待核對；migration 0005 | Bot 換到別的供應商要重啟電腦才生效；預算超過時 Hermes 的訊息是否夠清楚；日界線時區；80% 預警、EN-7 未做 |
| E13 Hermes 訂閱登入驗證 | ✅ 完成（原型） | `task/E13-oauth` | 結論：ChatGPT 方案可經 Hermes 使用、權杖只留在 Model Proxy（假伺服器測試，未用真帳號）；報告 `docs/spike-hermes-oauth.md` | **待使用者決定**：Hermes 用 Codex CLI 的 OAuth client 呼叫 ChatGPT 內部 API 有條款風險；OpenAI 已有官方「ChatGPT 方案使用」流程（自架可能要先申請）。ChatGPT 方案 Bot 要關掉 Hermes 的 tool_search；登入畫面、重新登入卡片未做 |
| C12 待我處理與核准 | ✅ 完成 | `task/C12-inbox` | Hermes 核准模式 ask/edits→manual、auto/full→smart（不用 off，守 SEC-2）；只有無人值守的工作 10 分鐘逾期；migration 0006（事件加 thread_id、inbox 重建）；回答卡片放在 WorkSteps 而非 registerCard | 待確認：full 是否也該 manual；外部代理送的訊息算不算無人值守；改 approval_mode 要重開電腦才生效；通知面板 B04/L14 畫面未做；代為核准在 C21 |
| E12 ACP 驅動與 Claude Code | ✅ 完成 | `task/E12-acp` | 轉接器改用 `@agentclientprotocol/claude-agent-acp@0.85.1`、`codex-acp@2.1.1`（Zed 舊套件已改名）；映像大 1 GB；憑證放容器內（非 volume），開機前重新複製；**合併後修正**：「完全放手」原本對到 bypassPermissions，違反 SEC-2，已改成 auto／agent 並加測試 | 主機與容器共用 refresh token 可能互相踢掉（未用真帳號測）；Codex workspace-write 在強化容器內可能不能用；macOS 的 Claude 登入在 Keychain 抓不到；換引擎時舊行程要關 |
| C11 Bot 用的 XBot MCP | ✅ 完成 | `task/C11-bot-mcp` | 橋接程式用 Python（標準函式庫）`/opt/xbot/xbot_mcp.py`；新增 ForBot 旗標，Bot 只看到 5 個工具（cards.post、approvals.request/wait/list、team.roster）；身分由 socket 決定、驗 worker key；沒做 files.share（用 file 卡片） | 核准後要不要像 LazyBoy 一樣在對話串叫醒 Bot；網頁還沒畫 card 訊息；花名冊沒有團隊名（X03）；合併後修正提問過期的寫入順序（偶發測試失敗） |
| B11 SSH 反向轉發原型 | ✅ 完成（CP2：照原設計繼續） | `task/B11-ssh` | 用 direct-streamlocal 取代 docker context ssh://；known_hosts 每台主機一個檔；測試主機用 DinD＋sshd 模擬 | 主機 sshd 設定要求見 CP2；ReleaseAddr／ReleaseIPC 留給 B13；斷線瞬間的模型請求會失敗；發現 E02 問題（上一輪剛結束就送下一則會卡住） |
| W12 設定頁（設定部分） | ✅ 完成 | `task/W12-settings` | 設定做成頁面（畫板是對話框）；金額用美元（Proxy 以美元記帳）；淺色主題補了幾個色票；外部代理頁留給 C21；電腦頁等 B12–B14 | 待決定：要不要全域預設引擎、顯示台幣、通知與語言設定、改核准模式要提示「重開電腦後生效」 |
| X01 限制常數 | ✅ 完成 | `task/X01-limits` | 新增 85 個常數（每個標 LazyBoy 檔案:行號），對照表 `docs/limits.md`；修正 ChatTextMax 100000→64000；PRD §18／TM-5／§12 依程式更正 | 待確認：§12「每 Bot 進行中對話 3」程式裡沒有；Bot 在群組的回覆要不要也截在 12,000 字；§19 的 20／30 分鐘提醒是 LazyBoy 規劃、非程式 |
| C21 代為核准與外部 MCP 權限 | ✅ 完成 | `task/C21-delegation` | 類別 external_send／spend_small／publish／sign；目前還沒有真的可代為核准的 op，用測試 op 驗證全流程；代理只能代為「允許」；MCP-8 預設只能碰自己建立的；速率預設每分鐘 60 次（自訂）；migration 0007；代理頁放進 W12 設定殼 /settings/advanced/agents | 代理能不能核准自己提出的請求（目前可以）；復原後是否還算次數；C16 欄位層級的「由 X 修改・復原」未做 |
| E02 修正：連續送訊息卡住＋看門狗 | ✅ 完成 | `task/E02-fix-backtoback` | 根因：Hermes 在 message.complete 後還在收尾，此時送的訊息被當成「插話」吞掉；改成等 session 閒置、送 queued、被插話時明確報錯；新增啟動看門狗（630 秒）與 CH-12 停滯看門狗（15／40 分鐘，照 LazyBoy） | 看門狗放棄後 Hermes 的遲到事件可能落到下一輪（LazyBoy 用 draining 旗標）；Hermes 自己開的背景回合尚未收成工作 |
| 修正偶發失敗的測試 | ✅ 完成 | `task/fix-flaky` | 3 個：SSE 補發與即時事件順序（產品 bug）、提問卡剛建立時回答被當成過期（產品 bug）、ipc 測試重用已關閉連線（測試問題）；壓力測試全數通過 | ipc 的 Ensure 重開時會硬切進行中的請求，可改優雅關閉 |
