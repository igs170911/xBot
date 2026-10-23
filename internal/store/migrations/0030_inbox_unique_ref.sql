-- 0030 (G4)：auto-stop 卡不重複（每個群組只一張）

-- G4: Unique index on (kind, ref) for open inbox items to prevent duplicate auto-stop cards
CREATE UNIQUE INDEX inbox_unique_open_ref ON inbox_item(kind, ref) WHERE status = 'open';
