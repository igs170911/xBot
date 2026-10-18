package store

import (
	"context"
	"encoding/json"
)

// UpdateAgentTokenScopes 改代理權杖的權限範圍與資源範圍（xbot.agents.update_scopes）。
// resources 是 nil 時保留原本的資源範圍。
func (q *Queries) UpdateAgentTokenScopes(ctx context.Context, id string, scopes []string, resources json.RawMessage) error {
	if scopes == nil {
		scopes = []string{}
	}
	if resources == nil {
		return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET scopes=?, updated_at=? WHERE id=?`,
			strList(scopes), nowMS(), id))
	}
	return affected(q.w.ExecContext(ctx, `UPDATE agent_token SET scopes=?, resources=?, updated_at=? WHERE id=?`,
		strList(scopes), jsonOr(resources, "{}"), nowMS(), id))
}
