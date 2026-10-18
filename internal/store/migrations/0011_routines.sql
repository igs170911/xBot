-- 0011（C32）：定時任務、執行紀錄與網址觸發（PRD 6.8 RT-*；LazyBoy server/scheduling.py）。

-- 定時任務。schedule 是 JSON：{kind, time, date, weekdays, day_of_month, every_minutes, active_from, active_to,
-- end_date, cron, timezone}；kind：once、daily、weekdays、weekly、monthly、interval、cron、trigger（只靠網址觸發）。
--   status：active（啟用中）、paused（已暫停）、done（單次跑完、目標完成或用完輪數）。
--   next_run_at：下一次要跑的時間（Unix 毫秒，已含隨機延後）；也是這一次的去重鍵：啟動失敗退避時不變。
--   failures、retry_at：啟動失敗的次數與下次重試時間（退避 1／5／15／60 分鐘）。
--   goal：目標型（RT-4）{done_when, max_rounds, rounds, progress_file, completed_at, summary}；'{}' 表示不是目標型。
--   hook_id、hook_secret：網址觸發 POST /hooks/<hook_id>（Authorization: Bearer <secret> 或 HMAC 簽章）。
CREATE TABLE routine (
    id                TEXT PRIMARY KEY,
    bot_id            TEXT NOT NULL REFERENCES bot(id) ON DELETE CASCADE,
    title             TEXT NOT NULL,
    prompt            TEXT NOT NULL,
    schedule          TEXT NOT NULL DEFAULT '{}',
    jitter_minutes    INTEGER NOT NULL DEFAULT 0,
    notify_on_failure INTEGER NOT NULL DEFAULT 1,
    goal              TEXT NOT NULL DEFAULT '{}',
    thread_id         TEXT,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'done')),
    next_run_at       INTEGER NOT NULL DEFAULT 0,
    last_run_at       INTEGER NOT NULL DEFAULT 0,
    last_task_id      TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    failures          INTEGER NOT NULL DEFAULT 0,
    retry_at          INTEGER NOT NULL DEFAULT 0,
    hook_id           TEXT UNIQUE,
    hook_secret       TEXT NOT NULL DEFAULT '',
    last_hook_at      INTEGER NOT NULL DEFAULT 0,
    created_by        TEXT NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);
CREATE INDEX routine_bot ON routine(bot_id);
CREATE INDEX routine_due ON routine(next_run_at) WHERE status = 'active';

-- 每一次執行（RT-6）。occurrence 是這一次的唯一鍵（sched:<排定時間>、manual:<id>、hook:<id>）：
-- 服務在「已經交辦」和「排下一次」之間重開，也不會同一次跑兩遍。
--   trigger：schedule（排程）、catchup（補跑）、manual（手動）、webhook（網址觸發）、test（測試請求，不開始工作）。
--   status：starting、running、done、failed、cancelled、skipped（上一次還在跑或太密）、retrying（啟動失敗，等重試）、test。
--   task_id：這次交給 Bot 的工作（result_ref）。
CREATE TABLE routine_run (
    id            TEXT PRIMARY KEY,
    routine_id    TEXT NOT NULL REFERENCES routine(id) ON DELETE CASCADE,
    bot_id        TEXT NOT NULL,
    occurrence    TEXT NOT NULL,
    trigger       TEXT NOT NULL CHECK (trigger IN ('schedule', 'catchup', 'manual', 'webhook', 'test')),
    status        TEXT NOT NULL CHECK (status IN ('starting', 'running', 'done', 'failed', 'cancelled', 'skipped', 'retrying', 'test')),
    task_id       TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    payload_bytes INTEGER NOT NULL DEFAULT 0,
    scheduled_at  INTEGER NOT NULL DEFAULT 0,
    started_at    INTEGER NOT NULL DEFAULT 0,
    finished_at   INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE (routine_id, occurrence)
);
CREATE INDEX routine_run_routine ON routine_run(routine_id, created_at);
CREATE INDEX routine_run_scheduled ON routine_run(scheduled_at);
CREATE INDEX routine_run_open ON routine_run(status) WHERE status IN ('starting', 'running');
