# 引擎

引擎是 Bot 用來思考和動手的程式。XBot 自己不寫這部分，而是接上現成的引擎，把它們的輸出整理成一樣的對話畫面。

| 引擎 | 用什麼模型 | 適合 |
|---|---|---|
| **Hermes**（預設） | 你在「設定 → AI 模型」接上的任何模型（API 金鑰、ChatGPT 方案、OpenCode Go、本機模型） | 大部分工作。技能、記憶、應用程式都完整支援 |
| **Claude Code** | 你的 Claude 訂閱（Pro／Max） | 寫程式、改專案 |
| **Codex** | 你在 Codex CLI 的登入（ChatGPT 方案） | 寫程式 |
| **Gemini CLI** | 你的 Google 帳號登入 | 一般工作、寫程式 |
| **OpenCode** | 你的 OpenCode Go 或 Zen 登入 | 寫程式 |
| **Grok** | 你的 Grok 登入 | 一般工作 |

不管哪個引擎，都跑在 Bot 自己的電腦裡，用一樣的工具（桌面、瀏覽器、終端機、檔案）和 XBot 的工具（傳話、待我處理、卡片）。

換引擎：「Bot 設定 → 進階」。全部 Bot 用哪個方案，在「設定 → AI 模型 → 方案」最下面的「各個 Bot 用哪個」。

## 用你的訂閱（Hermes 以外的引擎）

Hermes 以外的引擎用的是**你在跑 XBot 的這台電腦上登入的帳號**。

1. 先在這台電腦登入。`make doctor` 會顯示每個的登入狀態：

   | 引擎 | 登入指令 |
   |---|---|
   | Claude Code | `claude`，再輸入 `/login` |
   | Codex | `codex login` |
   | Gemini CLI | `gemini`，選 Login with Google |
   | OpenCode | `opencode auth login`（OpenCode Go 或 Zen） |
   | Grok | `grok login` |

2. 把 Bot 的引擎設成那一個。
3. **逐個 Bot 同意**：勾「同意使用我的訂閱」。這個勾選**預設不勾**。沒同意之前，這個 Bot 用那個引擎不能工作。

同意後會發生什麼：

- 這台電腦上那個 CLI 的登入憑證，會**唯讀**複製進這個 Bot 的電腦，只有這個 Bot 拿得到。每次開機前重新複製一次，換到最新的權杖。
- 介面上會標「這個 Bot 用你的 ○○ 訂閱」。
- 它的工作會用掉你的方案額度。
- 它電腦裡的程式讀得到這份憑證。這是「金鑰不進 Bot 電腦」規則的例外，所以要你逐個同意。

收回：在 Bot 設定或引擎一覽按「收回」。XBot 會立刻停掉它正在跑的引擎，並清掉電腦裡的憑證（電腦暫時連不上時，下次開機前清掉）。

指令列：

```bash
xbot engines status
xbot engines link_credentials   --bot_id <id> --engine claude-code
xbot engines unlink_credentials --bot_id <id>
```

只有你本人能同意或收回，代理不行。

首次設定（「接上 AI」那一步）目前只列出 Claude Code 和 Codex；其他引擎請建好 Bot 後在 Bot 設定換。

### Claude 訂閱只給 Claude Code 用

XBot 不讓其他引擎借用 Claude 的登入（Anthropic 的使用條款不允許）。Hermes 拿不到；OpenCode 的登入檔裡如果有 Claude 的登入，複製進 Bot 電腦前會先拿掉。想讓 Hermes 用 Claude 的模型，請接 Anthropic 的 API 金鑰。

## 額度用完

訂閱或 API 的額度用完、被限速、餘額不足、登入過期時，XBot 會用白話結束那一輪，並在對話和「待我處理」出一張「額度用完」卡，寫著什麼時候會恢復（知道的話）。

卡片上可以一鍵**換到別的引擎或模型**，並選擇把剛剛那則訊息在同一個對話串再送一次。要用訂閱、還沒同意過的引擎，換的時候會一起請你同意。換的是整個 Bot 的引擎，不是只有這段對話。Bot 正在做事時不能換。

指令列：`xbot engines switch --bot_id <id> --engine hermes --inbox_id <卡片 id> --resend`。

## 權限等級在各引擎的意思

四個權限等級（先問我／一般的事自己做／放手做／完全放手）對每個引擎都有效，XBot 會翻成各引擎自己的設定。就算是「完全放手」，也不會關掉引擎的危險指令檢查：Claude Code 不用「略過所有權限」模式、Gemini 不用 yolo、OpenCode 跑終端機指令一律先問、Grok 不送「一律允許」。

## 已知限制

- **技能與記憶**：Hermes 以外的引擎不會讀 XBot 的技能和記憶（那是給 Hermes 的）。
- **共用登入**：這台電腦和 Bot 的電腦用同一份登入。CLI 自己換新權杖時，可能碰到唯讀的憑證，或兩邊互相踢掉，要在這台電腦重新登入。這一版還沒用真的帳號長時間測過。
- **Gemini**：這台電腦登出後，Bot 的 Gemini 不會啟動；登入失效時會卡在等登入碼，約 60 秒後才報「沒有登入」。
- **Codex 的寫檔模式**：Bot 的電腦是唯讀根目錄、沒有任何特權，Codex 的沙箱模式可能不能用。
- **macOS 的 Claude 登入**存在鑰匙圈，讀不到。XBot 本來就只支援 Linux。
- 新引擎需要新的 Bot 電腦映像：升級後執行 `make box`，再 `xbot computers update`。
