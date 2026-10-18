-- 0016（X05）：快判模型的每一次判斷（PRD §20 FJ-5、FJ-6）。
-- 影子模式：模型的判斷和規則的判斷並排記下（不生效），用來算一致率；生效模式也記，用來算每日呼叫次數與上限。
--   use：用途（task_done、responder、precheck_clarity…）。mode：shadow（只記錄）／live（生效）。
--   tier：實際回答的是哪一層（tier1 選擇題 Jev、tier2 短文字、local 本機小模型；沒有回答時空白）。
--   calls：這次一共呼叫了幾次模型（往下一層退時會多於 1）；tier2_calls 是其中第二層的次數（第二層另有每日上限）。
--   rule_label／model_label：規則與模型各自的判斷；p：模型給的機率；why：模型的一句話原因。
--   outcome：ok、low_confidence（機率低於門檻）、invalid（格式不合）、timeout、error、rate_limited、capped（到每日上限）、
--   not_configured（沒有設定可用的模型）、locked（規則判成要人，不降級）。
--   used：最後採用誰的判斷（rules／model）。ref：判斷的對象（工作 id、群組訊息、對話串…），用來對人工改正。
CREATE TABLE judge_run (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    use         TEXT NOT NULL,
    mode        TEXT NOT NULL CHECK (mode IN ('shadow', 'live')),
    tier        TEXT NOT NULL DEFAULT '',
    provider    TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    calls       INTEGER NOT NULL DEFAULT 0,
    tier2_calls INTEGER NOT NULL DEFAULT 0,
    ref         TEXT NOT NULL DEFAULT '',
    bot_id      TEXT NOT NULL DEFAULT '',
    rule_label  TEXT NOT NULL DEFAULT '',
    model_label TEXT NOT NULL DEFAULT '',
    p           REAL NOT NULL DEFAULT 0,
    why         TEXT NOT NULL DEFAULT '',
    outcome     TEXT NOT NULL,
    used        TEXT NOT NULL DEFAULT 'rules',
    latency_ms  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);
CREATE INDEX judge_run_use ON judge_run(use, created_at);
CREATE INDEX judge_run_time ON judge_run(created_at);
CREATE INDEX judge_run_ref ON judge_run(ref);
