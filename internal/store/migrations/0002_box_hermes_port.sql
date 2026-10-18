-- B02：Bot 電腦除了 noVNC（6080）也發布 Hermes 主機埠（8642），兩個都記下來。
ALTER TABLE box ADD COLUMN hermes_port INTEGER NOT NULL DEFAULT 0;
