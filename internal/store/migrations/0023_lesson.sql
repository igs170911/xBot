-- 0023（B32）：示範教學（PRD BX-9、E04、E05）。你在 Bot 的電腦上示範一次，錄製器記下步驟，整理成技能草稿。
--   state：recording（錄製中）、paused（暫停）、stopped（錄完，草稿等你確認）、saved（已存成技能）、discarded（捨棄）。
--   notes：你在示範中補充的說明 [{at, text}]；dropped：你刪掉的步驟 id；steps：錄完時整理好的步驟（錄製中是空的，即時從錄製檔算）。
--   draft：技能草稿 {name, description, instructions, shots, by}；skill_id：存成的技能（Bot 的 /hermes/skills 底下的資料夾）。
--   thread_id：從哪條對話串開始（記錄「你開始示範」等系統訊息）；trial_task_id：存檔後試做一次的工作。
CREATE TABLE lesson (
    id            TEXT PRIMARY KEY,
    bot_id        TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    goal          TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL CHECK (state IN ('recording', 'paused', 'stopped', 'saved', 'discarded')),
    notes         TEXT NOT NULL DEFAULT '[]',
    dropped       TEXT NOT NULL DEFAULT '[]',
    steps         TEXT NOT NULL DEFAULT '[]',
    draft         TEXT NOT NULL DEFAULT '{}',
    skill_id      TEXT NOT NULL DEFAULT '',
    thread_id     TEXT NOT NULL DEFAULT '',
    trial_task_id TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    started_at    INTEGER NOT NULL,
    ended_at      INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL
);
CREATE INDEX lesson_bot ON lesson(bot_id, started_at);
CREATE INDEX lesson_state ON lesson(state);
