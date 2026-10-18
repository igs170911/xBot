-- 0003：操作登錄表（C02）。

-- request_id 去重（MCP-4）：同一個身分用同一個 request_id 重送，回第一次的結果、不重做。
-- status：running（處理中，佔住這個 request_id）／ok（完成，result 是輸出）／pending_approval（等核准）。
-- 失敗的呼叫不留紀錄，讓呼叫端可以用同一個 request_id 重試（同 LazyBoy store.py admissions 只記成功收下的）。
CREATE TABLE op_request (
    actor       TEXT NOT NULL,
    request_id  TEXT NOT NULL,
    op          TEXT NOT NULL,
    params_hash TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('running', 'ok', 'pending_approval')),
    result      TEXT NOT NULL DEFAULT '{}',
    approval_id TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (actor, request_id)
);
CREATE INDEX op_request_created ON op_request(created_at);
CREATE INDEX op_request_approval ON op_request(approval_id) WHERE approval_id <> '';

-- 核准後要執行的原始輸入（可能含密鑰，不對外回傳）；payload 只放遮蔽後的內容給介面顯示。
ALTER TABLE approval ADD COLUMN input TEXT NOT NULL DEFAULT '{}';
