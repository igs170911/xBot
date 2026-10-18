-- 0021（B31）：電腦搬家（CL-5）的紀錄。
--
-- 一列＝一次把一個 Bot 的電腦從 from_host 搬到 to_host。state：queued（排隊）、running（進行中）、done（完成）、
-- failed（失敗；原主機的資料沒動）、canceled（取消）。服務重新啟動時，沒做完的會依 box.host_id 判定成 done 或 failed。
-- 搬家成功後原主機的資料（volume）保留到 keep_until（毫秒）才由背景工作刪除，刪掉後記下 cleaned_at。
-- bot_id、from_host、to_host 不設外鍵：Bot 或主機刪掉後，留著紀錄才能清掉原主機上的舊資料。
CREATE TABLE box_move (
    id          TEXT PRIMARY KEY,
    bot_id      TEXT NOT NULL,
    from_host   TEXT NOT NULL,
    to_host     TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done', 'failed', 'canceled')),
    error       TEXT NOT NULL DEFAULT '',
    files       INTEGER NOT NULL DEFAULT 0,
    bytes       INTEGER NOT NULL DEFAULT 0,
    keep_until  INTEGER NOT NULL DEFAULT 0,
    cleaned_at  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX box_move_bot ON box_move(bot_id, created_at);
CREATE INDEX box_move_keep ON box_move(state, cleaned_at, keep_until);
