package engines

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xbot/internal/engine/acp"
	"xbot/internal/engine/acp/acptest"
	"xbot/internal/engine/hermes"
	"xbot/internal/ops"
)

// fakeLogin 是 Bot 電腦裡的登入指令：測試寫它的輸出，讀它收到的 stdin。
type fakeLogin struct {
	r *io.PipeReader
	w *io.PipeWriter

	mu     sync.Mutex
	stdin  strings.Builder
	closed bool
}

func newFakeLogin() *fakeLogin {
	r, w := io.Pipe()
	return &fakeLogin{r: r, w: w}
}

func (p *fakeLogin) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *fakeLogin) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdin.Write(b)
}
func (p *fakeLogin) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.w.Close()
	return p.r.Close()
}
func (p *fakeLogin) say(lines ...string) {
	go func() {
		for _, l := range lines {
			_, _ = io.WriteString(p.w, l+"\n")
		}
	}()
}

// execRec 記下 Exec 開過的行程。
type execRec struct {
	mu    sync.Mutex
	homes []string
	cmds  [][]string
}

// withLogin 接上假的 Exec：登入指令回 fl；pkill、true 直接結束；其他（確認登入）是假的 ACP agent。
func (e *env) withLogin(fl *fakeLogin, agent *acptest.Agent) *execRec {
	rec := &execRec{}
	e.svc.Exec = func(_ context.Context, _ string, home string, a acp.Agent) (acp.Process, error) {
		rec.mu.Lock()
		rec.homes = append(rec.homes, home)
		rec.cmds = append(rec.cmds, a.Command)
		rec.mu.Unlock()
		switch a.Command[0] {
		case "sh":
			return fl, nil
		case "pkill", "true":
			done := newFakeLogin()
			_ = done.w.Close()
			return done, nil
		}
		return agent.Start(), nil
	}
	e.svc.Launch = func(context.Context, string, acp.Agent) (acp.Process, error) { return agent.Start(), nil }
	return rec
}

func (e *env) waitLogin(t *testing.T, botID, state string) LoginView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, _ := e.svc.LoginStatus(botID)
		if v.State == state {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("登入一直不是 %s：%+v", state, v)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const (
	codexOwn  = "/opt/xbot/cred/own/codex/.codex/auth.json"
	codexHome = "/home/box/.codex/auth.json"
)

func TestOwnLoginCodex(t *testing.T) {
	e := setup(t)
	// 原本用這台電腦的登入；自己登入後就不用了。
	e.hostCred(t, filepath.Join(e.home, ".codex"), "auth.json", `{"tokens":{"access_token":"HOST-`+fakeToken+`"}}`)
	b := e.bot(t, acp.EngineCodex)
	if _, err := e.call(owner, "xbot.engines.link_credentials", LinkIn{BotID: b.ID, Engine: acp.EngineCodex}); err != nil {
		t.Fatal(err)
	}
	fl := newFakeLogin()
	rec := e.withLogin(fl, acptest.NewFor(acp.EngineCodex))
	fl.say("Follow these steps to sign in with ChatGPT using device code authorization:",
		"1. Open this link in your browser and sign in to your account",
		"   \x1b[94mhttps://auth.openai.com/codex/device\x1b[0m",
		"2. Enter this one-time code \x1b[90m(expires in 15 minutes)\x1b[0m",
		"   \x1b[94m78EM-T8MY9\x1b[0m")

	if _, err := e.call(ops.Bot(b.ID, b.Name), "xbot.engines.login_start", LoginStartIn{BotID: b.ID, Engine: acp.EngineCodex}); code(err) != "forbidden" {
		t.Fatalf("Bot 不能自己登入：%v", err)
	}
	raw, err := e.call(owner, "xbot.engines.login_start", LoginStartIn{BotID: b.ID, Engine: acp.EngineCodex})
	if err != nil {
		t.Fatal(err)
	}
	var v LoginView
	_ = json.Unmarshal(raw, &v)
	if v.State != LoginWaiting || v.URL != "https://auth.openai.com/codex/device" || v.Code != "78EM-T8MY9" || v.NeedsCode {
		t.Fatalf("登入開始：%s", raw)
	}
	if rec.homes[0] != "/opt/xbot/cred/own/codex" || !strings.Contains(strings.Join(rec.cmds[0], " "), "login --device-auth") {
		t.Fatalf("登入指令：%v %v", rec.homes, rec.cmds)
	}

	// 使用者在網頁上登入好了：CLI 寫下登入資料、結束。
	e.files.mu.Lock()
	e.files.files[codexOwn] = entry{typ: tar.TypeReg, mode: 0o600, data: `{"tokens":{"access_token":"` + fakeToken + `"}}`}
	e.files.mu.Unlock()
	fl.say(exitMarker + "0")
	v = e.waitLogin(t, b.ID, LoginDone)
	if v.Message != "已登入" || v.URL != "" {
		t.Fatalf("登入完：%+v", v)
	}
	// Bot 正在用 Codex：搬進家目錄（Bot 擁有、可寫），own 資料夾清空；原本用這台電腦的那份不再用。
	if h := e.files.get(codexHome); h.typ != tar.TypeReg || h.uid != 1000 || h.mode != 0o600 || !strings.Contains(h.data, fakeToken) || strings.Contains(h.data, "HOST-") {
		t.Fatalf("家目錄：%+v", h)
	}
	if o := e.files.get(codexOwn); hasCred([]byte(o.data)) || o.uid != 0 {
		t.Fatalf("own 資料夾應該清空、屬於 root：%+v", o)
	}
	if _, ok, _ := e.svc.Get(bg, b.ID); ok {
		t.Fatal("自己登入後不該再用這台電腦的登入")
	}
	if c := e.files.get("/opt/xbot/cred/codex/auth.json"); hasCred([]byte(c.data)) {
		t.Fatalf("這台電腦複製來的那份要清掉：%+v", c)
	}
	if err := e.svc.Authorize(bg, b.ID, acp.EngineCodex); err != nil {
		t.Fatal(err)
	}
	raw, _ = e.call(owner, "xbot.engines.status", StatusIn{BotID: b.ID})
	var st StatusOut
	_ = json.Unmarshal(raw, &st)
	var acc Account
	for _, a := range st.Bots[0].Accounts {
		if a.Engine == acp.EngineCodex {
			acc = a
		}
	}
	if !st.Bots[0].Linked || st.Bots[0].Source != "bot" || !acc.Ready || acc.Source != "bot" || acc.Login != LoginWeb || len(acc.Models) == 0 {
		t.Fatalf("狀態：%s", raw)
	}
	if strings.Contains(string(raw), fakeToken) {
		t.Fatal("狀態不能有憑證內容")
	}

	// 換成 Hermes：憑證收回 own 資料夾，家目錄清空（EN-2a）。
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: hermes.Name}); err != nil {
		t.Fatal(err)
	}
	if h := e.files.get(codexHome); hasCred([]byte(h.data)) {
		t.Fatalf("換成 Hermes 後家目錄還有憑證：%+v", h)
	}
	if o := e.files.get(codexOwn); !strings.Contains(o.data, fakeToken) || o.uid != 0 || o.mode != 0o600 {
		t.Fatalf("own 資料夾：%+v", o)
	}
	// 換回來不用再登入。
	if _, err := e.call(owner, "xbot.engines.switch", SwitchIn{BotID: b.ID, Engine: acp.EngineCodex}); err != nil {
		t.Fatal(err)
	}
	if h := e.files.get(codexHome); !strings.Contains(h.data, fakeToken) {
		t.Fatalf("換回 Codex 後家目錄：%+v", h)
	}

	// 登出：兩邊都清掉，驅動拒絕。
	if _, err := e.call(owner, "xbot.engines.logout", LogoutIn{BotID: b.ID, Engine: acp.EngineCodex}); err != nil {
		t.Fatal(err)
	}
	if h, o := e.files.get(codexHome), e.files.get(codexOwn); hasCred([]byte(h.data)) || hasCred([]byte(o.data)) {
		t.Fatalf("登出後：%+v %+v", h, o)
	}
	if err := e.svc.Authorize(bg, b.ID, acp.EngineCodex); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("登出後應該拒絕：%v", err)
	}
}

func TestOwnLoginClaudePasteCode(t *testing.T) {
	e := setup(t)
	b := e.bot(t, hermes.Name)
	fl := newFakeLogin()
	e.withLogin(fl, acptest.New())
	fl.say("Opening browser to sign in…",
		"If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=y",
		"Paste code here if prompted >")
	raw, err := e.call(owner, "xbot.engines.login_start", LoginStartIn{BotID: b.ID, Engine: acp.EngineClaude})
	if err != nil {
		t.Fatal(err)
	}
	var v LoginView
	_ = json.Unmarshal(raw, &v)
	if !v.NeedsCode || !strings.HasPrefix(v.URL, "https://claude.com/cai/oauth/authorize?") {
		t.Fatalf("Claude 要貼授權碼：%s", raw)
	}
	if _, err := e.call(owner, "xbot.engines.login_code", LoginCodeIn{BotID: b.ID, Code: "abc\ndef"}); code(err) != "invalid_input" {
		t.Fatalf("授權碼不能換行：%v", err)
	}
	if _, err := e.call(owner, "xbot.engines.login_code", LoginCodeIn{BotID: b.ID, Code: " AUTH-CODE#state "}); err != nil {
		t.Fatal(err)
	}
	fl.mu.Lock()
	got := fl.stdin.String()
	fl.mu.Unlock()
	if got != "AUTH-CODE#state\n" {
		t.Fatalf("交給 CLI 的授權碼：%q", got)
	}
	// Bot 用的是 Hermes：登入資料留在 own 資料夾，不放進家目錄（EN-2a）。
	e.files.mu.Lock()
	e.files.files["/opt/xbot/cred/own/claude-code/.claude/.credentials.json"] = entry{typ: tar.TypeReg, mode: 0o600, data: `{"claudeAiOauth":{"accessToken":"` + fakeToken + `"}}`}
	e.files.mu.Unlock()
	fl.say("Login successful", exitMarker+"0")
	e.waitLogin(t, b.ID, LoginDone)
	if h := e.files.get(claudeLink); h.data != "" {
		t.Fatalf("Hermes 的 Bot 家目錄不能有 Claude 的登入：%+v", h)
	}
	if ok, _ := e.svc.HasOwn(bg, b.ID, acp.EngineClaude); !ok {
		t.Fatal("應該記下自己登入了")
	}
}

func TestOwnLoginFailsAndCancel(t *testing.T) {
	e := setup(t)
	b := e.bot(t, acp.EngineGrok)
	fl := newFakeLogin()
	e.withLogin(fl, acptest.NewFor(acp.EngineGrok))
	fl.say("To sign in, open this URL in your browser:", "  https://accounts.x.ai/oauth2/device?user_code=8WHR-XMDR",
		"Confirm this code in your browser:", "  8WHR-XMDR")
	if _, err := e.call(owner, "xbot.engines.login_start", LoginStartIn{BotID: b.ID, Engine: acp.EngineGrok}); err != nil {
		t.Fatal(err)
	}
	fl.say("Error: device code expired", exitMarker+"1")
	if v := e.waitLogin(t, b.ID, LoginFailed); v.Message != "登入沒有完成：Error: device code expired" {
		t.Fatalf("%+v", v)
	}
	if ok, _ := e.svc.HasOwn(bg, b.ID, acp.EngineGrok); ok {
		t.Fatal("失敗了不能記成已登入")
	}

	fl2 := newFakeLogin()
	rec := e.withLogin(fl2, acptest.NewFor(acp.EngineGrok))
	fl2.say("  https://accounts.x.ai/oauth2/device?user_code=AAAA-BBBB", "  AAAA-BBBB")
	if _, err := e.call(owner, "xbot.engines.login_start", LoginStartIn{BotID: b.ID, Engine: acp.EngineGrok}); err != nil {
		t.Fatal(err)
	}
	raw, err := e.call(owner, "xbot.engines.login_cancel", LoginBotIn{BotID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cancelled"`) {
		t.Fatalf("%s", raw)
	}
	rec.mu.Lock()
	last := strings.Join(rec.cmds[len(rec.cmds)-1], " ")
	rec.mu.Unlock()
	if last != "pkill -f grok login" {
		t.Fatalf("取消時要結束電腦裡的登入指令：%q", last)
	}
}

func TestLoginKeyOpenCode(t *testing.T) {
	e := setup(t)
	b := e.bot(t, acp.EngineOpenCode)
	e.withLogin(newFakeLogin(), acptest.NewFor(acp.EngineOpenCode))
	if _, err := e.call(owner, "xbot.engines.login_key", LoginKeyIn{BotID: b.ID}); code(err) != "invalid_input" {
		t.Fatalf("沒有金鑰：%v", err)
	}
	raw, err := e.call(owner, "xbot.engines.login_key", LoginKeyIn{BotID: b.ID, GoKey: "sk-go-" + fakeToken})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), fakeToken) {
		t.Fatal("回應不能有金鑰")
	}
	h := e.files.get("/home/box/.local/share/opencode/auth.json")
	var auth map[string]map[string]string
	if err := json.Unmarshal([]byte(h.data), &auth); err != nil || auth["opencode-go"]["key"] != "sk-go-"+fakeToken || auth["opencode-go"]["type"] != "api" || h.uid != 1000 {
		t.Fatalf("OpenCode 的 auth.json：%+v", h)
	}
	if _, ok := auth["opencode"]; ok {
		t.Fatal("沒給 Zen 的金鑰就不寫")
	}
}

// 開機前（Prepare）照引擎搬，已登出、還沒清掉的清掉。
func TestPrepareOwn(t *testing.T) {
	e := setup(t)
	b := e.bot(t, hermes.Name)
	if err := e.svc.saveOwns(bg, b.ID, map[string]Own{acp.EngineCodex: {At: 1}, acp.EngineGrok: {At: 1, Revoked: true}}); err != nil {
		t.Fatal(err)
	}
	e.files.files[codexHome] = entry{typ: tar.TypeReg, mode: 0o600, uid: 1000, data: `{"tokens":"` + fakeToken + `"}`}
	e.files.files["/opt/xbot/cred/own/grok/.grok/auth.json"] = entry{typ: tar.TypeReg, mode: 0o600, data: `{"k":"` + fakeToken + `"}`}
	c := Container{API: e.files, ID: "c1"}
	if err := e.svc.Prepare(bg, c, b); err != nil {
		t.Fatal(err)
	}
	if h, o := e.files.get(codexHome), e.files.get(codexOwn); hasCred([]byte(h.data)) || !strings.Contains(o.data, fakeToken) {
		t.Fatalf("Hermes 的 Bot：Codex 的登入要收起來：%+v %+v", h, o)
	}
	if g := e.files.get("/opt/xbot/cred/own/grok/.grok/auth.json"); hasCred([]byte(g.data)) {
		t.Fatalf("登出的要清掉：%+v", g)
	}
	m, _ := e.svc.owns(bg, b.ID)
	if _, ok := m[acp.EngineGrok]; ok || len(m) != 1 {
		t.Fatalf("%+v", m)
	}
}
