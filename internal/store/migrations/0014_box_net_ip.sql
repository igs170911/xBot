-- B13：Bot 電腦在 xbot-box 網路上的固定 IP（SEC-4 網路防護依來源 IP 套用每個 Bot 的私有網路白名單）。
-- 重建電腦時沿用；空白＝還沒分配（或用預設 bridge 網路）。
ALTER TABLE box ADD COLUMN net_ip TEXT NOT NULL DEFAULT '';
