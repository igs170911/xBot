-- 0018（C34）：連接應用程式與登入（PRD SK-5、SK-6、BX-10、SEC-5）。
--
-- app_connection：一個連接＝一個 MCP 服務的一個帳號（目錄裡的應用程式，或自訂的 MCP）。
--   name 是它在 Bot 設定裡的名字（mcp_servers.<name>），全部連接之間不重複。
--   transport：http（遠端網址，一律由 XBot 代為轉送，權杖留在服務端）或 stdio（在 Bot 電腦裡執行的本機程式）。
--   auth：none／api_key／oauth2。secret 是加密後的 JSON（權杖、金鑰、標頭值、環境變數值），永遠不回傳。
--   secret_hint：給畫面看的末四碼。status：ok／needs_signin／expired／error／disabled／checking。
--   all_bots：1＝所有 Bot 都能用；0＝只有 app_grant 列出的 Bot。
-- app_grant：只給指定的 Bot 用時，哪些 Bot 可以用（SEC-5：每 Bot 隔離）。
-- app_use：最近使用（I02），每個連接留最新幾筆。
-- login_share：分享登入（E09）。snapshot 是分享前對方原本的登入（加密），復原時寫回去。
CREATE TABLE app_connection (
    id             TEXT PRIMARY KEY,
    app_id         TEXT NOT NULL,
    name           TEXT NOT NULL UNIQUE,
    title          TEXT NOT NULL,
    transport      TEXT NOT NULL,
    url            TEXT NOT NULL DEFAULT '',
    command        TEXT NOT NULL DEFAULT '',
    args           TEXT NOT NULL DEFAULT '[]',
    auth           TEXT NOT NULL DEFAULT 'none',
    account        TEXT NOT NULL DEFAULT '',
    secret         BLOB,
    secret_hint    TEXT NOT NULL DEFAULT '',
    header_names   TEXT NOT NULL DEFAULT '[]',
    env_names      TEXT NOT NULL DEFAULT '[]',
    status         TEXT NOT NULL DEFAULT 'checking',
    error          TEXT NOT NULL DEFAULT '',
    tools          TEXT NOT NULL DEFAULT '[]',
    disabled_tools TEXT NOT NULL DEFAULT '[]',
    all_bots       INTEGER NOT NULL DEFAULT 1,
    enabled        INTEGER NOT NULL DEFAULT 1,
    checked_at     INTEGER NOT NULL DEFAULT 0,
    last_used_at   INTEGER NOT NULL DEFAULT 0,
    created_by     TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX app_connection_app ON app_connection(app_id);

CREATE TABLE app_grant (
    connection_id TEXT NOT NULL REFERENCES app_connection(id) ON DELETE CASCADE,
    bot_id        TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (connection_id, bot_id)
);
CREATE INDEX app_grant_bot ON app_grant(bot_id);

CREATE TABLE app_use (
    id            TEXT PRIMARY KEY,
    connection_id TEXT NOT NULL REFERENCES app_connection(id) ON DELETE CASCADE,
    bot_id        TEXT NOT NULL DEFAULT '',
    tool          TEXT NOT NULL,
    ok            INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL
);
CREATE INDEX app_use_connection ON app_use(connection_id, created_at);

CREATE TABLE login_share (
    id         TEXT PRIMARY KEY,
    source_bot TEXT NOT NULL,
    targets    TEXT NOT NULL DEFAULT '[]',
    sites      TEXT NOT NULL DEFAULT '[]',
    snapshot   BLOB,
    actor      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    undone_at  INTEGER NOT NULL DEFAULT 0
);
