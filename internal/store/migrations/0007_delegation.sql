-- 0007（C21）：代為核准、代理的資源範圍與上限、MCP 修改標註。

-- 誰建立／最後修改這個 Bot（owner、agent:<權杖 id>、bot:<id>）。
-- MCP-8「預設只能碰自己建立的東西」靠 created_by；MCP-13「由 ○○（MCP）修改」靠 updated_by。
ALTER TABLE bot ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE bot ADD COLUMN updated_by TEXT NOT NULL DEFAULT '';
CREATE INDEX bot_created_by ON bot(created_by) WHERE created_by <> '';

-- 代理的上限（MCP-12）：0 表示用預設值（每分鐘呼叫）或不另外限制（每日呼叫、每日花費）。
ALTER TABLE agent_token ADD COLUMN rate_per_min INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_token ADD COLUMN daily_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_token ADD COLUMN daily_spend_micros INTEGER NOT NULL DEFAULT 0;

-- 代為核准授權（MCP-10）：一個代理一筆；沒有資料列＝沒有授權（預設關閉）。
--   categories：可以代為核准的類別（external_send、spend_small、publish、sign）。
--   daily_count：每天最多幾次；daily_amount_micros：每天最多核准多少錢（美元百萬分之一，只算帶金額的動作）。
--   expires_at：授權到期時間（Unix 毫秒）。
CREATE TABLE delegation (
    agent_id            TEXT PRIMARY KEY REFERENCES agent_token(id) ON DELETE CASCADE,
    categories          TEXT NOT NULL DEFAULT '[]',
    daily_count         INTEGER NOT NULL DEFAULT 0,
    daily_amount_micros INTEGER NOT NULL DEFAULT 0,
    expires_at          INTEGER NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

-- 每一次代為核准（算每日次數與金額；一個核准只會被代為核准一次）。
CREATE TABLE delegation_use (
    approval_id   TEXT PRIMARY KEY,
    agent_id      TEXT NOT NULL,
    op            TEXT NOT NULL,
    category      TEXT NOT NULL,
    amount_micros INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);
CREATE INDEX delegation_use_agent ON delegation_use(agent_id, created_at);

-- 待我處理多一種卡：delegated（已由 ○○ 代為核准的紀錄卡）。SQLite 不能改 CHECK，只好換一張表。
CREATE TABLE inbox_item_new (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('approval', 'question', 'secret', 'signin', 'takeover', 'proposal', 'discussion', 'routine_failed', 'budget', 'done', 'error', 'mention', 'delegated')),
    bot_id      TEXT REFERENCES bot(id) ON DELETE CASCADE,
    task_id     TEXT,
    thread_id   TEXT,
    ref         TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed', 'expired')),
    resolution  TEXT NOT NULL DEFAULT '',
    resolved_by TEXT NOT NULL DEFAULT '',
    resolved_at INTEGER NOT NULL DEFAULT 0,
    read_at     INTEGER NOT NULL DEFAULT 0,
    expires_at  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
INSERT INTO inbox_item_new SELECT id, kind, bot_id, task_id, thread_id, ref, title, body, payload, status, resolution, resolved_by,
    resolved_at, read_at, expires_at, created_at, updated_at FROM inbox_item;
DROP TABLE inbox_item;
ALTER TABLE inbox_item_new RENAME TO inbox_item;
CREATE INDEX inbox_status ON inbox_item(status, created_at);
CREATE INDEX inbox_bot ON inbox_item(bot_id, status);
CREATE INDEX inbox_ref ON inbox_item(ref);
CREATE INDEX inbox_task ON inbox_item(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX inbox_unread ON inbox_item(read_at) WHERE read_at = 0;
