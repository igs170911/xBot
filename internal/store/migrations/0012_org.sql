-- 0012（X03）：團隊、匯報線、提案與討論（PRD §6.5 TM-*、§10 team、§18；LazyBoy server/org.py、agenda.py）。

-- 團隊（TM-1）：側欄的一個分區，一位主管（chief_id）帶成員。成員是 bot.team_id 指向它的 Bot。
--   memory：團隊記憶（TM-7），JSON 字串陣列；room_id：建立團隊時一起開的群組（可以沒有）。
--   template：從哪個範本建立（engineering／company／marketing；自訂是空白）。
CREATE TABLE team (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    about      TEXT NOT NULL DEFAULT '',
    chief_id   TEXT REFERENCES bot(id) ON DELETE SET NULL,
    memory     TEXT NOT NULL DEFAULT '[]',
    room_id    TEXT REFERENCES room(id) ON DELETE SET NULL,
    template   TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- bot.team_id 在 0001 已經有（沒有外鍵）。SQLite 不能替既有欄位加外鍵，bot 又被很多表參照、不好重建，
-- 所以用 trigger 做到同樣的事：刪團隊時成員回到「未分區」。
CREATE TRIGGER team_delete_members AFTER DELETE ON team
BEGIN
    UPDATE bot SET team_id = NULL WHERE team_id = OLD.id;
END;

-- 刪除 Bot 時，向它報告的 Bot 改向它的主管報告（LazyBoy org.forget）。BEFORE：在外鍵 SET NULL 之前搬。
CREATE TRIGGER bot_delete_reports BEFORE DELETE ON bot
BEGIN
    UPDATE bot SET reports_to = OLD.reports_to WHERE reports_to = OLD.id;
END;

-- 團隊管理權限（LazyBoy can_manage）：可以替你建團隊、調整別的 Bot 的職稱與報告對象；每天可建 20 個 Bot。
ALTER TABLE bot ADD COLUMN can_manage INTEGER NOT NULL DEFAULT 0;

-- 提案與討論（TM-5、H04）：Bot 送來等你的事。
--   kind：propose（N 天後沒意見就生效）／discuss（約一個時間討論）。
--   state：open（等你）→ accepted（你同意）／vetoed（否決）／auto（到期視同同意）；
--          discuss：open → held（時間到、Bot 已被叫醒說明）→ answered（你回覆了）／declined（不用討論）；
--          cancelled（Bot 被刪除等）。open、held 還算「等你的事」。
--   due_at：提案生效或討論的時間；grace：服務關著錯過時間時已延過一次。
--   thread_id、task_id：送出時 Bot 在哪條對話、哪件工作（你的決定送回這條對話）；from_bot／from_name：那件工作是誰交辦的。
CREATE TABLE agenda (
    id         TEXT PRIMARY KEY,
    bot_id     TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('propose', 'discuss')),
    subject    TEXT NOT NULL,
    details    TEXT NOT NULL DEFAULT '',
    due_at     INTEGER NOT NULL,
    state      TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'held', 'accepted', 'vetoed', 'auto', 'answered', 'declined', 'cancelled')),
    grace      INTEGER NOT NULL DEFAULT 0,
    note       TEXT NOT NULL DEFAULT '',
    thread_id  TEXT,
    task_id    TEXT,
    from_bot   TEXT NOT NULL DEFAULT '',
    from_name  TEXT NOT NULL DEFAULT '',
    decided_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX agenda_state ON agenda(state, due_at);
CREATE INDEX agenda_bot ON agenda(bot_id, state);
