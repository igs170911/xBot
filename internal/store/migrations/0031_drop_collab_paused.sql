-- 0031：「全部停下」改成緊急停止（2026-10-23），不再記住暫停狀態；清掉舊的 collab.paused。
DELETE FROM setting WHERE key = 'collab.paused';
