-- 0025（V10）：從 LazyBoy、OpenMausBot 匯入（PRD ON-5、BT-8）的對照表。
--
-- 一列＝舊系統裡的一樣東西已經帶進 XBot：source 是哪一種舊系統（lazyboy、openmausbot），kind 是哪一類
-- （bot、skills、memory、data、login、routine、team、room、key、approvals），source_id 是它在舊系統的 id，
-- target_id 是在 XBot 建出來的 id（技能、記憶這類跟著 Bot 的，是那個 Bot 的 id）。
-- 再匯入一次時看這張表，已經帶過的就不再建立（目標被刪掉的除外）。不設外鍵：目標刪掉後由匯入那邊判斷。
CREATE TABLE import_map (
    source     TEXT NOT NULL,
    kind       TEXT NOT NULL,
    source_id  TEXT NOT NULL,
    target_id  TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY (source, kind, source_id)
);
