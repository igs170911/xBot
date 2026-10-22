-- 2026-10-22d：為 room 表加入 consec_empty 欄位，用於追蹤連續無進展次數（auto-stop）
ALTER TABLE room ADD COLUMN consec_empty INTEGER NOT NULL DEFAULT 0;
