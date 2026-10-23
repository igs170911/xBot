-- 0030 (G4)：auto-stop 卡不重複（每個群組只一張）

-- G4: Unique index on (kind, ref) for open inbox items to prevent duplicate auto-stop cards
-- Only applies when ref is not empty (auto-stop cards always have a ref identifying the room)
CREATE UNIQUE INDEX inbox_unique_open_ref ON inbox_item(kind, ref) WHERE status = 'open' AND ref != '';
