package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 0007 再重建一次 inbox_item（多了 delegated）：舊卡的所有欄位都要留著；舊 Bot 的 created_by 是空白；
// 舊權杖的資源範圍不變（{} 仍是不限制）。
func TestMigration0007KeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	check(t, err)
	defer raw.Close()
	ms := must[[]Migration](t)(Migrations())
	var before []Migration
	for _, m := range ms {
		if m.Version < 7 {
			before = append(before, m)
		}
	}
	check(t, applyMigrations(ctx, raw, before))
	for _, q := range []string{
		`INSERT INTO bot(id, name, idle_minutes, created_at, updated_at) VALUES ('b', 'x', 30, 1, 1)`,
		`INSERT INTO agent_token(id, name, token_hash, created_at, updated_at) VALUES ('t', 'Claude Code', 'h', 1, 1)`,
		`INSERT INTO inbox_item(id, kind, bot_id, task_id, thread_id, ref, title, body, payload, status, resolution, resolved_by,
			resolved_at, read_at, expires_at, created_at, updated_at)
			VALUES ('i', 'question', 'b', 'tk', 'th', 'r', '舊卡', '內文', '{"a":1}', 'resolved', 'answered', 'owner', 5, 6, 7, 1, 2)`,
	} {
		_, err := raw.Exec(q)
		check(t, err)
	}
	check(t, applyMigrations(ctx, raw, ms))
	var task, thread, res, payload, createdBy, resources string
	var readAt, expires int64
	check(t, raw.QueryRow(`SELECT task_id, thread_id, resolution, payload, read_at, expires_at FROM inbox_item WHERE id = 'i'`).
		Scan(&task, &thread, &res, &payload, &readAt, &expires))
	if task != "tk" || thread != "th" || res != "answered" || payload != `{"a":1}` || readAt != 6 || expires != 7 {
		t.Fatalf("migration 後：%q %q %q %q %d %d", task, thread, res, payload, readAt, expires)
	}
	check(t, raw.QueryRow(`SELECT created_by FROM bot WHERE id = 'b'`).Scan(&createdBy))
	check(t, raw.QueryRow(`SELECT resources FROM agent_token WHERE id = 't'`).Scan(&resources))
	if createdBy != "" || resources != "{}" {
		t.Fatalf("舊資料：%q %q", createdBy, resources)
	}
	_, err = raw.Exec(`INSERT INTO inbox_item(id, kind, status, created_at, updated_at) VALUES ('d', 'delegated', 'resolved', 1, 1)`)
	check(t, err)
	// 刪權杖時授權一起刪。
	_, err = raw.Exec(`INSERT INTO delegation(agent_id, expires_at, created_at, updated_at) VALUES ('t', 9, 1, 1)`)
	check(t, err)
	_, err = raw.Exec(`DELETE FROM agent_token WHERE id = 't'`)
	check(t, err)
	var n int
	check(t, raw.QueryRow(`SELECT COUNT(*) FROM delegation`).Scan(&n))
	if n != 0 {
		t.Fatal("刪權杖後授權要一起刪")
	}
}
