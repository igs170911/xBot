package store

import "context"

// ImportRecord 是一樣從舊系統（LazyBoy、OpenMausBot）帶進來的東西（V10，import_map）。
type ImportRecord struct {
	Source    string `json:"source"`
	Kind      string `json:"kind"`
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Detail    string `json:"detail"`
	CreatedAt int64  `json:"created_at"`
}

const importCols = `source, kind, source_id, target_id, detail, created_at`

func scanImport(s scanner) (ImportRecord, error) {
	var r ImportRecord
	err := s.Scan(&r.Source, &r.Kind, &r.SourceID, &r.TargetID, &r.Detail, &r.CreatedAt)
	return r, err
}

// PutImport 記下一樣帶進來的東西（同一個 source、kind、source_id 已經有時覆寫）。
func (q *Queries) PutImport(ctx context.Context, r ImportRecord) error {
	r.CreatedAt = nowMS()
	_, err := q.w.ExecContext(ctx, `INSERT INTO import_map(`+importCols+`) VALUES (?,?,?,?,?,?)
		ON CONFLICT(source, kind, source_id) DO UPDATE SET target_id=excluded.target_id, detail=excluded.detail, created_at=excluded.created_at`,
		r.Source, r.Kind, r.SourceID, r.TargetID, r.Detail, r.CreatedAt)
	return err
}

// ListImports 列出從某一種舊系統帶進來的東西。
func (q *Queries) ListImports(ctx context.Context, source string) ([]ImportRecord, error) {
	rows, err := q.r.QueryContext(ctx, `SELECT `+importCols+` FROM import_map WHERE source = ? ORDER BY created_at, kind, source_id`, source)
	return collect(rows, err, scanImport)
}

// DeleteImport 刪掉一筆（目標已經不在時，讓它可以再匯入）。
func (q *Queries) DeleteImport(ctx context.Context, source, kind, sourceID string) error {
	_, err := q.w.ExecContext(ctx, `DELETE FROM import_map WHERE source = ? AND kind = ? AND source_id = ?`, source, kind, sourceID)
	return err
}
