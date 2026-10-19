-- 0027：記憶的變更記下是在哪條對話串（Bot 在對話裡用記憶工具記的）。
-- 你、代理改的，或在網頁上才發現 Bot 改過的，是空白。
ALTER TABLE memory_version ADD COLUMN thread_id TEXT NOT NULL DEFAULT '';
