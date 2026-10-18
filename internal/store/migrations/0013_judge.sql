-- 0013（X04）：任務完成判定的改正紀錄（PRD §20 FJ-5、FJ-7）。
-- 系統判斷（規則或快判模型）旁邊的「不對？」按下去改正時記一筆，X05 拿來比對、校正規則與門檻。
-- 同一件工作、同一個用途只留最新的一筆（你改了又改，以最後一次為準）。
--   use：判斷的用途（task_done＝§20.2 用途 2）。judged／judged_by：系統當時的判斷與誰判的（rules／model）。
--   corrected：你說應該是哪一種。reason：系統當時的原因（一行）。
CREATE TABLE judge_correction (
    id         TEXT PRIMARY KEY,
    use        TEXT NOT NULL,
    task_id    TEXT NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    bot_id     TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    judged     TEXT NOT NULL,
    judged_by  TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    corrected  TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    actor      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (task_id, use)
);
CREATE INDEX judge_correction_use ON judge_correction(use, created_at);
