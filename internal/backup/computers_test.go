package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/store"
)

// fakeVol 是一個項目：名稱「kind/相對路徑」、內容（資料夾時 nil）、擁有者。
type fakeVol struct {
	name string
	body []byte
	uid  int
}

// fakeComputers 模擬每台主機上的 volume。
type fakeComputers struct {
	data    map[string][]fakeVol // bot id → 項目
	offline map[string]bool      // host id → 連不上
	failAt  map[string]int       // bot id → 讀到第幾個檔案時斷線
	got     map[string][]fakeVol // 還原寫回去的
	closed  bool
}

func (f *fakeComputers) Export(_ context.Context, ref ComputerRef, logins bool, emit func(string, *tar.Header, io.Reader) error) error {
	if f.offline[ref.Host.ID] {
		return fmt.Errorf("%w：%s", hosts.ErrHostOffline, ref.Host.Name)
	}
	items, ok := f.data[ref.BotID]
	if !ok {
		return box.ErrNoData
	}
	files := 0
	for _, it := range items {
		kind, rel, _ := strings.Cut(it.name, "/")
		if !logins && box.IsLoginPath(kind, rel) {
			continue
		}
		h := &tar.Header{Name: it.name, Uid: it.uid, Gid: it.uid, Mode: 0o600}
		var r io.Reader = bytes.NewReader(nil)
		if it.body == nil {
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		} else {
			h.Typeflag, h.Size = tar.TypeReg, int64(len(it.body))
			r = bytes.NewReader(it.body)
			files++
			if n, ok := f.failAt[ref.BotID]; ok && files == n {
				r = io.MultiReader(bytes.NewReader(it.body[:1]), errReader{})
			}
		}
		if err := emit(it.name, h, r); err != nil {
			return err
		}
	}
	return nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("連線中斷") }

type fakeSink struct {
	f     *fakeComputers
	botID string
	items []fakeVol
}

func (s *fakeSink) Write(name string, h *tar.Header, r io.Reader) error {
	var body []byte
	if h.Typeflag == tar.TypeReg {
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		body = b
	}
	s.items = append(s.items, fakeVol{name: name, body: body, uid: h.Uid})
	return nil
}

func (s *fakeSink) Close(_ context.Context, cause error) ([]string, error) {
	if cause != nil {
		return nil, cause
	}
	if s.f.got == nil {
		s.f.got = map[string][]fakeVol{}
	}
	s.f.got[s.botID] = s.items
	return []string{"xbot-box-" + s.botID + "-home-before-restore-x"}, nil
}

func (f *fakeComputers) Import(_ context.Context, ref ComputerRef, _ string) (VolumeSink, error) {
	if f.offline[ref.Host.ID] {
		return nil, fmt.Errorf("%w：%s", hosts.ErrHostOffline, ref.Host.Name)
	}
	return &fakeSink{f: f, botID: ref.BotID}, nil
}

func (f *fakeComputers) Close() error { f.closed = true; return nil }

// makeStoreData 建一個有真的資料庫（store）的資料目錄：本機、一台遠端主機，四個 Bot。
func makeStoreData(t *testing.T) (dir string, ids map[string]string) {
	t.Helper()
	ctx := context.Background()
	dir = filepath.Join(t.TempDir(), "xbot")
	write(t, filepath.Join(dir, "access-token"), "secret")
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := db.CreateHost(ctx, store.Host{Name: "這台電腦", Kind: "local"})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := db.CreateHost(ctx, store.Host{Name: "cloud-01", Kind: "ssh", Address: "203.0.113.9", Port: 22, SSHUser: "xbot"})
	if err != nil {
		t.Fatal(err)
	}
	ids = map[string]string{"local": local.ID, "remote": remote.ID}
	for _, b := range []struct{ name, host, box string }{
		{"小秘", "", local.ID},        // 電腦在本機
		{"阿凱", local.ID, remote.ID}, // 搬到遠端了（電腦為準）
		{"新來的", "", ""},             // 還沒有電腦
		{"雲端", remote.ID, ""},       // 指定放遠端，還沒有電腦
	} {
		bot, err := db.CreateBot(ctx, store.Bot{Name: b.name, HostID: b.host})
		if err != nil {
			t.Fatal(err)
		}
		ids[b.name] = bot.ID
		if b.box != "" {
			if err := db.UpsertBox(ctx, store.Box{BotID: bot.ID, HostID: b.box, State: "sleeping"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir, ids
}

func TestListComputers(t *testing.T) {
	dir, ids := makeStoreData(t)
	refs, err := ListComputers(context.Background(), filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.BotName] = r.Host.Name
	}
	want := map[string]string{"小秘": "這台電腦", "阿凱": "cloud-01", "新來的": "這台電腦", "雲端": "cloud-01"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s 應在 %s，得到 %s（%v）", k, v, got[k], ids)
		}
	}
	for _, r := range refs {
		if r.Host.Kind == "ssh" && (r.Host.Address != "203.0.113.9" || r.Host.SSHUser != "xbot") {
			t.Fatalf("遠端主機的連線設定沒讀到：%+v", r.Host)
		}
	}
}

func archiveNames(t *testing.T, file string) map[string]*tar.Header {
	t.Helper()
	out := map[string]*tar.Header{}
	if err := walkTar(file, func(h *tar.Header, _ io.Reader) error {
		hh := *h
		out[h.Name] = &hh
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBackupComputersV2(t *testing.T) {
	dir, ids := makeStoreData(t)
	fc := &fakeComputers{data: map[string][]fakeVol{
		ids["小秘"]: {
			{name: "home", uid: 1000}, {name: "home/.bashrc", body: []byte("alias ll='ls -l'"), uid: 1000},
			{name: "home/chrome-profile", uid: 1000}, {name: "home/chrome-profile/Cookies", body: []byte("login"), uid: 1000},
			{name: "workspace", uid: 1000}, {name: "workspace/報告.md", body: []byte("# 報告"), uid: 1000},
			{name: "hermes", uid: 1000}, {name: "hermes/config.yaml", body: []byte("model: x"), uid: 1000},
		},
		ids["阿凱"]: {{name: "workspace", uid: 1000}, {name: "workspace/a.txt", body: []byte("a"), uid: 1000}},
	}, offline: map[string]bool{ids["remote"]: true}}
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	var log []string
	m, err := Create(Options{DataDir: dir, Out: out, Computers: fc, Report: func(f string, a ...any) { log = append(log, fmt.Sprintf(f, a...)) }})
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != 2 {
		t.Fatalf("格式應為 2：%d", m.Format)
	}
	by := map[string]ComputerEntry{}
	for _, c := range m.Computers {
		by[c.BotName] = c
	}
	if c := by["小秘"]; !c.Included || c.Files != 3 || c.Skipped != "" {
		t.Fatalf("本機的電腦資料應完整備到（不含登入）：%+v", c)
	}
	if c := by["阿凱"]; c.Included || !strings.Contains(c.Skipped, "主機離線") {
		t.Fatalf("遠端離線時應記下沒備到：%+v", c)
	}
	if c := by["新來的"]; !c.Empty || c.Included || c.Skipped != "" {
		t.Fatalf("還沒有電腦的 Bot 不算問題：%+v", c)
	}
	if !strings.Contains(strings.Join(log, "\n"), "沒有備到") {
		t.Fatalf("應報告沒備到的電腦：%v", log)
	}
	names := archiveNames(t, out)
	p := ComputersDir + "/" + ids["小秘"] + "/"
	for _, want := range []string{p + "home/.bashrc", p + "workspace/報告.md", p + "hermes/config.yaml", p + "home/"} {
		if names[want] == nil {
			t.Fatalf("備份檔缺 %s：%v", want, keys(names))
		}
	}
	if names[p+"home/.bashrc"].Uid != 1000 {
		t.Fatal("擁有者應照原樣")
	}
	for n := range names {
		if strings.Contains(n, "chrome-profile") && strings.HasPrefix(n, ComputersDir) {
			t.Fatalf("預設不該備份瀏覽器登入：%s", n)
		}
	}

	// 還原：資料目錄照舊還原（不含 xbot-computers），電腦資料寫回去；遠端那台不在線上。
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Restore(out, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ComputersDir)); !os.IsNotExist(err) {
		t.Fatal("電腦資料不該解到資料目錄裡")
	}
	if read(t, filepath.Join(dir, "access-token")) != "secret" {
		t.Fatal("資料目錄沒有還原")
	}
	fc2 := &fakeComputers{}
	res, err := RestoreComputers(context.Background(), out, dir, fc2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].Restored || res[0].BotID != ids["小秘"] || len(res[0].Kept) != 1 {
		t.Fatalf("只有備到的那台要還原：%+v", res)
	}
	got := fc2.got[ids["小秘"]]
	var gotNames []string
	for _, it := range got {
		gotNames = append(gotNames, it.name)
	}
	sort.Strings(gotNames)
	if strings.Join(gotNames, ",") != "hermes,hermes/config.yaml,home,home/.bashrc,workspace,workspace/報告.md" {
		t.Fatalf("寫回去的項目不對：%v", gotNames)
	}
	for _, it := range got {
		if it.name == "workspace/報告.md" && (string(it.body) != "# 報告" || it.uid != 1000) {
			t.Fatalf("內容或擁有者不對：%+v", it)
		}
	}
}

func keys(m map[string]*tar.Header) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestBackupComputersIncludeLoginsAndPartial(t *testing.T) {
	dir, ids := makeStoreData(t)
	fc := &fakeComputers{data: map[string][]fakeVol{
		ids["小秘"]: {{name: "home", uid: 1000}, {name: "home/chrome-profile/Cookies", body: []byte("login"), uid: 1000}},
		ids["阿凱"]: {{name: "workspace", uid: 1000}, {name: "workspace/a.txt", body: []byte("aaaa"), uid: 1000},
			{name: "workspace/b.txt", body: []byte("bbbb"), uid: 1000}},
	}, failAt: map[string]int{ids["阿凱"]: 2}}
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	m, err := Create(Options{DataDir: dir, Out: out, Computers: fc, IncludeLogins: true})
	if err != nil {
		t.Fatalf("讀主機斷線不該讓整份備份失敗：%v", err)
	}
	by := map[string]ComputerEntry{}
	for _, c := range m.Computers {
		by[c.BotName] = c
	}
	if !by["小秘"].Included {
		t.Fatalf("%+v", by["小秘"])
	}
	if c := by["阿凱"]; c.Included || !strings.HasPrefix(c.Skipped, "不完整") {
		t.Fatalf("讀到一半斷線應記成不完整：%+v", c)
	}
	names := archiveNames(t, out)
	if names[ComputersDir+"/"+ids["小秘"]+"/home/chrome-profile/Cookies"] == nil {
		t.Fatal("IncludeLogins 應包含瀏覽器登入")
	}
	// 備份檔格式仍然完整（讀得完），不完整的那台不會被還原。
	fc2 := &fakeComputers{}
	res, err := RestoreComputers(context.Background(), out, dir, fc2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].BotID != ids["小秘"] {
		t.Fatalf("%+v", res)
	}
	if _, ok := fc2.got[ids["阿凱"]]; ok {
		t.Fatal("不完整的不該還原")
	}
}

func TestRestoreComputersHostOfflineAndOnly(t *testing.T) {
	dir, ids := makeStoreData(t)
	data := map[string][]fakeVol{
		ids["小秘"]: {{name: "workspace", uid: 1000}, {name: "workspace/x", body: []byte("x"), uid: 1000}},
		ids["阿凱"]: {{name: "workspace", uid: 1000}, {name: "workspace/y", body: []byte("y"), uid: 1000}},
	}
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Create(Options{DataDir: dir, Out: out, Computers: &fakeComputers{data: data}}); err != nil {
		t.Fatal(err)
	}
	fc := &fakeComputers{offline: map[string]bool{ids["remote"]: true}}
	res, err := RestoreComputers(context.Background(), out, dir, fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ComputerResult{}
	for _, r := range res {
		by[r.BotName] = r
	}
	if !by["小秘"].Restored {
		t.Fatalf("%+v", by["小秘"])
	}
	if r := by["阿凱"]; r.Restored || !strings.Contains(r.Error, "主機離線") || r.HostName != "cloud-01" {
		t.Fatalf("遠端離線應跳過並說明：%+v", r)
	}
	// 主機回來後只補還原那一台。
	fc.offline = nil
	fc.got = nil
	res, err = RestoreComputers(context.Background(), out, dir, fc, []string{ids["阿凱"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].Restored || string(fc.got[ids["阿凱"]][1].body) != "y" {
		t.Fatalf("%+v %+v", res, fc.got)
	}
	if _, ok := fc.got[ids["小秘"]]; ok {
		t.Fatal("--bot 以外的不該還原")
	}
}

// v1（v1.0 之前）的備份檔：沒有 format、沒有電腦資料，照樣還原。
func TestRestoreV1Archive(t *testing.T) {
	file := filepath.Join(t.TempDir(), "v1.tar.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	add("access-token", "old-secret")
	add("bots/b1/workspace/r.md", "舊報告")
	mb, _ := json.Marshal(map[string]any{"version": "dev", "created_at": "2026-09-01T00:00:00Z", "include_logins": false, "files": 2})
	add(ManifestName, string(mb))
	tw.Close()
	gz.Close()
	f.Close()

	dir := filepath.Join(t.TempDir(), "xbot")
	m, _, err := Restore(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != 0 || m.Files != 2 {
		t.Fatalf("%+v", m)
	}
	if read(t, filepath.Join(dir, "access-token")) != "old-secret" || read(t, filepath.Join(dir, "bots", "b1", "workspace", "r.md")) != "舊報告" {
		t.Fatal("v1 備份應還原資料目錄")
	}
	res, err := RestoreComputers(context.Background(), file, dir, &fakeComputers{}, nil)
	if err != nil || len(res) != 0 {
		t.Fatalf("v1 沒有電腦資料：%v %v", res, err)
	}
}

func TestRejectsNewerFormat(t *testing.T) {
	file := filepath.Join(t.TempDir(), "v9.tar.gz")
	f, _ := os.Create(file)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	mb, _ := json.Marshal(map[string]any{"format": 9, "version": "v9"})
	_ = tw.WriteHeader(&tar.Header{Name: ManifestName, Mode: 0o600, Size: int64(len(mb)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(mb)
	tw.Close()
	gz.Close()
	f.Close()
	if _, _, err := Restore(file, filepath.Join(t.TempDir(), "d")); err == nil || !strings.Contains(err.Error(), "較新版") {
		t.Fatalf("應拒絕較新的格式：%v", err)
	}
}

func TestRestoreDB(t *testing.T) {
	data := makeData(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Create(Options{DataDir: data, Out: out}); err != nil {
		t.Fatal(err)
	}
	// 新版改了資料庫（例：套用了新的 migration），其他檔案也改了。
	write(t, filepath.Join(data, "xbot.db"), "新版的資料庫")
	write(t, filepath.Join(data, "xbot.db-wal"), "wal")
	write(t, filepath.Join(data, "access-token"), "changed")
	kept, err := RestoreDB(out, data)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, kept) != "新版的資料庫" || read(t, kept+"-wal") != "wal" {
		t.Fatal("換下來的資料庫要保留")
	}
	if _, err := os.Stat(filepath.Join(data, "xbot.db-wal")); !os.IsNotExist(err) {
		t.Fatal("舊的 wal 不能留著（會套到還原的資料庫上）")
	}
	if read(t, filepath.Join(data, "access-token")) != "changed" {
		t.Fatal("只換資料庫，其他檔案不動")
	}
	db, err := sqlOpen(filepath.Join(data, "xbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow("SELECT name FROM bot").Scan(&name); err != nil || name != "小秘" {
		t.Fatalf("資料庫沒換回來：%q %v", name, err)
	}
}

func sqlOpen(p string) (*sql.DB, error) { return sql.Open("sqlite", p) }
