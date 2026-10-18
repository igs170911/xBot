-- 0001：第一版資料表（PRD §10）。
-- 慣例：id 是 TEXT（時間在前的 32 位十六進位）；時間是 unix 毫秒 INTEGER；JSON 欄位存 TEXT。
-- 流水號的表（event、audit_log、usage_entry）用 INTEGER 自動遞增，方便斷線續傳與依序讀取。

-- 主機：這台主機（本機 Docker socket）或遠端主機（ssh://xbot@…），PRD §16。
CREATE TABLE host (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL CHECK (kind IN ('local', 'ssh')),
    address      TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'online', 'offline', 'updating')),
    arch         TEXT NOT NULL DEFAULT '',
    max_running  INTEGER NOT NULL DEFAULT 0,
    resources    TEXT NOT NULL DEFAULT '{}',
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE TABLE bot (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    soul            TEXT NOT NULL DEFAULT '',
    color           TEXT NOT NULL DEFAULT '',
    avatar          TEXT NOT NULL DEFAULT '{}',
    engine          TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    effort          TEXT NOT NULL DEFAULT '',
    fallback        TEXT NOT NULL DEFAULT '[]',
    approval_mode   TEXT NOT NULL DEFAULT '',
    tool_scope      TEXT NOT NULL DEFAULT '{}',
    contacts_policy TEXT NOT NULL DEFAULT '{}',
    team_id         TEXT,
    reports_to      TEXT REFERENCES bot(id) ON DELETE SET NULL,
    is_chief        INTEGER NOT NULL DEFAULT 0,
    box_limits      TEXT NOT NULL DEFAULT '{}',
    idle_minutes    INTEGER NOT NULL,
    budget          TEXT NOT NULL DEFAULT '{}',
    memory_enabled  INTEGER NOT NULL DEFAULT 1,
    voice           TEXT NOT NULL DEFAULT '',
    archived        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX bot_archived ON bot(archived, created_at);
CREATE INDEX bot_team ON bot(team_id);
CREATE INDEX bot_reports_to ON bot(reports_to);

-- Bot 的電腦（容器），一個 Bot 最多一台；主機上還有電腦時不能刪主機。
CREATE TABLE box (
    bot_id         TEXT PRIMARY KEY REFERENCES bot(id) ON DELETE CASCADE,
    host_id        TEXT REFERENCES host(id) ON DELETE RESTRICT,
    container_id   TEXT NOT NULL DEFAULT '',
    image_rev      TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'none' CHECK (state IN ('none', 'creating', 'running', 'sleeping', 'updating', 'error')),
    error          TEXT NOT NULL DEFAULT '',
    limits         TEXT NOT NULL DEFAULT '{}',
    last_active_at INTEGER NOT NULL DEFAULT 0,
    novnc_port     INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX box_host_state ON box(host_id, state);

CREATE TABLE thread (
    id              TEXT PRIMARY KEY,
    bot_id          TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    title           TEXT NOT NULL DEFAULT '',
    folder_id       TEXT,
    pinned          INTEGER NOT NULL DEFAULT 0,
    archived        INTEGER NOT NULL DEFAULT 0,
    unread          INTEGER NOT NULL DEFAULT 0,
    model_override  TEXT NOT NULL DEFAULT '',
    usage           TEXT NOT NULL DEFAULT '{}',
    last_message_at INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX thread_bot ON thread(bot_id, archived, pinned DESC, last_message_at DESC);

-- 訊息屬於一個對話串或群組（scope＋scope_id），依 seq（寫入順序）排序。
CREATE TABLE message (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    id          TEXT NOT NULL UNIQUE,
    scope       TEXT NOT NULL CHECK (scope IN ('thread', 'room')),
    scope_id    TEXT NOT NULL,
    parent_id   TEXT REFERENCES message(id) ON DELETE SET NULL,
    sender      TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'text' CHECK (kind IN ('text', 'card', 'activity', 'digest', 'widget', 'voice', 'peer')),
    content     TEXT NOT NULL DEFAULT '',
    attachments TEXT NOT NULL DEFAULT '[]',
    meta        TEXT NOT NULL DEFAULT '{}',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX message_scope ON message(scope, scope_id, seq);
CREATE INDEX message_parent ON message(parent_id);

-- scope_id 指向不同的表，無法用外鍵；刪對話串時用 trigger 一起刪訊息。
CREATE TRIGGER thread_delete_messages AFTER DELETE ON thread
BEGIN
    DELETE FROM message WHERE scope = 'thread' AND scope_id = OLD.id;
END;

CREATE TABLE task (
    id           TEXT PRIMARY KEY,
    bot_id       TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    thread_id    TEXT REFERENCES thread(id) ON DELETE SET NULL,
    source       TEXT NOT NULL CHECK (source IN ('owner', 'bot', 'routine', 'webhook')),
    state        TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'waiting_input', 'waiting_approval', 'waiting_box', 'stopping', 'done')),
    verdict      TEXT NOT NULL DEFAULT '',
    low_priority INTEGER NOT NULL DEFAULT 0,
    input        TEXT NOT NULL DEFAULT '',
    meta         TEXT NOT NULL DEFAULT '{}',
    started_at   INTEGER NOT NULL DEFAULT 0,
    finished_at  INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX task_bot_state ON task(bot_id, state);
CREATE INDEX task_thread ON task(thread_id);
CREATE INDEX task_open ON task(state) WHERE state <> 'done';

-- 事件流水帳：SSE 用 id 斷線續傳（Last-Event-ID）。
CREATE TABLE event (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id     TEXT REFERENCES bot(id) ON DELETE CASCADE,
    task_id    TEXT,
    kind       TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);
CREATE INDEX event_bot ON event(bot_id, id);

-- 稽核：誰、從哪裡、哪個操作、參數（已遮蔽）、結果（MCP-13）。
CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    actor      TEXT NOT NULL,
    via        TEXT NOT NULL DEFAULT '',
    op         TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL DEFAULT '',
    params     TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX audit_created ON audit_log(created_at);
CREATE INDEX audit_actor ON audit_log(actor, id);
CREATE INDEX audit_target ON audit_log(target, id);

-- 外部代理的權杖（MCP-6）：只存雜湊，不存原文。
CREATE TABLE agent_token (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL DEFAULT '[]',
    resources    TEXT NOT NULL DEFAULT '{}',
    last_used_at INTEGER NOT NULL DEFAULT 0,
    expires_at   INTEGER NOT NULL DEFAULT 0,
    revoked_at   INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE TABLE approval (
    id           TEXT PRIMARY KEY,
    bot_id       TEXT REFERENCES bot(id) ON DELETE CASCADE,
    action       TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    requested_by TEXT NOT NULL DEFAULT '',
    request_id   TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'expired', 'canceled')),
    decided_by   TEXT NOT NULL DEFAULT '',
    decided_at   INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX approval_status ON approval(status, created_at);
CREATE INDEX approval_bot ON approval(bot_id, status);

-- 待我處理。
CREATE TABLE inbox_item (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('approval', 'question', 'secret', 'signin', 'takeover', 'proposal', 'discussion', 'routine_failed', 'budget')),
    bot_id      TEXT REFERENCES bot(id) ON DELETE CASCADE,
    ref         TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by TEXT NOT NULL DEFAULT '',
    resolved_at INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
CREATE INDEX inbox_status ON inbox_item(status, created_at);
CREATE INDEX inbox_bot ON inbox_item(bot_id, status);
CREATE INDEX inbox_ref ON inbox_item(ref);

-- 用量帳本（US-1）。刪掉 Bot 後帳仍保留，所以 bot_id 不設外鍵。
CREATE TABLE usage_entry (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id             TEXT NOT NULL DEFAULT '',
    thread_id          TEXT NOT NULL DEFAULT '',
    task_id            TEXT NOT NULL DEFAULT '',
    engine             TEXT NOT NULL DEFAULT '',
    provider           TEXT NOT NULL DEFAULT '',
    model              TEXT NOT NULL DEFAULT '',
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micros        INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL
);
CREATE INDEX usage_created ON usage_entry(created_at);
CREATE INDEX usage_bot ON usage_entry(bot_id, created_at);

-- 設定：key → JSON 值。
CREATE TABLE setting (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
