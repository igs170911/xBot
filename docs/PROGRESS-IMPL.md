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
| CP1（關卡 G0） | ✅ 通過 | tag `cp1` | 用假模型驗收；Claude Code 真的透過 MCP 建 Bot 並對話 | 真模型驗收待使用者同意使用金鑰 |
