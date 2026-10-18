package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRollbackRestoresDB(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, out := newManager(t, f, "")
	if err := m.Install(writeBin(t, "good")); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(m.Paths.DataDir, "xbot.db")
	if err := os.WriteFile(db, []byte("schema 25"), 0o600); err != nil {
		t.Fatal(err)
	}
	var order []string
	m.Snapshot = func() (string, error) {
		order = append(order, "snapshot")
		b, _ := os.ReadFile(db)
		snap := filepath.Join(t.TempDir(), "snap")
		return snap, os.WriteFile(snap, b, 0o600)
	}
	m.RestoreDB = func(snap string) (string, error) {
		order = append(order, "restore")
		b, _ := os.ReadFile(snap)
		if err := os.Rename(db, db+".replaced"); err != nil {
			return "", err
		}
		return db + ".replaced", os.WriteFile(db, b, 0o600)
	}
	// 新版：套用了新的 migration 才發現起不來。
	f.failHealthyFor = "bad"
	origRun := f.run
	m.Run = func(name string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "restart") {
			if b, _ := os.ReadFile(m.Paths.Bin); string(b) == "bad" {
				_ = os.WriteFile(db, []byte("schema 26"), 0o600)
			}
		}
		return origRun(name, args...)
	}
	err := m.Update(writeBin(t, "bad"))
	if err == nil || !strings.Contains(err.Error(), "已換回上一版") {
		t.Fatalf("應回滾：%v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(db); string(b) != "schema 25" {
		t.Fatalf("資料庫應換回更新前的備份：%q", b)
	}
	if b, _ := os.ReadFile(db + ".replaced"); string(b) != "schema 26" {
		t.Fatal("新版改過的資料庫要保留")
	}
	if b, _ := os.ReadFile(m.Paths.Bin); string(b) != "good" {
		t.Fatal("應換回舊版")
	}
	if strings.Join(order, ",") != "snapshot,restore" {
		t.Fatalf("先備份、失敗才還原：%v", order)
	}
	if !strings.Contains(out.String(), "資料庫已換回更新前的備份") {
		t.Fatal(out.String())
	}
	// 換資料庫前要先停服務。
	joined := strings.Join(f.calls, "\n")
	stop := strings.LastIndex(joined, "stop xbot.service")
	if stop < 0 {
		t.Fatalf("應先停服務：\n%s", joined)
	}
}

func TestUpdateSnapshotFailureAborts(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, _ := newManager(t, f, "")
	if err := m.Install(writeBin(t, "good")); err != nil {
		t.Fatal(err)
	}
	m.Snapshot = func() (string, error) { return "", os.ErrPermission }
	if err := m.Update(writeBin(t, "v2")); err == nil || !strings.Contains(err.Error(), "沒有更新") {
		t.Fatalf("備份失敗不該更新：%v", err)
	}
	if b, _ := os.ReadFile(m.Paths.Bin); string(b) != "good" {
		t.Fatal("舊版應照常")
	}
}

func TestParseUnitEnv(t *testing.T) {
	text := "[Service]\nExecStart=/x serve\nEnvironment=XBOT_BIND=127.0.0.1\nEnvironment=\"XBOT_PUSH_SUBJECT=mailto:a b@c\" DOCKER_HOST=unix:///run/d.sock\n" +
		"Environment=XBOT_PUBLIC_URL=https://x.example.com/%%41\n"
	got := parseUnitEnv(text)
	want := []EnvVar{{"XBOT_BIND", "127.0.0.1"}, {"XBOT_PUSH_SUBJECT", "mailto:a b@c"}, {"DOCKER_HOST", "unix:///run/d.sock"}, {"XBOT_PUBLIC_URL", "https://x.example.com/%41"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d：%+v，應為 %+v", i, got[i], want[i])
		}
	}
}

func TestMergeEnvKeepsUnitUnlessExplicit(t *testing.T) {
	unit := []EnvVar{{"XBOT_DATA_DIR", "/d"}, {"XBOT_BIND", "127.0.0.1"}, {"XBOT_PUSH_SUBJECT", "mailto:old@x"},
		{"DOCKER_HOST", "unix:///run/d.sock"}, {"XBOT_PUBLIC_URL", "https://old"}}
	env := map[string]string{"XBOT_PUBLIC_URL": "https://new", "XBOT_TRUSTED_PROXIES": "10.0.0.1", "XBOT_URL": "http://x", "XBOT_E2E_PORT": "1"}
	got := MergeEnv(unit, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	s := ""
	for _, e := range got {
		s += e.Key + "=" + e.Value + ";"
	}
	if s != "XBOT_PUSH_SUBJECT=mailto:old@x;DOCKER_HOST=unix:///run/d.sock;XBOT_PUBLIC_URL=https://new;XBOT_TRUSTED_PROXIES=10.0.0.1;" {
		t.Fatalf("合併結果不對：%s", s)
	}
}

func TestRenderUnitRoundTrip(t *testing.T) {
	p := Paths{Bin: "/home/u/.local/bin/xbot", DataDir: "/home/u/my data/xbot"}
	extra := []EnvVar{{"XBOT_PUSH_SUBJECT", "mailto:me@x"}, {"XBOT_PUBLIC_URL", "https://x/%20"}}
	u := RenderUnit(p, Settings{Bind: "127.0.0.1", Port: 8781, Extra: extra})
	got := map[string]string{}
	for _, e := range parseUnitEnv(u) {
		got[e.Key] = e.Value
	}
	if got["XBOT_DATA_DIR"] != "/home/u/my data/xbot" || got["XBOT_PORT"] != "8781" || got["XBOT_BIND"] != "127.0.0.1" ||
		got["XBOT_PUSH_SUBJECT"] != "mailto:me@x" || got["XBOT_PUBLIC_URL"] != "https://x/%20" {
		t.Fatalf("寫出去再讀回來應一樣：%v\n%s", got, u)
	}
	if !strings.Contains(u, "%%20") {
		t.Fatalf("%% 要寫成 %%%%：\n%s", u)
	}
}

// make update 沒帶 XBOT_BIND 等變數時沿用服務檔裡的值；明確帶了才改。
func TestApplyUnitEnv(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, Unit)
	text := RenderUnit(Paths{Bin: "/b", DataDir: "/srv/xbot"}, Settings{Bind: "127.0.0.1", Port: 8790,
		Extra: []EnvVar{{"XBOT_PUSH_SUBJECT", "mailto:me@x"}, {"DOCKER_HOST", "unix:///x"}}})
	if err := os.WriteFile(unit, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"XBOT_DATA_DIR", "XBOT_BIND", "XBOT_PUSH_SUBJECT", "DOCKER_HOST"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("XBOT_PORT", "9999") // 這次明確設了
	set := ApplyUnitEnv(unit)
	if os.Getenv("XBOT_DATA_DIR") != "/srv/xbot" || os.Getenv("XBOT_BIND") != "127.0.0.1" || os.Getenv("XBOT_PUSH_SUBJECT") != "mailto:me@x" {
		t.Fatalf("應補上服務檔的值：%v", set)
	}
	if os.Getenv("XBOT_PORT") != "9999" {
		t.Fatal("明確設的值優先")
	}
	if _, ok := os.LookupEnv("DOCKER_HOST"); ok {
		t.Fatal("不是 XBot 的變數不要補進環境（只保留在服務檔裡）")
	}
}

func TestInstallReportsEnvChanges(t *testing.T) {
	f := &fakeSys{healthy: true, linger: true}
	m, out := newManager(t, f, "")
	m.Settings.Extra = []EnvVar{{"XBOT_PUSH_SUBJECT", "mailto:a@x"}}
	if err := m.Install(writeBin(t, "v1")); err != nil {
		t.Fatal(err)
	}
	m.Settings.Port = 8790
	out.Reset()
	if err := m.Install(writeBin(t, "v1")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "XBOT_PORT：8780 → 8790") {
		t.Fatalf("應印出服務設定的變化：%s", out.String())
	}
	b, _ := os.ReadFile(m.Paths.UnitPath())
	if !strings.Contains(string(b), "Environment=XBOT_PUSH_SUBJECT=mailto:a@x") {
		t.Fatalf("%s", b)
	}
}
