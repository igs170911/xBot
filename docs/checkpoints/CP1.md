# CP1：M0 骨架完成（關卡 G0）

> 狀態：已通過自動驗收。依使用者「按照順序做下去」的指示，檢查點不停下等待；這份報告和 git tag `cp1` 隨時可以檢查，有問題再回頭改。

## 1. 示範步驟

```bash
make build            # 建出執行檔（make box 要先建過 Bot 電腦映像）
scripts/test-cp1.sh   # 全自動：開服務 → 設模型 → 配對 → Claude Code 透過 MCP 建 Bot 並對話 → 網頁截圖
```

手動試（約 5 分鐘）：
1. `make dev`，打開 http://localhost:5181 → 左上 ＋ 新增 Bot。
2. 還沒設定模型時，對話頁會出現「還沒設定模型」卡片，填入供應商網址、金鑰、模型。
   沒有金鑰可以先開假模型：`bin/xbot dev fake-model`，網址填 `http://127.0.0.1:8798/v1`、模型 `fake-model`。
3. 對 Bot 說話，回覆會即時出現；工作步驟會收合成「工作了 N 分鐘 · M 步」。
4. 接 Claude Code：照 `docs/mcp-setup.md` §7「第一次對話」。

## 2. 證據

| 項目 | 結果 |
|---|---|
| G0：Claude Code（真的 claude 指令）透過 MCP 建 Bot、送訊息、等回答 | ✅ `BOT_REPLY=OK`；服務端查得到 Bot 與代理的稽核紀錄 |
| G0：網頁同步看到對話 | ✅ 截圖 `docs/checkpoints/cp1-web.png` |
| 真金鑰不進 Bot 電腦 | ✅ Docker 測試檢查容器環境變數與 /hermes 所有檔案 |
| `make check`、`make test-race`、`make e2e`（17 通過）、`make test-docker`、`make e2e-docker` | ✅ |
| **真模型**（OpenCode Go `deepseek-v4-flash`，2026-10-18）：同一套流程 | ✅ Bot 用真模型回答「OK」並自我介紹；截圖 `docs/checkpoints/cp1-web-real.png`；只跑一次 |
| 含點的 MCP 工具名稱（`xbot.bots.create`）在 Claude Code 可用 | ✅（C04 的待確認事項已解決） |

## 3. M0 完成的卡

C00、C06、C01、B01、E01、W01、C02、B02、E02、C03、C04、C05、W02（細節見 `docs/PROGRESS-IMPL.md`）。

途中修正：`chat.wait_until_settled` 偶爾回「等你」卻沒附問題內容（事件寫入順序錯誤，已修正並加 `make test-race`）。

## 4. 和規格不一樣的地方

- 驗收用**假模型**（永遠回 OK）。你機器上只有 OpenCode Go 的金鑰，你說過要省用，所以沒有拿它跑；要用真模型：`CP1_BASE_URL=… CP1_MODEL=… CP1_API_KEY=… scripts/test-cp1.sh`。
- 其餘差異見 `docs/PROGRESS-IMPL.md` 各卡的「和規格不一樣的地方」。

## 5. 需要你決定的事（不急，預設照目前做法）

1. CP0 的三題（安裝在家目錄、linger 每次問、Node 26）目前都用 A。
2. `approval_mode` 四段：先問我／一般的事自己做／放手做／完全放手（ask/edits/auto/full），名稱可以嗎？
3. ~~要不要用你的 OpenCode Go 金鑰跑一次真模型驗收？~~ 使用者已同意（2026-10-18）。結果：
   - OpenCode Zen 的免費模型（deepseek-v4-flash-free 等）目前回「Model is unavailable」。
   - OpenCode Go 規定要帶 `x-opencode-session` 與自己的 User-Agent（已交給 E11 實作）。
   - 但你的 OpenCode Go 金鑰連官方 `opencode run` 都回「Invalid credential」，金鑰可能過期或額度已用完。
   - 請在終端機執行 `opencode auth login` 重新登入（或到 opencode.ai 確認額度），之後跑：
     `CP1_BASE_URL=https://opencode.ai/zen/go/v1 CP1_MODEL=deepseek-v4-flash CP1_API_KEY=… scripts/test-cp1.sh`（E11 合併後才會自動帶 session 標頭）。

## 6. 下一階段：M1（一個 Bot＋自己的電腦）

進行中：B11（遠端主機 SSH 轉發原型，用本機容器模擬遠端主機）、E11（Model Proxy 完整版）、C12（待我處理與核准）、W11（對話完整版）。
接著：B12 主機管理（`make host-add`／`deploy`）、B13、B14、E12（Claude Code 當引擎）、E13、C11、W12 → CP2～CP4。


## 7. 真模型驗收（2026-10-18 補做）

使用者重新登入 OpenCode 後，E11 合併（自動帶 `x-opencode-session`）再跑一次 `scripts/test-cp1.sh`，用 OpenCode Go 最便宜的 `deepseek-v4-flash`：✅ 通過。注意 auth.json 裡要用 `opencode` 那把金鑰（`opencode-go` 那把無效）。
