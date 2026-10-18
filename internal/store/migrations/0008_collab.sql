-- 0008（X02）：Bot 傳話與群組（PRD §10 room、bot_message、assignment；§18；LazyBoy server/collab.py）。

-- 任務來源多了 room（群組座位的一輪）。SQLite 不能改 CHECK，只好換一張表（沒有別的表用外鍵指向 task）。
CREATE TABLE task_new (
    id           TEXT PRIMARY KEY,
    bot_id       TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    thread_id    TEXT REFERENCES thread(id) ON DELETE SET NULL,
    source       TEXT NOT NULL CHECK (source IN ('owner', 'bot', 'room', 'routine', 'webhook')),
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
INSERT INTO task_new SELECT id, bot_id, thread_id, source, state, verdict, low_priority, input, meta, started_at, finished_at, created_at, updated_at FROM task;
DROP TABLE task;
ALTER TABLE task_new RENAME TO task;
CREATE INDEX task_bot_state ON task(bot_id, state);
CREATE INDEX task_thread ON task(thread_id);
CREATE INDEX task_open ON task(state) WHERE state <> 'done';

-- 對話串：群組座位（每位成員在每個群組一條隱藏的對話串，RM-4）與隊友傳話（收件 Bot 這邊，每位寄件者一條）。
ALTER TABLE thread ADD COLUMN room_id TEXT;
ALTER TABLE thread ADD COLUMN peer_bot_id TEXT;
CREATE INDEX thread_room ON thread(room_id, bot_id) WHERE room_id IS NOT NULL;
CREATE INDEX thread_peer ON thread(bot_id, peer_bot_id) WHERE peer_bot_id IS NOT NULL;

-- 群組（RM-*）：你＋2–10 個 Bot。訊息存在 message（scope='room'）。
--   responder_rule：沒點名時誰回（auto 自動挑一位／lead 主責／mentioned 只回被點名的／everyone 所有成員，D01）。
--   auto_name：名稱跟著成員自動產生（清空名稱時回到自動）。
CREATE TABLE room (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    auto_name       INTEGER NOT NULL DEFAULT 1,
    responder_rule  TEXT NOT NULL DEFAULT 'auto' CHECK (responder_rule IN ('auto', 'lead', 'mentioned', 'everyone')),
    lead_id         TEXT REFERENCES bot(id) ON DELETE SET NULL,
    bulletin        TEXT NOT NULL DEFAULT '',
    cwd             TEXT NOT NULL DEFAULT '',
    created_by      TEXT NOT NULL DEFAULT '',
    last_message_at INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX room_recent ON room(last_message_at DESC, created_at DESC);

-- 群組成員：座位對話串、讀到哪一則（seen_seq＝message.seq）、暫停（RM-10）。
CREATE TABLE room_member (
    room_id        TEXT NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    bot_id         TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    position       INTEGER NOT NULL DEFAULT 0,
    seat_thread_id TEXT REFERENCES thread(id) ON DELETE SET NULL,
    seen_seq       INTEGER NOT NULL DEFAULT 0,
    paused         INTEGER NOT NULL DEFAULT 0,
    joined_at      INTEGER NOT NULL,
    PRIMARY KEY (room_id, bot_id)
);
CREATE INDEX room_member_bot ON room_member(bot_id);

-- 刪群組時一起刪它的訊息與成員的座位對話串。
CREATE TRIGGER room_delete_rest AFTER DELETE ON room
BEGIN
    DELETE FROM message WHERE scope = 'room' AND scope_id = OLD.id;
    DELETE FROM thread WHERE room_id = OLD.id;
END;

-- Bot 之間的一則傳話（AA-*）。Bot 刪除後紀錄仍保留（雙方名字存在這裡），所以不設外鍵。
--   intent：request（要回覆）、question（要回覆）、result（回覆，不算往來次數上限）、fyi（不回覆）。
--   origin_task_id：寄件者發出時正在做的工作；task_id：這則傳話在收件者那邊變成的工作。
--   answered：要回覆的傳話已經有結果（或不會有了）。
CREATE TABLE bot_message (
    id             TEXT PRIMARY KEY,
    from_bot       TEXT NOT NULL,
    to_bot         TEXT NOT NULL,
    from_name      TEXT NOT NULL DEFAULT '',
    to_name        TEXT NOT NULL DEFAULT '',
    intent         TEXT NOT NULL CHECK (intent IN ('request', 'question', 'result', 'fyi')),
    text           TEXT NOT NULL DEFAULT '',
    hop            INTEGER NOT NULL DEFAULT 1,
    chain_id       TEXT NOT NULL,
    assignment_id  TEXT,
    origin_task_id TEXT NOT NULL DEFAULT '',
    task_id        TEXT NOT NULL DEFAULT '',
    request_id     TEXT NOT NULL DEFAULT '',
    files          TEXT NOT NULL DEFAULT '[]',
    answered       INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL
);
CREATE INDEX bot_message_pair ON bot_message(from_bot, to_bot, created_at);
CREATE INDEX bot_message_origin ON bot_message(origin_task_id, answered);
CREATE INDEX bot_message_to ON bot_message(to_bot, answered);
CREATE INDEX bot_message_chain ON bot_message(chain_id, created_at);
CREATE UNIQUE INDEX bot_message_request ON bot_message(to_bot, request_id) WHERE request_id <> '';

-- 交辦樹（AA-10、AA-11、LP-8）：每則要處理的傳話一筆。root_id 是這條工作鏈（chain），parent_id 是寄件者當時在處理的交辦。
--   mode：work（交辦，intent=request）／consult（諮詢，question）／notify（通知，fyi）。
--   status：queued → running → deferred（轉派出去、等隊友結果）→ done／failed／cancelled。
CREATE TABLE assignment (
    id             TEXT PRIMARY KEY,
    root_id        TEXT NOT NULL,
    parent_id      TEXT,
    requester      TEXT NOT NULL,
    assignee       TEXT NOT NULL,
    mode           TEXT NOT NULL CHECK (mode IN ('work', 'consult', 'notify')),
    status         TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'deferred', 'done', 'failed', 'cancelled')),
    message_id     TEXT NOT NULL,
    task_id        TEXT NOT NULL DEFAULT '',
    deferred_reply TEXT NOT NULL DEFAULT '{}',
    budget_tokens  INTEGER NOT NULL DEFAULT 0,
    used_tokens    INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX assignment_root ON assignment(root_id, created_at);
CREATE INDEX assignment_task ON assignment(task_id);
CREATE INDEX assignment_assignee ON assignment(assignee, status);
