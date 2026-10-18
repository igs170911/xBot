package backup

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func makeData(t *testing.T) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "xbot")
	write(t, filepath.Join(data, "access-token"), "secret")
	write(t, filepath.Join(data, "bots", "b1", "workspace", "report.md"), "# 報告")
	write(t, filepath.Join(data, "bots", "b1", "home", "chrome-profile", "Cookies"), "login")
	db, err := sql.Open("sqlite", filepath.Join(data, "xbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE bot(name TEXT)", "INSERT INTO bot VALUES ('小秘')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func TestRoundTrip(t *testing.T) {
	data := makeData(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	m, err := Create(Options{DataDir: data, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if m.Files < 3 {
		t.Fatalf("檔案數太少：%+v", m)
	}

	// 模擬資料遺失後還原
	if err := os.RemoveAll(data); err != nil {
		t.Fatal(err)
	}
	got, kept, err := Restore(out, data)
	if err != nil {
		t.Fatal(err)
	}
	if kept != "" {
		t.Fatalf("原本沒有資料時不該保留舊資料：%s", kept)
	}
	if got.IncludeLogins {
		t.Fatal("預設不含登入資料")
	}
	if read(t, filepath.Join(data, "access-token")) != "secret" || read(t, filepath.Join(data, "bots", "b1", "workspace", "report.md")) != "# 報告" {
		t.Fatal("檔案內容不對")
	}
	if _, err := os.Stat(filepath.Join(data, "bots", "b1", "home", "chrome-profile")); err == nil {
		t.Fatal("預設不該備份登入資料")
	}
	db, err := sql.Open("sqlite", filepath.Join(data, "xbot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow("SELECT name FROM bot").Scan(&name); err != nil || name != "小秘" {
		t.Fatalf("資料庫內容不對：%q %v", name, err)
	}
}

func TestIncludeLoginsAndKeepOld(t *testing.T) {
	data := makeData(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := Create(Options{DataDir: data, Out: out, IncludeLogins: true}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(data, "access-token"), "changed")
	_, kept, err := Restore(out, data)
	if err != nil {
		t.Fatal(err)
	}
	if kept == "" || read(t, filepath.Join(kept, "access-token")) != "changed" {
		t.Fatal("還原前應保留原本的資料")
	}
	if read(t, filepath.Join(data, "bots", "b1", "home", "chrome-profile", "Cookies")) != "login" {
		t.Fatal("IncludeLogins 應包含登入資料")
	}
}

func TestRejectsUnsafeArchive(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	f, _ := os.Create(bad)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, n := range []string{ManifestName, "../evil"} {
		body := "{}"
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	f.Close()
	data := filepath.Join(t.TempDir(), "xbot")
	if _, _, err := Restore(bad, data); err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("應拒絕不安全的路徑：%v", err)
	}
}

func TestRejectsNonBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tar.NewWriter(gz).Close()
	gz.Close()
	f.Close()
	if _, _, err := Restore(p, filepath.Join(t.TempDir(), "d")); err == nil || !strings.Contains(err.Error(), "不是 XBot 的備份檔") {
		t.Fatalf("應拒絕不是備份的檔案：%v", err)
	}
}

func TestIsLogin(t *testing.T) {
	cases := map[string]bool{
		"bots/b1/home/chrome-profile":            true,
		"bots/b1/home/chrome-profile/Default/x":  true,
		"bots/b1/home/.config/chromium/Local":    true,
		"bots/b1/workspace/chrome-profile-notes": false,
		"access-token":                           false,
	}
	for p, want := range cases {
		if got := isLogin(p); got != want {
			t.Fatalf("isLogin(%q)=%v，應為 %v", p, got, want)
		}
	}
}
