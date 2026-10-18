-- 0004（E02）：Model Proxy 的 worker-key 亂數，以及對話串對應的引擎原生 session。
-- （0003 保留給 C02。）

-- 每個 Bot 目前的 worker-key 亂數；換掉就讓舊 key 失效，刪 Bot 時一起刪（等於收回）。
CREATE TABLE worker_nonce (
    bot_id     TEXT PRIMARY KEY REFERENCES bot(id) ON DELETE CASCADE,
    nonce      TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

-- 對話串在引擎裡的 session（Hermes 的 stored_session_id），服務重開後用它接回同一段對話。
ALTER TABLE thread ADD COLUMN engine_session TEXT NOT NULL DEFAULT '';
