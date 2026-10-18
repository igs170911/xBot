package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 0006 重建 inbox_item：舊的卡與事件都要留著，事件補上對話串。
func TestMigration0006KeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	check(t, err)
	defer raw.Close()
	ms := must[[]Migration](t)(Migrations())
	var before []Migration
	for _, m := range ms {
		if m.Version < 6 {
			before = append(before, m)
		}
	}
	check(t, applyMigrations(ctx, raw, before))
	for _, q := range []string{
		`INSERT INTO bot(id, name, idle_minutes, created_at, updated_at) VALUES ('b', 'x', 30, 1, 1)`,
		`INSERT INTO thread(id, bot_id, created_at, updated_at) VALUES ('th', 'b', 1, 1)`,
		`INSERT INTO task(id, bot_id, thread_id, source, created_at, updated_at) VALUES ('tk', 'b', 'th', 'owner', 1, 1)`,
		`INSERT INTO event(bot_id, task_id, kind, created_at) VALUES ('b', 'tk', 'chat.tool_call', 1)`,
		`INSERT INTO inbox_item(id, kind, bot_id, ref, title, status, created_at, updated_at) VALUES ('i', 'approval', 'b', 'a', '舊卡', 'resolved', 1, 1)`,
	} {
		_, err := raw.Exec(q)
		check(t, err)
	}
	check(t, applyMigrations(ctx, raw, ms))
	var thread, title, status string
	check(t, raw.QueryRow(`SELECT thread_id FROM event`).Scan(&thread))
	check(t, raw.QueryRow(`SELECT title, status FROM inbox_item WHERE id = 'i'`).Scan(&title, &status))
	if thread != "th" || title != "舊卡" || status != "resolved" {
		t.Fatalf("migration 後：%q %q %q", thread, title, status)
	}
	// 刪 Bot 時卡片一起刪（外鍵還在）。
	_, err = raw.Exec(`DELETE FROM bot WHERE id = 'b'`)
	check(t, err)
	var n int
	check(t, raw.QueryRow(`SELECT COUNT(*) FROM inbox_item`).Scan(&n))
	if n != 0 {
		t.Fatalf("刪 Bot 後卡片還有 %d 張", n)
	}
}
