-- 0028（2026-10-20）：待我處理多一種卡：lesson（老師模型看了沒做完的工作，提議一條學到的事或技能注意事項，internal/lessons）。
-- SQLite 不能改 CHECK，只好換一張表（同 0022）。索引照 0022 與 0026。

CREATE TABLE inbox_item_new (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('approval', 'question', 'secret', 'signin', 'takeover', 'proposal', 'discussion', 'routine_failed', 'budget', 'done', 'error', 'mention', 'delegated', 'engine_quota', 'lesson')),
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
CREATE INDEX inbox_created ON inbox_item(created_at, id);
