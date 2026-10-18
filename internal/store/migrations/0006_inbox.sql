-- 0006（C12）：待我處理與核准。

-- 事件記下對話串（W02 待確認）：重新整理後用 chat.steps 依對話串讀回工作步驟，SSE 補送時也帶 thread_id。
ALTER TABLE event ADD COLUMN thread_id TEXT;
UPDATE event SET thread_id = (SELECT thread_id FROM task WHERE task.id = event.task_id) WHERE task_id IS NOT NULL;
CREATE INDEX event_thread ON event(thread_id, id) WHERE thread_id IS NOT NULL;

-- 允許了但執行失敗時的錯誤（MCP-5 格式的 JSON）；成功或再試成功後清空。approvals.wait 用它回報失敗（C05 待確認 2）。
ALTER TABLE approval ADD COLUMN error TEXT NOT NULL DEFAULT '';

-- 待我處理重建：SQLite 不能改 CHECK，只好換一張表。
--   kind 多了通知類：done（完成）、error（出錯）、mention（提到你）—— B04／L14 的篩選。
--   status 多了 expired（無人在場的提問 10 分鐘沒回，PRD §18）。
--   task_id／thread_id：Bot 在對話裡問的問題屬於哪件工作、哪條對話串。
--   resolution：怎麼處理的（once／always／deny／allow／answered／provided／declined／expired／gone／canceled／asked_again）。
--   read_at：已讀時間（0＝未讀）；expires_at：過期時間（0＝不會過期）。
CREATE TABLE inbox_item_new (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('approval', 'question', 'secret', 'signin', 'takeover', 'proposal', 'discussion', 'routine_failed', 'budget', 'done', 'error', 'mention')),
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
INSERT INTO inbox_item_new (id, kind, bot_id, ref, title, body, payload, status, resolved_by, resolved_at, created_at, updated_at)
    SELECT id, kind, bot_id, ref, title, body, payload, status, resolved_by, resolved_at, created_at, updated_at FROM inbox_item;
DROP TABLE inbox_item;
ALTER TABLE inbox_item_new RENAME TO inbox_item;
CREATE INDEX inbox_status ON inbox_item(status, created_at);
CREATE INDEX inbox_bot ON inbox_item(bot_id, status);
CREATE INDEX inbox_ref ON inbox_item(ref);
CREATE INDEX inbox_task ON inbox_item(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX inbox_unread ON inbox_item(read_at) WHERE read_at = 0;
