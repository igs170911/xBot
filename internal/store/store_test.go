package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"xbot/internal/backup"
)

var ctx = context.Background()

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(ctx, filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newBot(t *testing.T, db *DB, name string) Bot {
	t.Helper()
	return must[Bot](t)(db.CreateBot(ctx, Bot{Name: name}))
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for range 1000 {
		id := NewID()
		if len(id) != 32 || seen[id] {
			t.Fatalf("id 不對或重複：%q", id)
		}
		if id[:12] < prev {
			t.Fatalf("時間前綴不應該變小：%s < %s", id[:12], prev)
		}
		prev = id[:12]
		seen[id] = true
	}
}

func TestPragmas(t *testing.T) {
	db := openTest(t)
	for name, conn := range map[string]*sql.DB{"write": db.write, "read": db.read} {
		var mode string
		var fk, busy int
		check(t, conn.QueryRow("PRAGMA journal_mode").Scan(&mode))
		check(t, conn.QueryRow("PRAGMA foreign_keys").Scan(&fk))
		check(t, conn.QueryRow("PRAGMA busy_timeout").Scan(&busy))
		if mode != "wal" || fk != 1 || busy != 5000 {
			t.Fatalf("%s：journal_mode=%s foreign_keys=%d busy_timeout=%d", name, mode, fk, busy)
		}
	}
	if _, err := db.read.Exec("INSERT INTO setting(key, value, updated_at) VALUES ('x', '1', 0)"); err == nil {
		t.Fatal("唯讀連線不應該能寫入")
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db := must[*DB](t)(Open(ctx, path))
	ms := must[[]Migration](t)(Migrations())
	if len(ms) == 0 || ms[0].Version != 1 {
		t.Fatalf("migration 清單不對：%v", ms)
	}
	want := ms[len(ms)-1].Version
	if v := must[int](t)(db.SchemaVersion(ctx)); v != want {
		t.Fatalf("版本 %d，想要 %d", v, want)
	}
	newBot(t, db, "小秘")
	// 同一個連線再跑一次：不會出錯、不會重複記錄。
	check(t, applyMigrations(ctx, db.write, ms))
	check(t, db.Close())

	// 重新開檔：資料還在、版本表沒有多出來。
	db = must[*DB](t)(Open(ctx, path))
	defer db.Close()
	var n int
	check(t, db.read.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n))
	if n != len(ms) {
		t.Fatalf("schema_migrations 有 %d 筆，想要 %d", n, len(ms))
	}
	if bots := must[[]Bot](t)(db.ListBots(ctx, true)); len(bots) != 1 {
		t.Fatalf("重開後 Bot 數 %d", len(bots))
	}
}

func TestMigrationsAllTables(t *testing.T) {
	db := openTest(t)
	for _, table := range []string{"bot", "box", "host", "thread", "message", "task", "event", "audit_log",
		"agent_token", "approval", "inbox_item", "usage_entry", "setting", "op_request"} {
		var name string
		if err := db.read.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Errorf("少了資料表 %s：%v", table, err)
		}
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	db := openTest(t)
	bad := Migration{Version: 900, Name: "0900_bad.sql", SQL: "CREATE TABLE half(x INTEGER); THIS IS NOT SQL;"}
	ms := append(must[[]Migration](t)(Migrations()), bad)
	if err := applyMigrations(ctx, db.write, ms); err == nil || !strings.Contains(err.Error(), "0900_bad.sql") {
		t.Fatalf("壞的 migration 應該失敗並指出檔名：%v", err)
	}
	var n int
	check(t, db.read.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='half'").Scan(&n))
	if n != 0 {
		t.Fatal("失敗的 migration 應該整個復原")
	}
	if v := must[int](t)(db.SchemaVersion(ctx)); v >= 900 {
		t.Fatalf("失敗的 migration 不應該記版本：%d", v)
	}
}

func TestMigrationNewerDatabaseRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db := must[*DB](t)(Open(ctx, path))
	_, err := db.write.Exec("INSERT INTO schema_migrations(version, name, applied_at) VALUES (9999, 'future.sql', 0)")
	check(t, err)
	db.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "比這個 XBot 新") {
		t.Fatalf("比程式新的資料庫應該拒絕開啟：%v", err)
	}
}

func TestLoadMigrationsValidation(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"壞檔名": {"m/abc.sql": {Data: []byte("")}},
		"重複":  {"m/0001_a.sql": {Data: []byte("")}, "m/001_b.sql": {Data: []byte("")}},
	}
	for name, fsys := range cases {
		if _, err := loadMigrations(fsys, "m"); err == nil {
			t.Errorf("%s：應該失敗", name)
		}
	}
	ms := must[[]Migration](t)(loadMigrations(fstest.MapFS{
		"m/0010_c.sql": {Data: []byte("c")}, "m/0002_b.sql": {Data: []byte("b")}, "m/README.md": {Data: []byte("")},
	}, "m"))
	if len(ms) != 2 || ms[0].Version != 2 || ms[1].Version != 10 {
		t.Fatalf("排序不對：%+v", ms)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := openTest(t)
	if _, err := db.CreateThread(ctx, Thread{BotID: "沒有這個"}); err == nil {
		t.Fatal("對話串指到不存在的 Bot 應該失敗")
	}
	if err := db.UpsertBox(ctx, Box{BotID: "沒有這個"}); err == nil {
		t.Fatal("電腦指到不存在的 Bot 應該失敗")
	}
	b := newBot(t, db, "小秘")
	if err := db.UpsertBox(ctx, Box{BotID: b.ID, HostID: "沒有這台"}); err == nil {
		t.Fatal("電腦指到不存在的主機應該失敗")
	}
	if _, err := db.CreateTask(ctx, Task{BotID: b.ID, ThreadID: "沒有這串", Source: "owner"}); err == nil {
		t.Fatal("任務指到不存在的對話串應該失敗")
	}
}

func TestDeleteBotCascades(t *testing.T) {
	db := openTest(t)
	h := must[Host](t)(db.CreateHost(ctx, Host{Name: "這台主機", Kind: "local"}))
	b := newBot(t, db, "小秘")
	boss := newBot(t, db, "主管")
	b.ReportsTo = boss.ID
	b = must[Bot](t)(db.UpdateBot(ctx, b))
	check(t, db.UpsertBox(ctx, Box{BotID: b.ID, HostID: h.ID, State: "running"}))
	th := must[Thread](t)(db.CreateThread(ctx, Thread{BotID: b.ID}))
	must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: th.ID, Sender: "owner", Content: "嗨"}))
	must[Task](t)(db.CreateTask(ctx, Task{BotID: b.ID, ThreadID: th.ID, Source: "owner"}))
	must[Event](t)(db.AppendEvent(ctx, Event{BotID: b.ID, Kind: "x"}))
	must[Approval](t)(db.CreateApproval(ctx, Approval{BotID: b.ID, Action: "send"}))
	must[InboxItem](t)(db.CreateInboxItem(ctx, InboxItem{BotID: b.ID, Kind: "question"}))
	must[UsageEntry](t)(db.AddUsage(ctx, UsageEntry{BotID: b.ID, CostMicros: 5}))

	// 主機上還有電腦：不能刪。
	if err := db.DeleteHost(ctx, h.ID); err == nil {
		t.Fatal("主機上還有電腦時不應該能刪")
	}
	// 刪主管：下屬的 reports_to 變空。
	check(t, db.DeleteBot(ctx, boss.ID))
	if got := must[Bot](t)(db.GetBot(ctx, b.ID)); got.ReportsTo != "" {
		t.Fatalf("reports_to 應該清空：%q", got.ReportsTo)
	}

	check(t, db.DeleteBot(ctx, b.ID))
	for _, table := range []string{"box", "thread", "message", "task", "event", "approval", "inbox_item"} {
		var n int
		check(t, db.read.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Errorf("刪 Bot 後 %s 還有 %d 筆", table, n)
		}
	}
	// 用量帳保留。
	if u := must[UsageTotals](t)(db.SumUsage(ctx, UsageFilter{})); u.CostMicros != 5 {
		t.Fatalf("用量帳不應該跟著刪：%+v", u)
	}
	// 電腦沒了，主機可以刪。
	check(t, db.DeleteHost(ctx, h.ID))
	if err := db.DeleteBot(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("刪不存在的 Bot 應該回 ErrNotFound：%v", err)
	}
}

func TestTxRollback(t *testing.T) {
	db := openTest(t)
	boom := errors.New("boom")
	err := db.Tx(ctx, func(q *Queries) error {
		if _, err := q.CreateBot(ctx, Bot{Name: "會消失"}); err != nil {
			return err
		}
		if bots, err := q.ListBots(ctx, true); err != nil || len(bots) != 1 {
			t.Errorf("交易內應該讀得到自己的寫入：%v %v", bots, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("應該回傳 fn 的錯誤：%v", err)
	}
	if bots := must[[]Bot](t)(db.ListBots(ctx, true)); len(bots) != 0 {
		t.Fatal("交易失敗應該復原")
	}
	check(t, db.Tx(ctx, func(q *Queries) error {
		_, err := q.CreateBot(ctx, Bot{Name: "留下來"})
		return err
	}))
	if bots := must[[]Bot](t)(db.ListBots(ctx, true)); len(bots) != 1 {
		t.Fatal("交易成功應該留下資料")
	}
}

func TestConcurrentWriters(t *testing.T) {
	db := openTest(t)
	b := newBot(t, db, "小秘")
	const workers, each = 16, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*each*2)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if _, err := db.AppendEvent(ctx, Event{BotID: b.ID, Kind: fmt.Sprintf("w%d-%d", w, i)}); err != nil {
					errs <- err
				}
				if err := db.SetSetting(ctx, fmt.Sprintf("k%d", w), i); err != nil {
					errs <- err
				}
				if _, err := db.ListEvents(ctx, b.ID, 0, 10); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if id := must[int64](t)(db.LastEventID(ctx, b.ID)); id != workers*each {
		t.Fatalf("事件數 %d，想要 %d", id, workers*each)
	}
}

// 用 store 建出來的資料庫，備份再還原後資料一樣、可以直接開。
func TestBackupRestore(t *testing.T) {
	data := filepath.Join(t.TempDir(), "xbot")
	check(t, os.MkdirAll(data, 0o700))
	db := must[*DB](t)(Open(ctx, filepath.Join(data, FileName)))
	b := newBot(t, db, "小秘")
	th := must[Thread](t)(db.CreateThread(ctx, Thread{BotID: b.ID, Title: "第一串"}))
	must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: th.ID, Sender: "owner", Content: "備份前"}))
	check(t, db.SetSetting(ctx, "theme", "dark"))

	// 服務執行中（資料庫還開著、WAL 還沒合併）就備份。
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := backup.Create(backup.Options{DataDir: data, Out: out}); err != nil {
		t.Fatal(err)
	}
	must[Message](t)(db.AddMessage(ctx, Message{Scope: ScopeThread, ScopeID: th.ID, Sender: "owner", Content: "備份後"}))
	check(t, db.Close())

	if _, _, err := backup.Restore(out, data); err != nil {
		t.Fatal(err)
	}
	db = must[*DB](t)(Open(ctx, filepath.Join(data, FileName)))
	defer db.Close()
	if got := must[Bot](t)(db.GetBot(ctx, b.ID)); got.Name != "小秘" {
		t.Fatalf("還原後 Bot 不對：%+v", got)
	}
	msgs := must[[]Message](t)(db.ListMessages(ctx, ScopeThread, th.ID, 0, 0))
	if len(msgs) != 1 || msgs[0].Content != "備份前" {
		t.Fatalf("還原後訊息不對：%+v", msgs)
	}
	var theme string
	if ok := must[bool](t)(db.GetSettingInto(ctx, "theme", &theme)); !ok || theme != "dark" {
		t.Fatalf("還原後設定不對：%q", theme)
	}
	if v := must[int](t)(db.SchemaVersion(ctx)); v == 0 {
		t.Fatal("還原後應該有 schema 版本")
	}
	var table string
	if err := db.write.QueryRow("PRAGMA foreign_key_check").Scan(&table); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("還原後外鍵不一致：%s %v", table, err)
	}
}
