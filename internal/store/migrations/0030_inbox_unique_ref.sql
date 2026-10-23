-- 0030 (G4)：auto-stop 卡不重複（每個群組只一張）

-- H1: First dedupe existing duplicates: keep newest open card per (kind, ref), dismiss the rest
UPDATE inbox_item SET status = 'dismissed'
WHERE status = 'open' AND ref != ''
AND id NOT IN (
  SELECT id FROM (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY kind, ref ORDER BY created_at DESC) as rn
    FROM inbox_item WHERE status = 'open' AND ref != ''
  ) WHERE rn = 1
);

-- G4/H1: Unique index on (kind, ref) for open inbox items to prevent duplicate auto-stop cards
-- Only applies when ref is not empty (auto-stop cards always have a ref identifying the room)
CREATE UNIQUE INDEX IF NOT EXISTS inbox_unique_open_ref ON inbox_item(kind, ref) WHERE status = 'open' AND ref != '';
