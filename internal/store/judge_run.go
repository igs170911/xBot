package store

import "context"

// ── judge_run（X05）──────────────────────────────────

// JudgeRun 是快判的一次判斷（影子或生效），見 migrations/0016_judge_run.sql。
type JudgeRun struct {
	ID         int64   `json:"id"`
	Use        string  `json:"use"`
	Mode       string  `json:"mode"`
	Tier       string  `json:"tier"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Calls      int     `json:"calls"`
	Tier2Calls int     `json:"tier2_calls"`
	Ref        string  `json:"ref"`
	BotID      string  `json:"bot_id"`
	RuleLabel  string  `json:"rule_label"`
	ModelLabel string  `json:"model_label"`
	P          float64 `json:"p"`
	Why        string  `json:"why"`
	Outcome    string  `json:"outcome"`
	Used       string  `json:"used"`
	LatencyMS  int64   `json:"latency_ms"`
	CreatedAt  int64   `json:"created_at"`
}

const judgeRunCols = `id, use, mode, tier, provider, model, calls, tier2_calls, ref, bot_id, rule_label, model_label, p, why, outcome, used, latency_ms, created_at`

func scanJudgeRun(s scanner) (JudgeRun, error) {
	var r JudgeRun
	err := s.Scan(&r.ID, &r.Use, &r.Mode, &r.Tier, &r.Provider, &r.Model, &r.Calls, &r.Tier2Calls, &r.Ref, &r.BotID,
		&r.RuleLabel, &r.ModelLabel, &r.P, &r.Why, &r.Outcome, &r.Used, &r.LatencyMS, &r.CreatedAt)
	return r, err
}

// AddJudgeRun 記一次快判。CreatedAt 為 0 時用現在時間。
func (q *Queries) AddJudgeRun(ctx context.Context, r JudgeRun) (JudgeRun, error) {
	if r.CreatedAt == 0 {
		r.CreatedAt = nowMS()
	}
	if r.Used == "" {
		r.Used = "rules"
	}
	res, err := q.w.ExecContext(ctx, `INSERT INTO judge_run(use, mode, tier, provider, model, calls, tier2_calls, ref, bot_id,
		rule_label, model_label, p, why, outcome, used, latency_ms, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Use, r.Mode, r.Tier, r.Provider, r.Model, r.Calls, r.Tier2Calls, r.Ref, r.BotID, r.RuleLabel, r.ModelLabel, r.P, r.Why,
		r.Outcome, r.Used, r.LatencyMS, r.CreatedAt)
	if err != nil {
		return r, err
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// ListJudgeRuns 列出一個用途（空白不限）最近的快判，新到舊。
func (q *Queries) ListJudgeRuns(ctx context.Context, use string, limit int) ([]JudgeRun, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := q.r.QueryContext(ctx, `SELECT `+judgeRunCols+` FROM judge_run WHERE (? = '' OR use = ?) ORDER BY created_at DESC, id DESC LIMIT ?`,
		use, use, limit)
	return collect(rows, err, scanJudgeRun)
}

// JudgeCalls 回傳 since（毫秒）之後一共呼叫了幾次模型，以及其中第二層的次數（每日上限用）。
func (q *Queries) JudgeCalls(ctx context.Context, since int64) (calls, tier2 int, err error) {
	err = q.r.QueryRowContext(ctx, `SELECT COALESCE(SUM(calls),0), COALESCE(SUM(tier2_calls),0) FROM judge_run WHERE created_at >= ?`, since).
		Scan(&calls, &tier2)
	return calls, tier2, err
}

// JudgeUseStats 是一個用途在一段時間內的快判統計。
type JudgeUseStats struct {
	Use         string `json:"use"`
	Runs        int    `json:"runs" desc:"判斷了幾次（含沒有呼叫模型的）"`
	Calls       int    `json:"calls" desc:"呼叫了幾次模型"`
	Answered    int    `json:"answered" desc:"模型有給出合格答案的次數"`
	Agreed      int    `json:"agreed" desc:"其中和規則一樣的次數"`
	UsedByModel int    `json:"used_by_model" desc:"生效模式下採用模型判斷的次數"`
	Failed      int    `json:"failed" desc:"逾時、出錯、限流、格式不合的次數"`
	// Corrected 是你按過「不對？」改正、而且模型有答案的次數；CorrectedModel／CorrectedRule 是模型、規則和你的改正一樣的次數。
	Corrected      int     `json:"corrected"`
	CorrectedModel int     `json:"corrected_model"`
	CorrectedRule  int     `json:"corrected_rule"`
	AvgLatencyMS   float64 `json:"avg_latency_ms"`
}

// JudgeStats 依用途統計 since（毫秒）之後的快判。
func (q *Queries) JudgeStats(ctx context.Context, since int64) ([]JudgeUseStats, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT r.use, COUNT(*), COALESCE(SUM(r.calls),0),
		COALESCE(SUM(r.model_label <> ''),0), COALESCE(SUM(r.model_label <> '' AND r.model_label = r.rule_label),0),
		COALESCE(SUM(r.used = 'model'),0), COALESCE(SUM(r.outcome IN ('timeout','error','rate_limited','invalid')),0),
		COALESCE(SUM(c.corrected IS NOT NULL AND r.model_label <> ''),0),
		COALESCE(SUM(c.corrected IS NOT NULL AND r.model_label <> '' AND r.model_label = c.corrected),0),
		COALESCE(SUM(c.corrected IS NOT NULL AND r.model_label <> '' AND r.rule_label = c.corrected),0),
		COALESCE(AVG(CASE WHEN r.calls > 0 THEN r.latency_ms END), 0)
		FROM judge_run r LEFT JOIN judge_correction c ON c.use = r.use AND c.task_id = r.ref AND r.ref <> ''
		WHERE r.created_at >= ? GROUP BY r.use ORDER BY r.use`, since)
	return collect(rows, err, func(s scanner) (JudgeUseStats, error) {
		var u JudgeUseStats
		err := s.Scan(&u.Use, &u.Runs, &u.Calls, &u.Answered, &u.Agreed, &u.UsedByModel, &u.Failed, &u.Corrected, &u.CorrectedModel,
			&u.CorrectedRule, &u.AvgLatencyMS)
		return u, err
	})
}

// PruneJudgeRuns 刪掉 before（毫秒）之前的快判紀錄。
func (q *Queries) PruneJudgeRuns(ctx context.Context, before int64) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM judge_run WHERE created_at < ?`, before)
	return err
}

// SumUsageEngine 合計某個引擎標記（例：judge）在 since（毫秒）之後的用量。
func (q *Queries) SumUsageEngine(ctx context.Context, engine string, since int64) (UsageTotals, error) {
	var t UsageTotals
	err := q.r.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(cost_micros),0)
		FROM usage_entry WHERE engine = ? AND created_at >= ?`, engine, since).
		Scan(&t.Calls, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostMicros)
	return t, err
}
