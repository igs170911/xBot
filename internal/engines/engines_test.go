package engines

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"xbot/internal/engine/acp"
	"xbot/internal/ops"
	"xbot/internal/store"
)

var bg = context.Background()

// 測試用的假憑證（絕對不用真的登入）。
const fakeToken = "FAKE-OAUTH-TOKEN-for-tests-only"

type entry struct {
	mode     int64
	uid, gid int
	data     string
	link     string
	typ      byte
}

// fakeFiles 是容器的檔案系統：記下 CopyToContainer 寫進去的每個項目。
type fakeFiles struct {
	mu    sync.Mutex
	files map[string]entry
	fail  error
}

func (f *fakeFiles) CopyToContainer(_ context.Context, _ string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return client.CopyToContainerResult{}, f.fail
	}
	if o.CopyUIDGID {
		// 擁有者照 tar 標頭（Bot 使用者或 root）；CopyUIDGID 會讓 Docker 把所有檔案改成容器的使用者（B13 起是 1000:1000，
		// 而且是在主機的 passwd 裡找，遠端主機上找不到就失敗）。
		return client.CopyToContainerResult{}, errors.New("不能用 CopyUIDGID")
	}
	tr := tar.NewReader(o.Content)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return client.CopyToContainerResult{}, nil
		}
		if err != nil {
			return client.CopyToContainerResult{}, err
		}
		data, _ := io.ReadAll(tr)
		f.files[path.Join(o.DestinationPath, h.Name)] = entry{mode: h.Mode, uid: h.Uid, gid: h.Gid, data: string(data), link: h.Linkname, typ: h.Typeflag}
	}
}

func (f *fakeFiles) get(p string) entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[p]
}

type env struct {
	db      *store.DB
	reg     *ops.Registry
	svc     *Service
	files   *fakeFiles
	home    string
	locErr  error
	noBox   bool
	closed  []string
	envVars map[string]string
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := store.Open(bg, filepath.Join(t.TempDir(), store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, reg: ops.New(ops.Config{DB: db}), files: &fakeFiles{files: map[string]entry{}}, home: t.TempDir(), envVars: map[string]string{}}
	e.svc = &Service{DB: db, HostHome: e.home, Getenv: func(k string) string { return e.envVars[k] }, UID: 1000, GID: 1000,
		Locate: func(context.Context, string) (Container, error) {
			if e.locErr != nil {
				return Container{}, e.locErr
			}
			if e.noBox {
				return Container{}, nil
			}
			return Container{API: e.files, ID: "c1", Running: true}, nil
		},
		Close:  func(botID string) { e.closed = append(e.closed, botID) },
		Logger: slog.New(slog.DiscardHandler),
	}
	Register(e.reg, e.svc)
	return e
}

func (e *env) hostCred(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) bot(t *testing.T, engine string) store.Bot {
	t.Helper()
	b, err := e.db.CreateBot(bg, store.Bot{Name: "小秘", Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var owner = ops.Owner("test")

func (e *env) call(id ops.Identity, op string, in any, opts ...ops.Options) (json.RawMessage, error) {
	raw, _ := json.Marshal(in)
	var o ops.Options
	if len(opts) > 0 {
		o = opts[0]
	}
	res, err := e.reg.Invoke(bg, id, op, raw, o)
	return res.Output, err
}

func code(err error) ops.Code {
	var oe *ops.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return ""
}

const (
	claudeTarget = "/opt/xbot/cred/claude-code/.credentials.json"
	claudeLink   = "/home/box/.claude/.credentials.json"
)

func TestLinkAndUnlinkClaude(t *testing.T) {
	e := setup(t)
	e.hostCred(t, filepath.Join(e.home, ".claude"), ".credentials.json", `{"claudeAiOauth":{"accessToken":"`+fakeToken+`"}}`)
	b := e.bot(t, "claude-code")

	if err := e.svc.Authorize(bg, b.ID, "claude-code"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("還沒同意時應該拒絕：%v", err)
	}
	raw, err := e.call(owner, "xbot.engines.link_credentials", map[string]any{"bot_id": b.ID, "engine": "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	var v BotStatus
	_ = json.Unmarshal(raw, &v)
	if !v.Linked || v.Marker != "這個 Bot 用你的 Claude 訂閱" || v.LinkedEngine != "claude-code" || v.LinkedAt == 0 {
		t.Fatalf("連結後：%s", raw)
	}
	if strings.Contains(string(raw), fakeToken) {
		t.Fatal("輸出不能有憑證")
	}
	// 憑證在容器檔案層：0400、Bot 擁有；資料夾屬於 root；家目錄裡是連結。
	cred := e.files.get(claudeTarget)
	if !strings.Contains(cred.data, fakeToken) || cred.mode != 0o400 || cred.uid != 1000 || cred.gid != 1000 {
		t.Fatalf("容器裡的憑證：%+v", cred)
	}
	if d := e.files.get("/opt/xbot/cred/claude-code"); d.typ != tar.TypeDir || d.uid != 0 {
		t.Fatalf("憑證資料夾要屬於 root：%+v", d)
	}
	if l := e.files.get(claudeLink); l.typ != tar.TypeSymlink || l.link != claudeTarget || l.uid != 1000 {
		t.Fatalf("家目錄的連結：%+v", l)
	}
	if err := e.svc.Authorize(bg, b.ID, "claude-code"); err != nil {
		t.Fatalf("同意後應該可以：%v", err)
	}
	if err := e.svc.Authorize(bg, b.ID, "codex"); err == nil {
		t.Fatal("同意的是 Claude，不能拿來用 Codex")
	}

	// 狀態：不含憑證，看得到這台電腦上有沒有登入。
	raw, err = e.call(owner, "xbot.engines.status", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var st StatusOut
	_ = json.Unmarshal(raw, &st)
	if len(st.Bots) != 1 || !st.Bots[0].Linked || len(st.Host) != len(acp.Engines) || !st.Host[0].LoggedIn || st.Host[1].LoggedIn || strings.Contains(string(raw), fakeToken) {
		t.Fatalf("狀態：%s", raw)
	}
	// 設定表與稽核也不能有憑證。
	settings, _ := e.db.ListSettings(bg)
	audit, _ := e.db.ListAudit(bg, store.AuditFilter{})
	all, _ := json.Marshal(map[string]any{"s": settings, "a": audit})
	if strings.Contains(string(all), fakeToken) {
		t.Fatal("憑證外流到設定表或稽核")
	}

	// 收回：驅動拒絕、結束 agent 行程、清掉容器裡的憑證、刪掉同意紀錄。
	raw, err = e.call(owner, "xbot.engines.unlink_credentials", map[string]any{"bot_id": b.ID})
	if err != nil {
		t.Fatal(err)
	}
	v = BotStatus{}
	_ = json.Unmarshal(raw, &v)
	if v.Linked || v.Marker != "" || v.Pending {
		t.Fatalf("收回後：%s", raw)
	}
	if err := e.svc.Authorize(bg, b.ID, "claude-code"); !errors.Is(err, ErrNotLinked) || !strings.Contains(err.Error(), "Claude") {
		t.Fatalf("收回後應該拒絕：%v", err)
	}
	if len(e.closed) != 1 || e.closed[0] != b.ID {
		t.Fatalf("應該結束 agent 行程：%v", e.closed)
	}
	if c := e.files.get(claudeTarget); strings.Contains(c.data, fakeToken) || strings.TrimSpace(c.data) != "{}" {
		t.Fatalf("容器裡的憑證沒清掉：%+v", c)
	}
	if _, ok, _ := e.svc.Get(bg, b.ID); ok {
		t.Fatal("同意紀錄應該刪掉")
	}
}

func TestLinkRules(t *testing.T) {
	e := setup(t)
	hb := e.bot(t, "hermes")
	cb := e.bot(t, "claude-code")
	// EN-2a：Hermes 的 Bot 不能連結 Claude 訂閱。
	if _, err := e.call(owner, "xbot.engines.link_credentials", map[string]any{"bot_id": hb.ID, "engine": "claude-code"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("Hermes 的 Bot：%v", err)
	}
	// 這台電腦上沒有登入。
	if _, err := e.call(owner, "xbot.engines.link_credentials", map[string]any{"bot_id": cb.ID, "engine": "claude-code"}); code(err) != ops.CodeConflict || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("沒有登入：%v", err)
	}
	e.hostCred(t, filepath.Join(e.home, ".claude"), ".credentials.json", fakeToken)
	// 只有本人：有 admin 的代理也不行；Bot 自己更不行。
	agent := ops.Identity{Kind: ops.KindAgent, ID: "a", Scopes: []ops.Scope{ops.ScopeAdmin, ops.ScopeRead}}
	if _, err := e.call(agent, "xbot.engines.link_credentials", map[string]any{"bot_id": cb.ID, "engine": "claude-code"}); code(err) != ops.CodeForbidden {
		t.Fatalf("連結 代理：%v", err)
	}
	if _, err := e.call(agent, "xbot.engines.unlink_credentials", map[string]any{"bot_id": cb.ID}); code(err) != ops.CodeForbidden {
		t.Fatalf("收回 代理：%v", err)
	}
	// 預演不寫。
	if _, err := e.call(owner, "xbot.engines.link_credentials", map[string]any{"bot_id": cb.ID, "engine": "claude-code"}, ops.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.svc.Get(bg, cb.ID); ok || len(e.files.files) != 0 {
		t.Fatal("預演不應該連結")
	}
	// 代理可以看狀態（read）。
	reader := ops.Identity{Kind: ops.KindAgent, ID: "r", Scopes: []ops.Scope{ops.ScopeRead}}
	if _, err := e.call(reader, "xbot.engines.status", map[string]any{"bot_id": cb.ID}); err != nil {
		t.Fatalf("read 可以看狀態：%v", err)
	}
	if _, err := e.call(owner, "xbot.engines.link_credentials", map[string]any{"bot_id": cb.ID, "engine": "gemini"}); code(err) != ops.CodeInvalidInput {
		t.Fatalf("不支援的引擎：%v", err)
	}
}

func TestCodexHomeAndNoBoxYet(t *testing.T) {
	e := setup(t)
	codexHome := filepath.Join(t.TempDir(), "codex")
	e.envVars["CODEX_HOME"] = codexHome
	e.hostCred(t, codexHome, "auth.json", `{"tokens":{"access_token":"`+fakeToken+`"}}`)
	e.noBox = true
	b := e.bot(t, "codex")
	raw, err := e.call(owner, "xbot.engines.link_credentials", map[string]any{"bot_id": b.ID, "engine": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "這個 Bot 用你的 ChatGPT 訂閱") {
		t.Fatalf("%s", raw)
	}
	if len(e.files.files) != 0 {
		t.Fatal("還沒有電腦時不寫")
	}
	// 開機前放進去。
	if err := e.svc.Prepare(bg, Container{API: e.files, ID: "c1"}, b); err != nil {
		t.Fatal(err)
	}
	if c := e.files.get("/opt/xbot/cred/codex/auth.json"); !strings.Contains(c.data, fakeToken) || c.mode != 0o400 {
		t.Fatalf("%+v", c)
	}
	if l := e.files.get("/home/box/.codex/auth.json"); l.link != "/opt/xbot/cred/codex/auth.json" {
		t.Fatalf("%+v", l)
	}
}

func TestPrepare(t *testing.T) {
	e := setup(t)
	dir := filepath.Join(e.home, ".claude")
	e.hostCred(t, dir, ".credentials.json", "v1-"+fakeToken)
	b := e.bot(t, "claude-code")
	if _, err := e.svc.Link(bg, b.ID, "claude-code"); err != nil {
		t.Fatal(err)
	}
	c := Container{API: e.files, ID: "c1"}
	// 每次開機前換成這台電腦上最新的憑證。
	e.hostCred(t, dir, ".credentials.json", "v2-"+fakeToken)
	if err := e.svc.Prepare(bg, c, b); err != nil {
		t.Fatal(err)
	}
	if got := e.files.get(claudeTarget).data; got != "v2-"+fakeToken {
		t.Fatalf("開機前應該更新：%q", got)
	}
	// 引擎改回 Hermes：憑證清掉（EN-2a），同意紀錄留著但不算連結。
	b.Engine = "hermes"
	if err := e.svc.Prepare(bg, c, b); err != nil {
		t.Fatal(err)
	}
	if got := e.files.get(claudeTarget).data; strings.Contains(got, fakeToken) {
		t.Fatal("換成 Hermes 後不能留著 Claude 憑證")
	}
	v, _ := e.svc.view(bg, b)
	if v.Linked || v.LinkedEngine != "claude-code" {
		t.Fatalf("%+v", v)
	}
	// 這台電腦上登出了：開機不擋，舊的清掉。
	b.Engine = "claude-code"
	_ = os.Remove(filepath.Join(dir, ".credentials.json"))
	if err := e.svc.Prepare(bg, c, b); err != nil {
		t.Fatal(err)
	}
	if got := e.files.get(claudeTarget).data; strings.Contains(got, fakeToken) {
		t.Fatal("登出後應該清掉")
	}
}

func TestUnlinkWhileHostOffline(t *testing.T) {
	e := setup(t)
	e.hostCred(t, filepath.Join(e.home, ".claude"), ".credentials.json", fakeToken)
	b := e.bot(t, "claude-code")
	if _, err := e.svc.Link(bg, b.ID, "claude-code"); err != nil {
		t.Fatal(err)
	}
	e.locErr = errors.New("主機離線")
	raw, err := e.call(owner, "xbot.engines.unlink_credentials", map[string]any{"bot_id": b.ID})
	if err != nil {
		t.Fatal(err)
	}
	var v BotStatus
	_ = json.Unmarshal(raw, &v)
	if v.Linked || !v.Pending {
		t.Fatalf("離線時收回：%s", raw)
	}
	if err := e.svc.Authorize(bg, b.ID, "claude-code"); err == nil {
		t.Fatal("收回後立刻拒絕，不等清除")
	}
	// 下次開機前清掉，並刪掉紀錄。
	if err := e.svc.Prepare(bg, Container{API: e.files, ID: "c1"}, b); err != nil {
		t.Fatal(err)
	}
	if got := e.files.get(claudeTarget).data; strings.Contains(got, fakeToken) {
		t.Fatal("開機前應該清掉")
	}
	if _, ok, _ := e.svc.Get(bg, b.ID); ok {
		t.Fatal("清掉後刪掉紀錄")
	}
}

// 訂閱 CLI 回報的模型按引擎記下來，engines.status 帶給用這個引擎的 Bot。
func TestRememberModels(t *testing.T) {
	e := setup(t)
	b := e.bot(t, "opencode")
	read := func() BotStatus {
		raw, err := e.call(owner, "xbot.engines.status", map[string]any{"bot_id": b.ID})
		if err != nil {
			t.Fatal(err)
		}
		var st StatusOut
		_ = json.Unmarshal(raw, &st)
		return st.Bots[0]
	}
	if v := read(); len(v.Models) != 0 || v.Plan != "OpenCode Go" {
		t.Fatalf("還沒用過：%+v", v)
	}
	e.svc.RememberModels("opencode", "opencode/big-pickle", []acp.ModelChoice{{Value: "opencode/big-pickle"}, {Value: "opencode-go/deepseek-v4-flash", Name: "DeepSeek"}})
	if v := read(); len(v.Models) != 2 || v.AgentDefault != "opencode/big-pickle" || v.Models[1].Name != "DeepSeek" {
		t.Fatalf("記下後：%+v", v)
	}
}
