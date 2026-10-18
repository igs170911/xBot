-- 0008（C31）：記憶的變更紀錄（MM-3：可復原）。
-- 每次 XBot 改 Bot 的 MEMORY.md／USER.md（或發現 Bot 自己改過）記一筆：改之前、改之後的全文。
-- 每個 Bot 每份記憶只留最近 limits.MemoryVersionsKept 筆。
CREATE TABLE memory_version (
    id         TEXT PRIMARY KEY,
    bot_id     TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    target     TEXT NOT NULL CHECK (target IN ('memory', 'user')),
    before     TEXT NOT NULL,
    after      TEXT NOT NULL,
    actor      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX memory_version_bot ON memory_version(bot_id, target, created_at);
