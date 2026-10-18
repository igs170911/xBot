package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 0009 重建 host 與 box：舊的主機、電腦都要留著，外鍵照舊有效，Bot 多了 host_id。
func TestMigration0009KeepsHostsAndBoxes(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	check(t, err)
	defer raw.Close()
	ms := must[[]Migration](t)(Migrations())
	var before []Migration
	for _, m := range ms {
		if m.Version < 9 {
			before = append(before, m)
		}
	}
	check(t, applyMigrations(ctx, raw, before))
	for _, q := range []string{
		`INSERT INTO host(id, name, kind, address, status, arch, max_running, created_at, updated_at) VALUES ('h', '這台電腦', 'local', 'unix:///x', 'online', 'x86_64', 4, 1, 1)`,
		`INSERT INTO bot(id, name, idle_minutes, created_at, updated_at) VALUES ('b', 'x', 30, 1, 1)`,
		`INSERT INTO box(bot_id, host_id, container_id, state, novnc_port, hermes_port, created_at, updated_at) VALUES ('b', 'h', 'c1', 'running', 6080, 8642, 1, 1)`,
	} {
		_, err := raw.Exec(q)
		check(t, err)
	}
	check(t, applyMigrations(ctx, raw, ms))
	var name, status, container string
	var maxRunning, hermes int
	check(t, raw.QueryRow(`SELECT name, status, max_running FROM host WHERE id = 'h'`).Scan(&name, &status, &maxRunning))
	check(t, raw.QueryRow(`SELECT container_id, hermes_port FROM box WHERE bot_id = 'b'`).Scan(&container, &hermes))
	if name != "這台電腦" || status != "online" || maxRunning != 4 || container != "c1" || hermes != 8642 {
		t.Fatalf("migration 後：%q %q %d %q %d", name, status, maxRunning, container, hermes)
	}
	// 新狀態 failed 可以存；上面有電腦的主機不能刪（外鍵還在，而且指向新的 host 表）。
	_, err = raw.Exec(`UPDATE host SET status = 'failed' WHERE id = 'h'`)
	check(t, err)
	if _, err := raw.Exec(`DELETE FROM host WHERE id = 'h'`); err == nil {
		t.Fatal("上面有電腦的主機不應該能刪")
	}
	// bot.host_id：主機刪掉時清成空白。
	_, err = raw.Exec(`UPDATE bot SET host_id = 'h' WHERE id = 'b'`)
	check(t, err)
	_, err = raw.Exec(`DELETE FROM box`)
	check(t, err)
	_, err = raw.Exec(`DELETE FROM host WHERE id = 'h'`)
	check(t, err)
	var host sql.NullString
	check(t, raw.QueryRow(`SELECT host_id FROM bot WHERE id = 'b'`).Scan(&host))
	if host.Valid {
		t.Fatalf("主機刪掉後 bot.host_id 應該清空：%v", host)
	}
	var fkErrors int
	check(t, raw.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fkErrors))
	if fkErrors != 0 {
		t.Fatalf("外鍵檢查有 %d 個錯誤", fkErrors)
	}
}

func TestHostLoadsAndBotHost(t *testing.T) {
	db := openTest(t)
	h := must[Host](t)(db.CreateHost(ctx, Host{Name: "cloud-01", Kind: "ssh", Address: "cloud-01", Port: 22, SSHUser: "xbot", MaxRunning: 3}))
	a, b, c := newBot(t, db, "阿凱"), newBot(t, db, "小編"), newBot(t, db, "數據喵")
	check(t, db.UpsertBox(ctx, Box{BotID: a.ID, HostID: h.ID, State: "running"}))
	check(t, db.UpsertBox(ctx, Box{BotID: b.ID, HostID: h.ID, State: "sleeping"}))
	check(t, db.SetBotHost(ctx, c.ID, h.ID))
	loads := must[map[string]HostLoad](t)(db.HostLoads(ctx))
	if l := loads[h.ID]; l.Running != 1 || l.Boxes != 2 || l.Bots != 3 {
		t.Fatalf("主機負載：%+v", l)
	}
	if got := must[Bot](t)(db.GetBot(ctx, c.ID)); got.HostID != h.ID {
		t.Fatalf("bot.host_id：%q", got.HostID)
	}
	check(t, db.SetHostHealth(ctx, h.ID, HostFailed, "主機金鑰不符", []byte(`{"state":"failed"}`)))
	got := must[Host](t)(db.GetHost(ctx, h.ID))
	if got.Status != HostFailed || got.StatusError != "主機金鑰不符" || string(got.Health) != `{"state":"failed"}` || got.SSHUser != "xbot" || got.Port != 22 {
		t.Fatalf("主機狀態：%+v", got)
	}
	check(t, db.SetHostStatus(ctx, h.ID, HostOnline))
	if got := must[Host](t)(db.GetHost(ctx, h.ID)); got.StatusError != "" || string(got.Health) != `{"state":"failed"}` {
		t.Fatalf("SetHostStatus 應清掉錯誤、不改 health：%+v", got)
	}
}
