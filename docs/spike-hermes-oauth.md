# E13 驗證：Hermes 訂閱登入能不能走 Model Proxy

> 任務卡：E13（PLAN）。相關需求：EN-2a、EN-3、SEC-5、PRD §14。
> Hermes 版本：`box/HERMES_VERSION` = `05eecbcd972c8737ebc7722ea08aab47fb538043`（從 `xbot-box:dev` 映像的 `/opt/hermes` 讀原始碼）。
> 日期：2026-10-18。沒有用任何真實帳號登入，也沒有讀 `~/.codex` 等本機憑證；測試全部用假的登入伺服器和假的 Codex 端點。

## 結論（先講）

**做得到。** ChatGPT 方案（Hermes 的 `openai-codex`）可以讓 OAuth 權杖只放在 XBot 服務的 Model Proxy，不進 Bot 的電腦：

- Bot 電腦裡的 Hermes 照現在的設定（E02）：自訂供應商 `product`、呼叫方式 `codex_responses`、網址指到代理、金鑰是 worker-key。Hermes 不需要改。
- Model Proxy 收到 `POST /model/openai-codex/v1/responses` 後：換上真的存取權杖、加上 `ChatGPT-Account-ID` 等標頭、把請求改成 Codex 端點要的樣子，送到 `https://chatgpt.com/backend-api/codex/responses`，串流原樣回傳。
- 權杖快到期或供應商回 401 時，代理自己用 refresh token 換新並加密存回；同一個供應商同時只換一次（refresh token 只能用一次）。
- 登入由 XBot 服務跑「裝置碼登入」：你在自己的瀏覽器打開網址、輸入代碼；Bot 的電腦完全不參與。

已做出最小原型（見「原型」），測試全過。**還沒用真帳號驗證**，正式上線前要做一次實測（見「待確認」）。

**但條款要先確認。** Hermes 用的是 Codex CLI 的公開 OAuth client（`app_EMoamEEZ73f0CkXaXp7hrann`）去打 ChatGPT 的內部端點 `backend-api/codex`。OpenAI 在 2026-09 DevDay 之後推出了正式的「Sign in with ChatGPT／ChatGPT 方案用量」計畫，走的是公開的 `api.openai.com/v1/responses`，而且**Hermes Agent 是首批合作夥伴之一**，但釘選的 Hermes 版本還沒用這個新流程。建議見最後一節。

## 1. Hermes 怎麼做 openai-codex 登入

原始碼：`hermes_cli/auth_codex.py`、`auth_codex_browser.py`、`auth_constants.py`、`agent/codex_headers.py`。

### 1.1 裝置碼登入（預設）

| 步驟 | 請求 | 說明 |
|---|---|---|
| 1 要代碼 | `POST https://auth.openai.com/api/accounts/deviceauth/usercode`，JSON `{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann"}` | 回 `device_auth_id`、`user_code`、`interval`（Hermes 取 `max(3, interval)` 秒）。429 時退避重試。 |
| 2 給使用者看 | 網址 `https://auth.openai.com/codex/device`＋`user_code` | 使用者在任何一台有瀏覽器的機器上登入 ChatGPT、輸入代碼。 |
| 3 等結果 | `POST .../api/accounts/deviceauth/token`，JSON `{"device_auth_id","user_code"}` | 403／404 = 還沒完成；200 回 `authorization_code`、`code_verifier`。最多等 15 分鐘。 |
| 4 換權杖 | `POST https://auth.openai.com/oauth/token`（表單）`grant_type=authorization_code`、`code`、`redirect_uri=https://auth.openai.com/deviceauth/callback`、`client_id`、`code_verifier` | 回 `access_token`（JWT）、`refresh_token`、`id_token`。 |

另有瀏覽器登入（PKCE）：重新導向固定是 `http://localhost:1455/auth/callback`，只能在「跑瀏覽器的那台機器」上收。XBot 服務可能在別台主機，所以 **XBot 用裝置碼登入**。

### 1.2 權杖存哪、怎麼換新

- Hermes 存在容器內的 `~/.hermes/auth.json`（`providers.openai-codex.tokens`），也會去讀 `~/.codex/auth.json`（Codex CLI 的檔案）來「自我修復」。**這正是 XBot 不能讓 Hermes 自己登入的原因**：權杖會落在 Bot 的電腦裡。
- 換新：`POST /oauth/token`，`grant_type=refresh_token`、`refresh_token`、`client_id`。存取權杖到期前 120 秒就換（`CODEX_ACCESS_TOKEN_REFRESH_SKEW_SECONDS`）。
- refresh token **會輪換、只能用一次**：同時換兩次會被回 `refresh_token_reused`，整組失效。Hermes 用檔案鎖確保一次只換一次。
- 換新失敗的分類：401／403 或 `invalid_grant`、`invalid_token`、`invalid_request`、`refresh_token_reused` → 要重新登入；429 → 只是暫時限制，refresh token 仍有效。

### 1.3 送給 ChatGPT 的請求長相

- 網址：`POST https://chatgpt.com/backend-api/codex/responses`（OpenAI Responses API 格式）；模型清單 `GET .../codex/models?client_version=<版本>`（回 `{"models":[{slug,…}]}`，不是 OpenAI 的 `data` 格式）。
- 標頭：
  - `Authorization: Bearer <access_token>`
  - `ChatGPT-Account-ID: <JWT 的 https://api.openai.com/auth.chatgpt_account_id>`
  - `x-openai-internal-codex-residency: <chatgpt_data_residency>`（有資料駐留的工作區沒帶會 401）
  - `originator: hermes-agent`、`User-Agent: HermesAgent/<版本>`（程式註解：「OpenAI 要求第三方 harness 自報身分」；非官方網址時改報 `codex_cli_rs`）
  - `session_id: <對話 id>`、`x-client-request-id: <和 body 的 prompt_cache_key 相同>`
- 內容：`model`、`instructions`、`input`、`tools`、`tool_choice`、`parallel_tool_calls`、`store:false`、`stream:true`、`prompt_cache_key`、`reasoning:{effort,summary:"auto"}`、`include:["reasoning.encrypted_content"]`。**不送** `max_output_tokens`、`prompt_cache_retention`（Codex 端點會 400）。
- Hermes 判斷「是不是 Codex 端點」只看兩件事（`classify_responses_route`）：供應商叫 `openai-codex`，或網址是 `chatgpt.com/backend-api/codex`。

## 2. 指向代理行不行

XBot 的 Hermes 設定（E02 `internal/engine/hermes/provision.go`）是自訂供應商 `product`，`transport: codex_responses`，網址 `http://127.0.0.1:8642/model/<供應商>/v1`。這時 Hermes 把代理當成一般 Responses 端點：

| 項目 | 經代理時 Hermes 送的 | 代理要做的 |
|---|---|---|
| 路徑 | `POST …/v1/responses` | 轉到 `chatgpt.com/backend-api/codex/responses` |
| 串流 | 一律 `stream:true`（`run_codex_stream`），`store:false` | 保險起見強制 `stream:true`、`store:false` |
| 驗證 | `Authorization: Bearer <worker-key>` | 驗 worker-key，換成真的存取權杖 |
| 帳號標頭 | 不送 | 從 JWT 讀出 `ChatGPT-Account-ID`、駐留區域，自己加 |
| 身分標頭 | OpenAI SDK 的 UA | 加 `originator`、`User-Agent` |
| `session_id`／`x-client-request-id` | 不送 | 用 XBot 的對話串 id；`x-client-request-id` 取 body 的 `prompt_cache_key` |
| `max_output_tokens` | 有設上限時會送 | 拿掉 |
| `prompt_cache_retention` | 一般網址不會送 | 拿掉（保險） |
| `temperature`、`top_p` | Responses 路徑通常不送 | 拿掉（Docker Agent 的 ChatGPT 方案文件說會被丟掉；待實測） |
| 模型清單 | 一般不會呼叫 | `GET models` 補上 `client_version` |

和 Hermes 直連 `openai-codex` 相比，經代理會少掉幾個只對 Codex 端點開的功能（因為 Hermes 不知道背後是 Codex）：

1. **`tool_search` 名稱衝突（要處理）**：Codex 端點保留了 `tool_search` 這個名稱，Hermes 直連時會改名成 `hermes_tool_search`。經代理不會改名；而 Hermes 的「工具搜尋」在有 MCP 工具延後載入時會出現 `tool_search` 橋接工具（XBot 有 desktop MCP，很可能會觸發）。解法二選一：(a) 用 ChatGPT 方案的 Bot，在 Hermes 設定加 `tools.tool_search.enabled: off`（最簡單，E02 provisioning 加一行）；(b) 代理在請求裡改名、在串流回應裡改回來（較複雜）。**原型沒做，列為後續。**
2. 原生網頁搜尋（`web.search_backend: openai-native` 才會用）：XBot 用自己的瀏覽器工具，影響不大。
3. gpt-5.6 的伺服器端壓縮（`context_management`）只在直連時開：經代理時用 Hermes 自己的壓縮，功能一樣，只是比較耗 token。
4. 思考強度的允許值：經代理時用模型的一般規則，Codex 端點收得下。
5. `-900k` 大上下文的模型別名只在直連時出現：XBot 的模型名稱用原本的就好。

另一條路是讓 Hermes 用 `openai-codex` 供應商、`HERMES_CODEX_BASE_URL` 指到代理、在容器的 `auth.json` 放一個「假權杖」（worker-key）。這樣上面 1～5 都會照直連的方式做，但 Hermes 會讀寫容器裡的權杖檔、會試著換新（打 auth.openai.com）、會打 `/wham/usage` 之類的額度查詢端點，和 E02「容器只有 worker-key」的單純設定差太多，**不建議**；只有 1～5 實測後真的影響很大時才考慮。

## 3. 代理要加什麼（原型已實作）

- **登入方式欄位**：供應商多一個 `auth`（空白 = API 金鑰；`oauth_codex` = ChatGPT 方案）。權杖整組（access、refresh、id token、到期時間、帳號、駐留區域）用和 API 金鑰**同一把金鑰、同一個欄位**加密（AES-GCM，aad 綁 `provider:<id>`），存在 setting 表 `model.provider.<id>`。列表、輸出、紀錄都只顯示「已設定」。
- **換新**：
  - 每次請求前：存取權杖剩不到 120 秒就先換新。
  - 供應商回 401：強制換新一次再送（這時還沒送任何位元組給容器）。
  - 同一個供應商用一把鎖，排隊的請求發現別人剛換好就直接用新的，不會重複用 refresh token。
  - 換不了（refresh token 失效）：回容器 `401 oauth_relogin_required`「訂閱登入已失效，請到模型設定重新登入」，不送出請求。
- **路徑限制**：訂閱登入的供應商只開放 `POST responses` 和 `GET models`，其他路徑 403。
- **記帳**：只記 token，不算錢（月費方案），也不標「沒有價格」。
- **不能混用**：不能用 `set_provider`／`update_provider` 的金鑰蓋掉訂閱登入；`models.probe` 對它回「不用探測」。

## 4. 使用者怎麼登入（XBot 服務跑，不經過 Bot 的電腦）

新增兩個操作（MCP 優先，HTTP／MCP／CLI／前端型別都由它產生）：

1. `xbot.models.oauth_start {kind:"openai-codex"}` → XBot 服務向 auth.openai.com 要代碼，回 `login_id`、`user_code`、`verification_url`、`interval_seconds`。`device_auth_id` 只留在服務端。
2. 畫面顯示「打開 https://auth.openai.com/codex/device，輸入 ABCD-1234」。使用者在自己的手機或電腦登入 ChatGPT。
3. 畫面每隔 `interval_seconds` 呼叫 `xbot.models.oauth_complete {login_id, default_model?}`：還沒好回 `pending`（查太快時代理不會去打擾登入伺服器）；好了就換權杖、加密存成供應商 `openai-codex`（呼叫方式 `codex_responses`），可順便設成預設模型。

兩個操作都只限你本人（`ownerOnly`），Bot 不能代為登入（`NoDelegate`）。登入最多等 15 分鐘，同時最多 8 個進行中的登入。

## 5. 風險

| 風險 | 說明 | 建議 |
|---|---|---|
| **OpenAI 條款** | Hermes 現在的作法是借用 Codex CLI 的 OAuth client 打 `chatgpt.com/backend-api/codex`（內部端點）。網路上找得到的說法：第三方工具用 Codex OAuth「還能用」且廣泛被使用（OpenClaw、OpenCode 等），但 OpenAI 沒有公開條款明文允許。2026-09-29 DevDay 後 OpenAI 推出正式的 ChatGPT 方案用量計畫：要用自己的 client（`dynamic_agent_client` 動態註冊拿 `oaiapp_…`），scope `offline_access resource.invoke chatgpt.tokens.use.direct`，請求送 `api.openai.com/v1/responses`，**明文要求不要打 backend-api**；開放給開源專案、個人本機專案和部分私有 app，**付費或遠端託管的 app 要先填申請表**。Hermes Agent 是首批合作夥伴，但釘選版本還沒用新流程。 | 原型先照 Hermes 現況（行為一致、Hermes 不用改）。**正式版改走官方流程**（見建議）。XBot 若有付費或代管形態，要先申請。 |
| 一個帳號多個 Bot 共用 | 所有用 ChatGPT 方案的 Bot 共用同一組權杖與額度；方案有每週／每 5 小時的用量上限，用完回 429 或串流中的 usage-limit 錯誤。 | 照 EN-5 備援；429 時發「待我處理」卡提示換模型。後續：額度查詢顯示在用量頁。 |
| refresh token 被別的程式用掉 | 你同一個帳號在 Codex CLI 用的是**不同的登入**（不同 session），彼此不衝突；但如果有人把 XBot 的權杖拷到別處用，會互相讓對方失效。 | XBot 的權杖只在服務端，不匯出。失效時清楚提示重新登入。 |
| 權杖外洩 | 權杖等同 ChatGPT 帳號的模型使用權。 | 只存密文；容器只有 worker-key；回應與紀錄都遮蔽；換新的錯誤訊息不帶權杖。 |
| 內部端點改格式 | `backend-api/codex` 不是公開 API，標頭或欄位可能隨時改。 | 跟著 Hermes 版本更新（Hermes 會先遇到）；改走官方 `api.openai.com/v1/responses` 後風險小很多。 |
| `originator` 要報誰 | 請求是 Hermes 產生、XBot 轉送。原型照 Hermes 報 `hermes-agent`，UA 寫 `HermesAgent (XBot/<版本>)`。 | 待確認（見下）。 |
| Claude 訂閱 | EN-2a：**不讓 Hermes 借用 Claude Code 的登入**。Anthropic 已更新條款，禁止在第三方工具使用訂閱的 OAuth 權杖。 | 原型只支援 `openai-codex`；Claude 訂閱只經 Claude Code 本身（方案 A，EN-2b）。 |

## 6. 其他訂閱登入（簡要）

都是同一個模式：XBot 服務跑登入、權杖加密存在代理、代理換新並加標頭。差別在登入方式與標頭：

| 供應商 | Hermes 怎麼登入 | 送請求時 | 能不能走代理 | 備註 |
|---|---|---|---|---|
| **GitHub Copilot** | GitHub 裝置碼登入（`github.com/login/device/code`，client `Iv1.b507a08c87ecfe98` = VS Code 的 GitHub App），拿 `ghu_`／`gho_` 權杖；再到 `api.github.com/copilot_internal/v2/token` 換短效的 Copilot JWT（約 30 分鐘），JWT 裡的 `proxy-ep` 決定 API 網址（預設 `api.githubcopilot.com`） | 標頭 `Editor-Version: vscode/1.104.1`、`Copilot-Integration-Id: vscode-chat`、`Openai-Intent: conversation-edits`、`x-initiator` | 可以：代理保管 GitHub 權杖，定時換 Copilot JWT，加標頭。chat/completions 與 responses 都有 | 冒用 VS Code 身分，條款風險比 ChatGPT 高（GitHub 條款限制非官方用戶端）。也有 `copilot-acp`（Copilot CLI 經 ACP），那條要 CLI 在容器裡，屬方案 A。 |
| **Nous Portal** | 裝置碼登入 `portal.nousresearch.com`，scope `inference:invoke`，拿到可呼叫推論的 JWT（invoke JWT），refresh 換新 | `inference-api.nousresearch.com/v1`，一般 Bearer | 可以，最簡單：一般 OpenAI 相容 API，代理換新權杖即可 | Nous 自家服務，條款風險低（Hermes 就是 Nous 做的）。免費帳號的推論網址不同（`welcome-api…`），由登入回應決定。 |
| **Qwen** | 不自己登入：讀 `~/.qwen/oauth_creds.json`（Qwen Code CLI 的檔案），用 `chat.qwen.ai/api/v1/oauth2/token` 換新 | `portal.qwen.ai/v1`，Bearer | 可以，但要 XBot 自己實作 Qwen 的裝置碼登入（Hermes 沒有，靠 qwen CLI） | 借用 Qwen Code 的 client。 |
| **xAI（Grok）** | 裝置碼登入 `auth.x.ai`（OIDC discovery），scope 含 `api:access`、`offline_access`；存取權杖約 6 小時，提前 1 小時換新 | `api.x.ai/v1` Responses，Bearer | 可以（同 ChatGPT 模式，不需要特殊帳號標頭） | 需要對應的 Grok 訂閱。 |
| **MiniMax** | `user_code` 授權（PKCE）`api.minimax.io`（中國區 `api.minimaxi.com`），refresh 換新 | Anthropic Messages 格式 `…/anthropic`，Bearer | 可以：代理已支援 messages 路徑 | |

共通需要：OAuthClient 介面（開始登入、查結果、換新、預設網址），原型已抽出（`internal/proxy/oauth.go`），新增一種只要再寫一個實作和標頭函式。

## 7. 原型（branch `task/E13-oauth`）

| 檔案 | 內容 |
|---|---|
| `internal/proxy/oauth.go` | `OAuthTokens`（Secret 欄位，印不出來）、`OAuthClient` 介面、`Providers.Fresh`（到期／401 換新，每個供應商一把鎖）、`StartLogin`／`CompleteLogin`（進行中的登入只在記憶體，15 分鐘過期，查詢間隔限制） |
| `internal/proxy/oauth_codex.go` | `CodexOAuth`（裝置碼登入、換權杖、換新，錯誤分類照 Hermes）、JWT 帳號資訊、`codexBody`（改請求）、`setCodexHeaders` |
| `internal/proxy/provider.go`、`store.go`、`dbstore.go` | 供應商多 `Auth`；訂閱登入時整組權杖加密存在原本的密文欄位 |
| `internal/proxy/handler.go` | 讀供應商時順便換新；路徑限制；Codex 改寫與標頭；401 換新重送；訂閱不算錢 |
| `internal/models/oauth.go` | `xbot.models.oauth_start`、`xbot.models.oauth_complete` |
| `internal/limits/proxy.go` | 換新提前量 120 秒、登入 15 分鐘、查詢最短 3 秒、同時 8 個登入、HTTP 20 秒、回應 1 MiB |
| 測試 | `internal/proxy/oauth_test.go`（假登入伺服器＋假 Codex 端點：登入只存密文、過期、標頭與內容改寫、只開放 responses、到期前換新、401 換新重送、8 個並行請求只換新一次、要重新登入時不送出、紀錄沒有權杖）；`internal/models/oauth_test.go`（操作、只限本人、輸出不含權杖、不能用金鑰蓋掉、不探測） |

沒做（後續）：前端登入畫面（W12／E 系列畫板）、`tool_search` 處理、重新登入的「待我處理」卡、額度顯示、官方 ChatGPT 方案流程、其他供應商。

## 8. 待確認

1. **條款**：XBot 能不能用 Codex CLI 的 client 打 `backend-api/codex`？還是一開始就申請／走官方「ChatGPT 方案用量」流程（XBot 是自架服務，可能算「遠端託管」，要填申請表）？
2. **`originator`／User-Agent** 要報 `hermes-agent`（請求內容是 Hermes 產生）還是 XBot 自己的名字？官方流程下應該用 XBot 註冊到的 client。
3. **真帳號實測**（需要你本人登入一次）：`temperature`／`top_p` 是否真的要拿掉、`tool_search` 是否真的會 400、串流中額度用完的錯誤格式、`models` 的回應能不能給設定頁用。
4. 一個 XBot 只登入一個 ChatGPT 帳號就好，還是要能登入多個（例：公司帳號＋個人帳號，代號 `openai-codex`、`openai-codex-2`）？原型支援用不同代號登入多個。

## 9. 建議

1. **採用「權杖留在代理」的架構**（本文件的原型）：Hermes 不用改、容器裡只有 worker-key，符合 SEC-5。ChatGPT 方案就不需要 Codex CLI＋方案 A 才能用。
2. **正式版改走 OpenAI 官方的 ChatGPT 方案流程**：代理的部分幾乎不變（Bearer＋`api.openai.com/v1/responses`，`store:false`、`stream:true`，甚至不需要帳號標頭），只換登入方式（動態註冊 client、scope `chatgpt.tokens.use.direct`、`127.0.0.1` 回呼）。官方文件對「自架主機」的建議是在有瀏覽器的電腦上登入、再把憑證傳到主機；對 XBot 來說就是：服務在本機時直接用 `127.0.0.1` 回呼，服務在遠端時由 `xbot` 指令列在你的電腦上登入、把權杖經操作 API 送進服務（仍不進 Bot 電腦）。動手前先確認 XBot 的資格（開源？託管？）。
3. 在官方流程確認前，原型的 `oauth_codex` 可以先留著但**不在畫面上開放**，或標示「實驗中」並附條款提醒，由你決定。
4. 先處理 `tool_search`：ChatGPT 方案的 Bot 在 Hermes 設定加 `tools.tool_search.enabled: off`。
5. 替代方案（做不到或條款不允許時）：Codex CLI 經 ACP（E12，方案 A：憑證逐 Bot 同意後唯讀掛載進容器）。缺點是權杖會進容器、每個 Bot 要各自同意、不能用 Hermes 的工具與記憶。

## 參考來源

- Hermes 原始碼（釘選版）：`hermes_cli/auth_codex.py`、`auth_codex_browser.py`、`auth_constants.py`、`copilot_auth.py`、`auth_nous.py`、`auth_qwen.py`、`auth_xai.py`、`auth_minimax.py`、`agent/codex_headers.py`、`agent/transports/codex.py`、`agent/codex_runtime.py`、`agent/codex_responses_adapter.py`、`hermes_cli/codex_models.py`、`hermes_cli/config_defaults.py`
- [OpenAI：Codex Authentication](https://developers.openai.com/codex/auth)
- [OpenAI：Sign in with ChatGPT（網站）](https://developers.openai.com/siwc/website)
- [OpenAI：ChatGPT 方案用量 — Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
- [OpenAI：ChatGPT 方案用量 — Registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)
- [OpenAI：ChatGPT 方案用量 — Self-hosted VMs](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms)
- [Developers Digest：Sign in with ChatGPT: Run Coding Agents on Your Plan (2026)](https://www.developersdigest.tech/blog/sign-in-with-chatgpt-coding-agents-plan-usage-2026)
- [Puter：OpenAI OAuth 教學（第三方使用 Codex OAuth 的現況）](https://developer.puter.com/tutorials/openai-oauth/)
- [Docker Agent：ChatGPT 供應商](https://docs.docker.com/ai/docker-agent/providers/chatgpt.md)
- [OpenClaw：OpenAI 驗證](https://docs.openclaw.ai/providers/openai/authentication)
