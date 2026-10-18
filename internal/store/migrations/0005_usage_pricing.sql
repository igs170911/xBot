-- 0005（E11）：用量的價格來源。unpriced=1 表示這個模型不在價目表、供應商也沒回報花費，花費記成 0（US-1）。
ALTER TABLE usage_entry ADD COLUMN unpriced INTEGER NOT NULL DEFAULT 0;
-- 實際使用的端點（chat/completions、responses、messages、embeddings）與是否改用備援模型。
ALTER TABLE usage_entry ADD COLUMN endpoint TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_entry ADD COLUMN fallback INTEGER NOT NULL DEFAULT 0;
CREATE INDEX usage_provider ON usage_entry(provider, created_at);
