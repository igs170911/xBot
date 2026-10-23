-- 0030 (G4)：auto-stop 卡不重複（每個群組只一張）
-- 只管 auto-stop 卡（ref 以 auto_stop: 開頭）。別的卡的 ref 不保證唯一：
-- 例如訂閱引擎的核准卡 ref 是引擎的 request id，每個 Bot 都從 0 開始數。

-- H1: First dedupe existing duplicates: keep newest open card per (kind, ref), dismiss the rest
UPDATE inbox_item SET status = 'dismissed'
WHERE status = 'open' AND substr(ref, 1, 10) = 'auto_stop:'
AND id NOT IN (
  SELECT id FROM (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY kind, ref ORDER BY created_at DESC) as rn
    FROM inbox_item WHERE status = 'open' AND substr(ref, 1, 10) = 'auto_stop:'
  ) WHERE rn = 1
);

-- G4/H1: Unique index on (kind, ref) for open auto-stop cards
CREATE UNIQUE INDEX IF NOT EXISTS inbox_unique_open_ref ON inbox_item(kind, ref) WHERE status = 'open' AND substr(ref, 1, 10) = 'auto_stop:';
