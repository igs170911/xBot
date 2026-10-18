package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/collab"
	"xbot/internal/events"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

type env struct {
	db        *store.DB
	reg       *ops.Registry
	root      string
	bot, mate store.Bot
	srv       *httptest.Server
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), root: filepath.Join(dir, "bots")}
	e.bot, _ = db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes"})
	e.mate, _ = db.CreateBot(ctx, store.Bot{Name: "阿哲", Engine: "hermes"})
	s := &Service{Locate: DirLocator(e.root), Carry: collab.DirFiles{Root: e.root}, TempDir: dir}
	Register(e.reg, s)
	bus := events.New()
	e.srv = httptest.NewServer(api.New(api.Options{Registry: e.reg, Bus: bus, DB: db,
		Auth: auth.NewHTTP(auth.HTTPConfig{DB: db, AccessToken: "tok", LocalNoAuth: true}),
		Streams: map[string]api.StreamHandler{
			"POST /api/v1/files.upload":  s.UploadHandler(e.reg),
			"GET /api/v1/files.download": s.DownloadHandler(e.reg),
		}}))
	t.Cleanup(e.srv.Close)
	return e
}

// ws 是 Bot 的 /workspace 在這台電腦的位置。
func (e *env) ws(botID string) string { return filepath.Join(e.root, botID, "workspace") }

func (e *env) call(t *testing.T, id ops.Identity, op string, in, out any) error {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := e.reg.Invoke(ctx, id, op, raw, ops.Options{})
	if err == nil && out != nil {
		if err := json.Unmarshal(res.Output, out); err != nil {
			t.Fatal(err)
		}
	}
	return err
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

var (
	owner = ops.Owner("test")
	agent = ops.Identity{Kind: ops.KindAgent, ID: "agent1", Name: "Claude Code", Scopes: ops.Scopes, Via: "mcp"}
)

func TestListFolderAndAll(t *testing.T) {
	e := setup(t)
	w := e.ws(e.bot.ID)
	for _, p := range []string{"報告.md", "b.csv", "A資料/x.txt", ".hidden", "uploads/k/訂單.xlsx", "from/阿哲/價格.csv", "lessons/匯出/steps.json",
		"node_modules/p/i.js", "app/dist/bundle.js"} {
		write(t, w, p, "x")
	}
	var out ListOut
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID}, &out); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range out.Entries {
		names = append(names, en.Name)
	}
	// 資料夾在前，再依名稱；隱藏的不列。
	if out.State != "ready" || out.Path != "/workspace" || strings.Join(names, ",") != "app,A資料,from,lessons,node_modules,uploads,b.csv,報告.md" {
		t.Fatalf("%+v", names)
	}
	if !out.Entries[1].Dir || out.Entries[1].Kind != KindFolder || out.Entries[6].Kind != KindCSV || out.Entries[6].Path != "/workspace/b.csv" {
		t.Fatalf("%+v", out.Entries)
	}
	// 分頁。
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Offset: 6, Limit: 1, Hidden: true}, &out); err != nil || out.Total != 9 ||
		len(out.Entries) != 1 || out.Entries[0].Name != ".hidden" {
		t.Fatalf("分頁：%+v %v", out, err)
	}
	// 子資料夾，用完整路徑。
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Path: "/workspace/A資料"}, &out); err != nil || len(out.Entries) != 1 ||
		out.Entries[0].Path != "/workspace/A資料/x.txt" {
		t.Fatalf("%+v %v", out, err)
	}
	// 全部檔案：分組、成果在前。
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, All: true}, &out); err != nil {
		t.Fatal(err)
	}
	groups := map[string]Group{}
	for _, en := range out.Entries {
		groups[en.Path] = en.Group
		if en.Group == GroupPeer && en.From != "阿哲" {
			t.Errorf("隊友：%+v", en)
		}
	}
	if groups["/workspace/uploads/k/訂單.xlsx"] != GroupUpload || groups["/workspace/from/阿哲/價格.csv"] != GroupPeer ||
		groups["/workspace/lessons/匯出/steps.json"] != GroupLesson || groups["/workspace/報告.md"] != GroupResult {
		t.Fatalf("%+v", groups)
	}
	if _, ok := groups["/workspace/node_modules/p/i.js"]; ok {
		t.Fatal("套件資料夾不列")
	}
	if last := out.Entries[len(out.Entries)-1]; last.Important || last.Path != "/workspace/app/dist/bundle.js" {
		t.Fatalf("建置產物排最後：%+v", last)
	}
	// 不是資料夾、找不到、跑出去。
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Path: "b.csv"}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Path: "沒有"}, nil); code(err) != ops.CodeNotFound {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Path: "../"}, nil); code(err) != ops.CodeForbidden {
		t.Fatal(err)
	}
}

func TestListComputerStates(t *testing.T) {
	e := setup(t)
	reg := ops.New(ops.Config{DB: e.db})
	var wake []bool
	Register(reg, &Service{Locate: func(_ context.Context, botID string, w bool) (Workspace, error) {
		wake = append(wake, w)
		if botID == e.mate.ID {
			return nil, ErrNoComputer
		}
		if !w {
			return nil, ErrNotRunning
		}
		return Dir{Path: t.TempDir()}, nil
	}})
	e.reg = reg
	var out ListOut
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.mate.ID}, &out); err != nil || out.State != "none" {
		t.Fatalf("沒有電腦：%+v %v", out, err)
	}
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID}, &out); err != nil || out.State != "off" {
		t.Fatalf("沒開機：%+v %v", out, err)
	}
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Wake: true}, &out); err != nil || out.State != "ready" || !wake[len(wake)-1] {
		t.Fatalf("開機後列出：%+v %v", out, err)
	}
	if err := e.call(t, owner, "xbot.files.read", ReadIn{BotID: e.mate.ID, Path: "a.txt"}, nil); code(err) != ops.CodeNotFound {
		t.Fatal(err)
	}
}

func TestReadPreviews(t *testing.T) {
	e := setup(t)
	w := e.ws(e.bot.ID)
	write(t, w, "筆記.md", "# 標題\n內容")
	write(t, w, "main.go", "package main")
	write(t, w, "價格.csv", "商品,價格\n帆布包,1280\n")
	write(t, w, "data.bin", "abc\x00def")
	write(t, w, "big.txt", strings.Repeat("字", 200_000))
	write(t, w, "photo.png", "\x89PNG")
	write(t, w, "報告.docx", "PK")
	var out ReadOut
	read := func(in ReadIn) error {
		out = ReadOut{}
		return e.call(t, owner, "xbot.files.read", in, &out)
	}
	if err := read(ReadIn{BotID: e.bot.ID, Path: "/workspace/筆記.md"}); err != nil ||
		out.Kind != KindMarkdown || out.Text != "# 標題\n內容" || out.Binary || !strings.HasPrefix(out.Download, "/api/v1/files.download?") {
		t.Fatalf("Markdown：%+v %v", out, err)
	}
	if err := read(ReadIn{BotID: e.bot.ID, Path: "main.go"}); err != nil || out.Language != "go" || out.Kind != KindCode {
		t.Fatalf("程式碼：%+v %v", out, err)
	}
	if err := read(ReadIn{BotID: e.bot.ID, Path: "價格.csv"}); err != nil || out.Table == nil ||
		out.Table.Rows[0][1] != "1280" || !strings.Contains(out.Text, "帆布包") {
		t.Fatalf("CSV：%+v %v", out, err)
	}
	if err := read(ReadIn{BotID: e.bot.ID, Path: "data.bin"}); err != nil || !out.Binary || out.Text != "" {
		t.Fatalf("二進位：%+v %v", out, err)
	}
	// 大檔只讀前面（文字預覽上限）。
	if err := read(ReadIn{BotID: e.bot.ID, Path: "big.txt", MaxBytes: 1000}); err != nil || !out.Truncated ||
		len(out.Text) > 1000 || out.Size != 600_000 {
		t.Fatalf("大檔：%d %v %v", len(out.Text), out.Truncated, err)
	}
	if err := e.call(t, owner, "xbot.files.read", ReadIn{BotID: e.bot.ID, Path: "big.txt", MaxBytes: limits.FilePreviewBytesMax + 1}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("超過上限：%v", err)
	}
	if err := read(ReadIn{BotID: e.bot.ID, Path: "photo.png"}); err != nil || out.Inline == "" || out.Thumb == "" || !out.Binary {
		t.Fatalf("圖片：%+v %v", out, err)
	}
	if err := read(ReadIn{BotID: e.bot.ID, Path: "報告.docx"}); err != nil || out.Inline != "" || out.Note == "" {
		t.Fatalf("Office：%+v %v", out, err)
	}
	// 資料夾、找不到。
	os.Mkdir(filepath.Join(w, "dir"), 0o755)
	if err := e.call(t, owner, "xbot.files.read", ReadIn{BotID: e.bot.ID, Path: "dir"}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatal(err)
	}
	if err := e.call(t, owner, "xbot.files.read", ReadIn{BotID: e.bot.ID, Path: "nope.txt"}, nil); code(err) != ops.CodeNotFound {
		t.Fatal(err)
	}
}

// 路徑跑不出 /workspace，也不跟著連結走（LazyBoy：包含連結的目標也要在工作目錄裡；XBot 更嚴：連結一律不開）。
func TestPathTraversalAndSymlinks(t *testing.T) {
	e := setup(t)
	w := e.ws(e.bot.ID)
	write(t, w, "ok.txt", "ok")
	secret := filepath.Join(filepath.Dir(e.root), "host-secret.txt")
	os.WriteFile(secret, []byte("HOST_SECRET"), 0o600)
	os.Symlink(secret, filepath.Join(w, "link.txt"))
	os.Symlink(filepath.Dir(e.root), filepath.Join(w, "linkdir"))
	os.Symlink("ok.txt", filepath.Join(w, "inside-link.txt"))
	for _, p := range []string{"../../host-secret.txt", "/workspace/../host-secret.txt", "/etc/passwd", "a/../../../x", "..%2f..%2fetc"} {
		err := e.call(t, owner, "xbot.files.read", ReadIn{BotID: e.bot.ID, Path: p}, nil)
		if c := code(err); c != ops.CodeForbidden && c != ops.CodeNotFound {
			t.Errorf("%s：%v", p, err)
		}
	}
	for _, p := range []string{"link.txt", "linkdir/host-secret.txt", "inside-link.txt"} {
		if err := e.call(t, owner, "xbot.files.read", ReadIn{BotID: e.bot.ID, Path: p}, nil); code(err) != ops.CodeForbidden {
			t.Errorf("連結 %s 要拒絕：%v", p, err)
		}
		if err := e.call(t, owner, "xbot.files.write", WriteIn{BotID: e.bot.ID, Path: p + "/x", Content: "x"}, nil); code(err) == "" {
			t.Errorf("寫進連結 %s 要拒絕", p)
		}
	}
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Path: "linkdir"}, nil); code(err) != ops.CodeForbidden {
		t.Errorf("列連結資料夾：%v", err)
	}
	// 列資料夾時連結不出現。
	var out ListOut
	e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID}, &out)
	if len(out.Entries) != 1 || out.Entries[0].Name != "ok.txt" {
		t.Fatalf("%+v", out.Entries)
	}
	// 經 HTTP 下載也一樣。
	for _, p := range []string{"link.txt", "../../host-secret.txt"} {
		res, body := e.get(t, "/api/v1/files.download?"+url.Values{"bot_id": {e.bot.ID}, "path": {p}}.Encode())
		if res.StatusCode != http.StatusForbidden || strings.Contains(body, "HOST_SECRET") {
			t.Errorf("下載 %s：%d %s", p, res.StatusCode, body)
		}
	}
	if b, _ := os.ReadFile(secret); string(b) != "HOST_SECRET" {
		t.Fatal("主機上的檔案不能被改")
	}
}

func TestWriteMkdirMoveDelete(t *testing.T) {
	e := setup(t)
	w := e.ws(e.bot.ID)
	var en Entry
	if err := e.call(t, owner, "xbot.files.write", WriteIn{BotID: e.bot.ID, Path: "/workspace/新資料夾/筆記.md", Content: "hi"}, &en); err != nil ||
		en.Path != "/workspace/新資料夾/筆記.md" || en.Size != 2 {
		t.Fatalf("%+v %v", en, err)
	}
	if err := e.call(t, owner, "xbot.files.write", WriteIn{BotID: e.bot.ID, Path: "新資料夾/筆記.md", Content: "x"}, nil); code(err) != ops.CodeConflict {
		t.Fatalf("同名：%v", err)
	}
	if err := e.call(t, owner, "xbot.files.write", WriteIn{BotID: e.bot.ID, Path: "新資料夾/筆記.md", Content: "new", Overwrite: true}, nil); err != nil {
		t.Fatal(err)
	}
	// 代理要換掉檔案：送到待我處理。
	if err := e.call(t, agent, "xbot.files.write", WriteIn{BotID: e.bot.ID, Path: "新資料夾/筆記.md", Content: "agent", Overwrite: true}, nil); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理覆蓋要核准：%v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(w, "新資料夾/筆記.md")); string(b) != "new" {
		t.Fatalf("還沒核准不能寫：%q", b)
	}
	if err := e.call(t, owner, "xbot.files.mkdir", PathIn{BotID: e.bot.ID, Path: "a/b/c"}, &en); err != nil || !en.Dir {
		t.Fatalf("%+v %v", en, err)
	}
	if err := e.call(t, owner, "xbot.files.mkdir", PathIn{BotID: e.bot.ID, Path: "a/b/c"}, nil); err != nil {
		t.Fatal("已經有了不算錯")
	}
	if err := e.call(t, owner, "xbot.files.mkdir", PathIn{BotID: e.bot.ID, Path: "新資料夾/筆記.md"}, nil); code(err) != ops.CodeConflict {
		t.Fatalf("同名的檔案：%v", err)
	}
	// 搬移、改名。
	if err := e.call(t, owner, "xbot.files.move", MoveIn{BotID: e.bot.ID, From: "新資料夾/筆記.md", To: "/workspace/a/改名.md"}, &en); err != nil || en.Path != "/workspace/a/改名.md" {
		t.Fatalf("%+v %v", en, err)
	}
	if _, err := os.Stat(filepath.Join(w, "新資料夾/筆記.md")); !os.IsNotExist(err) {
		t.Fatal("原本的位置不在了")
	}
	write(t, w, "x.txt", "x")
	if err := e.call(t, owner, "xbot.files.move", MoveIn{BotID: e.bot.ID, From: "x.txt", To: "a/改名.md"}, nil); code(err) != ops.CodeConflict {
		t.Fatalf("不蓋掉：%v", err)
	}
	if err := e.call(t, owner, "xbot.files.move", MoveIn{BotID: e.bot.ID, From: "a", To: "a/b/a"}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("搬到自己裡面：%v", err)
	}
	if err := e.call(t, owner, "xbot.files.move", MoveIn{BotID: e.bot.ID, From: "x.txt", To: "../x.txt"}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("搬出去：%v", err)
	}
	// 刪除：代理一律等核准，而且不能代為核准；你本人直接刪。
	if err := e.call(t, agent, "xbot.files.delete", PathIn{BotID: e.bot.ID, Path: "a"}, nil); code(err) != ops.CodePendingApproval {
		t.Fatalf("代理刪除要核准：%v", err)
	}
	if info, ok := e.reg.Get("xbot.files.delete"); !ok || !info.NoDelegate || info.Risk != ops.RiskConfirm {
		t.Fatalf("刪除是 confirm 且不能代為核准：%+v", info)
	}
	if _, err := os.Stat(filepath.Join(w, "a/改名.md")); err != nil {
		t.Fatal("還沒核准不能刪")
	}
	var del DeleteOut
	if err := e.call(t, owner, "xbot.files.delete", PathIn{BotID: e.bot.ID, Path: "a"}, &del); err != nil || !del.Deleted {
		t.Fatalf("%+v %v", del, err)
	}
	if _, err := os.Stat(filepath.Join(w, "a")); !os.IsNotExist(err) {
		t.Fatal("刪掉了")
	}
	if err := e.call(t, owner, "xbot.files.delete", PathIn{BotID: e.bot.ID, Path: "/workspace"}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("不能刪整個工作目錄：%v", err)
	}
	// 稽核：寫入、搬移、刪除都有紀錄。
	rows, err := e.db.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Op+":"+r.Status] = true
	}
	for _, k := range []string{"xbot.files.write:ok", "xbot.files.move:ok", "xbot.files.delete:ok", "xbot.files.delete:pending_approval"} {
		if !seen[k] {
			t.Errorf("稽核沒有 %s：%v", k, seen)
		}
	}
}

// Bot 自己只能看自己的電腦，而且只能看不能改（寫入類不開放給 Bot）。
func TestBotSeesOnlyItsOwnWorkspace(t *testing.T) {
	e := setup(t)
	write(t, e.ws(e.bot.ID), "mine.txt", "mine")
	write(t, e.ws(e.mate.ID), "theirs.txt", "theirs")
	me := ops.Bot(e.bot.ID, e.bot.Name)
	var out ListOut
	if err := e.call(t, me, "xbot.files.list", ListIn{}, &out); err != nil || out.BotID != e.bot.ID || len(out.Entries) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	var r ReadOut
	if err := e.call(t, me, "xbot.files.read", ReadIn{Path: "mine.txt"}, &r); err != nil || r.Text != "mine" {
		t.Fatalf("%+v %v", r, err)
	}
	if err := e.call(t, me, "xbot.files.read", ReadIn{BotID: e.mate.ID, Path: "theirs.txt"}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("別人的電腦：%v", err)
	}
	if err := e.call(t, me, "xbot.files.list", ListIn{BotID: e.mate.ID}, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("別人的電腦：%v", err)
	}
	for _, op := range []string{"xbot.files.write", "xbot.files.delete", "xbot.files.upload", "xbot.files.move_between_bots", "xbot.files.download"} {
		if err := e.call(t, me, op, map[string]any{"bot_id": e.bot.ID, "path": "x", "content": "x", "name": "x", "to_bot_id": e.mate.ID,
			"paths": []string{"x"}}, nil); code(err) != ops.CodeForbidden {
			t.Errorf("Bot 不能用 %s：%v", op, err)
		}
	}
}

func TestUploadBase64AndLimits(t *testing.T) {
	e := setup(t)
	var en Entry
	in := UploadIn{BotID: e.bot.ID, Name: "報告.pdf", Content: base64.StdEncoding.EncodeToString([]byte("%PDF"))}
	if err := e.call(t, owner, "xbot.files.upload", in, &en); err != nil || en.Path != "/workspace/uploads/報告.pdf" || en.Size != 4 {
		t.Fatalf("%+v %v", en, err)
	}
	// 同名：加上 (2)。
	if err := e.call(t, owner, "xbot.files.upload", in, &en); err != nil || en.Path != "/workspace/uploads/報告 (2).pdf" {
		t.Fatalf("%+v %v", en, err)
	}
	// 檔名整理、放到指定資料夾。
	in = UploadIn{BotID: e.bot.ID, Dir: "/workspace/資料", Name: "../../evil\x01.txt", Content: base64.StdEncoding.EncodeToString([]byte("x"))}
	if err := e.call(t, owner, "xbot.files.upload", in, &en); err != nil || en.Path != "/workspace/資料/evil_.txt" {
		t.Fatalf("%+v %v", en, err)
	}
	in.Dir = "../"
	if err := e.call(t, owner, "xbot.files.upload", in, nil); code(err) != ops.CodeForbidden {
		t.Fatalf("跑出去：%v", err)
	}
	// 稽核不存檔案內容。
	rows, _ := e.db.ListAudit(ctx, store.AuditFilter{Limit: 10})
	for _, r := range rows {
		if r.Op == "xbot.files.upload" && strings.Contains(string(r.Params), "eA==") {
			t.Fatalf("稽核不能有檔案內容：%s", r.Params)
		}
	}
	if SizeLimit("a.png") != limits.UploadImageBytes || SizeLimit("a.pdf") != limits.UploadFileBytes {
		t.Fatal("圖片 20 MB、其他 50 MB")
	}
	// 直接用 Upload（multipart 的路徑）：超過大小上限。
	s := &Service{Locate: DirLocator(e.root)}
	_, err := s.Upload(WithStream(ctx, Stream{R: strings.NewReader(""), Size: limits.UploadImageBytes + 1}), UploadIn{BotID: e.bot.ID, Name: "大.jpg"}, false)
	if code(err) != ops.CodeLimit {
		t.Fatalf("圖片超過 20 MB：%v", err)
	}
}

func TestMoveBetweenBotsReusesCarry(t *testing.T) {
	e := setup(t)
	write(t, e.ws(e.bot.ID), "報告/價格.csv", "a,b\n1,2\n")
	write(t, e.ws(e.bot.ID), "dir/x.txt", "x")
	os.Symlink("/etc/passwd", filepath.Join(e.ws(e.bot.ID), "link"))
	var out CopyOut
	in := CopyIn{BotID: e.bot.ID, ToBotID: e.mate.ID, Paths: []string{"/workspace/報告/價格.csv", "dir", "link", "nope.txt", "../x"}}
	if err := e.call(t, owner, "xbot.files.move_between_bots", in, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 1 || out.Files[0].To != "/workspace/from/小秘/報告/價格.csv" || out.Files[0].Reused || len(out.Skipped) != 4 {
		t.Fatalf("%+v", out)
	}
	got, _ := os.ReadFile(filepath.Join(e.ws(e.mate.ID), "from/小秘/報告/價格.csv"))
	if sha256.Sum256(got) != sha256.Sum256([]byte("a,b\n1,2\n")) {
		t.Fatal("內容要一樣")
	}
	// 原本的檔案留著。
	if _, err := os.Stat(filepath.Join(e.ws(e.bot.ID), "報告/價格.csv")); err != nil {
		t.Fatal(err)
	}
	// 對方傳回來：內容沒變，不再複製，指向它自己那份（X02 去重）。
	back := CopyIn{BotID: e.mate.ID, ToBotID: e.bot.ID, Paths: []string{"/workspace/from/小秘/報告/價格.csv"}}
	if err := e.call(t, owner, "xbot.files.move_between_bots", back, &out); err != nil || len(out.Files) != 1 || !out.Files[0].Reused ||
		out.Files[0].To != "/workspace/報告/價格.csv" {
		t.Fatalf("去重：%+v %v", out, err)
	}
	if err := e.call(t, owner, "xbot.files.move_between_bots", CopyIn{BotID: e.bot.ID, ToBotID: e.bot.ID, Paths: []string{"a"}}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatal(err)
	}
	// 一次最多 10 個。
	many := make([]string, limits.CarryFilesMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("f%d", i)
	}
	if err := e.call(t, owner, "xbot.files.move_between_bots", CopyIn{BotID: e.bot.ID, ToBotID: e.mate.ID, Paths: many}, nil); code(err) != ops.CodeInvalidInput {
		t.Fatalf("超過 10 個：%v", err)
	}
}

// ── HTTP：上傳 → 列出 → 下載 ─────────────────────────

func (e *env) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

type part struct {
	name string
	data []byte
}

func (e *env) upload(t *testing.T, query string, parts ...part) (*http.Response, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		w, _ := mw.CreateFormFile("file", p.name)
		w.Write(p.data)
	}
	mw.Close()
	res, err := http.Post(e.srv.URL+"/api/v1/files.upload?"+query, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestHTTPUploadListDownload(t *testing.T) {
	e := setup(t)
	q := url.Values{"bot_id": {e.bot.ID}, "dir": {"/workspace/資料"}}.Encode()
	res, body := e.upload(t, q, part{"價格.csv", []byte("商品,價格\n帆布包,1280\n")}, part{"頁面.html", []byte("<script>alert(1)</script>")})
	if res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	var up struct{ Files []Entry }
	json.Unmarshal([]byte(body), &up)
	if len(up.Files) != 2 || up.Files[0].Path != "/workspace/資料/價格.csv" {
		t.Fatalf("%s", body)
	}
	var out ListOut
	if err := e.call(t, owner, "xbot.files.list", ListIn{BotID: e.bot.ID, Path: "資料"}, &out); err != nil || len(out.Entries) != 2 {
		t.Fatalf("%+v %v", out, err)
	}
	dl := func(p string, extra string) (*http.Response, string) {
		return e.get(t, "/api/v1/files.download?"+url.Values{"bot_id": {e.bot.ID}, "path": {p}}.Encode()+extra)
	}
	res, body = dl("/workspace/資料/價格.csv", "")
	if res.StatusCode != 200 || body != "商品,價格\n帆布包,1280\n" {
		t.Fatalf("%d %q", res.StatusCode, body)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "%E5%83%B9%E6%A0%BC.csv") {
		t.Fatalf("Content-Disposition：%q", cd)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") ||
		res.Header.Get("Content-Length") != fmt.Sprint(len(body)) {
		t.Fatalf("%v", res.Header)
	}
	// 文字直接顯示時一律當純文字；HTML 不能直接顯示（不會在這個網站執行）。
	res, _ = dl("/workspace/資料/價格.csv", "&inline=1")
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") || res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("%v", res.Header)
	}
	res, _ = dl("/workspace/資料/頁面.html", "&inline=1")
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("HTML 一律下載：%v", res.Header)
	}
	// 找不到、資料夾、沒有縮圖。
	if res, _ := dl("/workspace/資料/沒有.csv", ""); res.StatusCode != http.StatusNotFound {
		t.Fatal(res.StatusCode)
	}
	if res, _ := dl("/workspace/資料", ""); res.StatusCode != http.StatusBadRequest {
		t.Fatal(res.StatusCode)
	}
	if res, _ := dl("/workspace/資料/價格.csv", "&thumb=1"); res.StatusCode != http.StatusNotFound {
		t.Fatal(res.StatusCode)
	}
	// 上傳一張圖，拿縮圖。
	var img bytes.Buffer
	pngImage(&img, 800, 400)
	res, body = e.upload(t, url.Values{"bot_id": {e.bot.ID}}.Encode(), part{"照片.png", img.Bytes()})
	if res.StatusCode != 200 || !strings.Contains(body, "/workspace/uploads/照片.png") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	res, body = dl("/workspace/uploads/照片.png", "&thumb=1")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || len(body) == 0 {
		t.Fatalf("縮圖：%d %v", res.StatusCode, res.Header)
	}
	// 跨站請求擋掉。
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/files.download?"+url.Values{"bot_id": {e.bot.ID}, "path": {"資料/價格.csv"}}.Encode(), nil)
	req.Header.Set("Origin", "https://evil.example")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("跨站：%v %v", res.StatusCode, err)
	}
	// JSON 的 files.upload 還是一般的操作。
	jr, _ := http.Post(e.srv.URL+"/api/v1/files.upload", "application/json",
		strings.NewReader(fmt.Sprintf(`{"bot_id":%q,"name":"a.txt","content_base64":"aGk="}`, e.bot.ID)))
	jb, _ := io.ReadAll(jr.Body)
	if jr.StatusCode != 200 || !strings.Contains(string(jb), `"output"`) {
		t.Fatalf("%d %s", jr.StatusCode, jb)
	}
}

func TestHTTPUploadLimits(t *testing.T) {
	e := setup(t)
	q := url.Values{"bot_id": {e.bot.ID}}.Encode()
	// 一次最多 10 個。
	var parts []part
	for i := range limits.UploadFilesMax + 1 {
		parts = append(parts, part{fmt.Sprintf("f%d.txt", i), []byte("x")})
	}
	res, body := e.upload(t, q, parts...)
	if res.StatusCode != http.StatusTooManyRequests && !strings.Contains(body, `"limit"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	// 圖片超過 20 MB。
	res, body = e.upload(t, q, part{"大.jpg", make([]byte, limits.UploadImageBytes+1)})
	if !strings.Contains(body, `"limit"`) || !strings.Contains(body, "20 MB") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(e.ws(e.bot.ID), "uploads/大.jpg")); !os.IsNotExist(err) {
		t.Fatal("超過上限的不寫入")
	}
	// 不是 multipart、沒有檔案、沒有 bot_id、跑出去。
	r, _ := http.Post(e.srv.URL+"/api/v1/files.upload", "text/plain", strings.NewReader("x"))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatal(r.StatusCode)
	}
	if res, _ := e.upload(t, q); res.StatusCode != http.StatusBadRequest {
		t.Fatal(res.StatusCode)
	}
	if res, _ := e.upload(t, "", part{"a.txt", []byte("x")}); res.StatusCode != http.StatusBadRequest {
		t.Fatal(res.StatusCode)
	}
	if res, _ := e.upload(t, q+"&dir=..%2F..", part{"a.txt", []byte("x")}); res.StatusCode != http.StatusForbidden {
		t.Fatal(res.StatusCode)
	}
}

func pngImage(w io.Writer, width, height int) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	png.Encode(w, img)
}
