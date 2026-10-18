-- 0009（B12）：遠端主機（SSH）的連線設定、健康狀態，以及每個 Bot 放在哪台主機（CL-1）。
--
-- host.status 要多一個 failed（主機金鑰不符等要人處理的狀態），SQLite 不能改 CHECK，只好換表。
-- box.host_id 參照 host（ON DELETE RESTRICT），直接 DROP host 會被擋，所以 box 也一起換表：
-- 先建 host_new、box_new（參照 host_new），搬資料、丟掉舊表，再改名（改名時 SQLite 會把 box 的外鍵一起改成 host）。

CREATE TABLE host_new (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL CHECK (kind IN ('local', 'ssh')),
    -- address：SSH 主機是 tailnet 名稱或 IP（不含埠）；本機是 Docker 的位址（只給人看）。
    address      TEXT NOT NULL DEFAULT '',
    port         INTEGER NOT NULL DEFAULT 0,
    -- ssh_user：遠端帳號（通常 xbot）；key_ref：XBot 的 SSH 私鑰在資料目錄裡的相對路徑；
    -- known_hosts：這台主機專用的 known_hosts（資料目錄裡的相對路徑，hosts/<id>/known_hosts）。
    ssh_user     TEXT NOT NULL DEFAULT '',
    key_ref      TEXT NOT NULL DEFAULT '',
    known_hosts  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'online', 'offline', 'updating', 'failed')),
    status_error TEXT NOT NULL DEFAULT '',
    arch         TEXT NOT NULL DEFAULT '',
    max_running  INTEGER NOT NULL DEFAULT 0,
    resources    TEXT NOT NULL DEFAULT '{}',
    -- health：最近一次的連線狀態（hosts.Health 的 JSON：state、往返時間、轉發數、最後的錯誤）。
    health       TEXT NOT NULL DEFAULT '{}',
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
INSERT INTO host_new (id, name, kind, address, status, arch, max_running, resources, last_seen_at, created_at, updated_at)
    SELECT id, name, kind, address, status, arch, max_running, resources, last_seen_at, created_at, updated_at FROM host;

CREATE TABLE box_new (
    bot_id         TEXT PRIMARY KEY REFERENCES bot(id) ON DELETE CASCADE,
    host_id        TEXT REFERENCES host_new(id) ON DELETE RESTRICT,
    container_id   TEXT NOT NULL DEFAULT '',
    image_rev      TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'none' CHECK (state IN ('none', 'creating', 'running', 'sleeping', 'updating', 'error')),
    error          TEXT NOT NULL DEFAULT '',
    limits         TEXT NOT NULL DEFAULT '{}',
    last_active_at INTEGER NOT NULL DEFAULT 0,
    novnc_port     INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    hermes_port    INTEGER NOT NULL DEFAULT 0
);
INSERT INTO box_new (bot_id, host_id, container_id, image_rev, state, error, limits, last_active_at, novnc_port, created_at, updated_at, hermes_port)
    SELECT bot_id, host_id, container_id, image_rev, state, error, limits, last_active_at, novnc_port, created_at, updated_at, hermes_port FROM box;

DROP TABLE box;
DROP TABLE host;
ALTER TABLE host_new RENAME TO host;
ALTER TABLE box_new RENAME TO box;
CREATE INDEX box_host_state ON box(host_id, state);

-- Bot 放在哪台主機（CL-1）；空白＝還沒擺放（第一次開機時自動擺到有空位的主機，CL-4）。主機被移除時清成空白。
ALTER TABLE bot ADD COLUMN host_id TEXT REFERENCES host(id) ON DELETE SET NULL;
