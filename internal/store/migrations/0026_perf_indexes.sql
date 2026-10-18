-- 0026（v1.0 效能）：補熱門查詢缺的索引。量測與前後數字見 docs/perf.md。
--   usage_task：一件工作、一條工作鏈用掉多少 token（collab 的 token 上限每回合都查）、代理花費（MCP-12）。
--   usage_engine：快判模型（judge）今天花了多少，每次快判前都查。
--   usage_unpriced：用量統計的「沒有價格的模型」；沒有它時要把範圍內每一筆都讀出來看。
--   event_task：某件工作最新一筆某種事件（進度、核准、出錯），原本要沿著 Bot 的全部事件往回找。
--   inbox_created：通知中心（status=all）依時間新到舊，一頁滿就停，不用先排序全部卡片。
CREATE INDEX usage_task ON usage_entry(task_id) WHERE task_id <> '';
CREATE INDEX usage_engine ON usage_entry(engine, created_at) WHERE engine <> '';
CREATE INDEX usage_unpriced ON usage_entry(created_at) WHERE unpriced = 1;
CREATE INDEX event_task ON event(task_id, kind, id) WHERE task_id IS NOT NULL;
CREATE INDEX inbox_created ON inbox_item(created_at, id);
