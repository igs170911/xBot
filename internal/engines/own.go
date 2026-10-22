package engines

// 每個 Bot 自己登入訂閱 CLI（2026-10-20，取代「把這台電腦的登入複製進去」當主要的方式）。
//
// 為什麼：原本只把這台電腦上的 CLI 憑證唯讀複製進 Bot 的電腦，實測（2026-10-20）有三個問題：
//   - 權杖過期時 CLI 寫不回換新的權杖（唯讀），Codex 一直說「登入過期」；Grok 換新時把連結換成檔案，下一次就找不到登入。
//   - 這台電腦和每個 Bot 共用同一組會輪換的 refresh token：誰先換新，其他人的就失效（連這台電腦的 CLI 都可能被登出）。
//   - 一定要先在這台電腦的終端機登入，手機上做不到。
//
// 現在每個 Bot 在自己的電腦裡登入，各自一組權杖：
//   - 登入（xbot.engines.login_start）以 root 在 Bot 的電腦裡跑 CLI 自己的登入指令，HOME 是 /opt/xbot/cred/own/<引擎>
//     （只有 root 讀得到，Bot 正在用別的引擎時也拿不到，EN-2a）。介面顯示 CLI 給的網址與代碼；Claude 要把網頁給的授權碼貼回來。
//   - Bot 的引擎是這個 CLI 時，把憑證「搬」進家目錄（Bot 擁有、可寫：CLI 自己換新權杖）；換成別的引擎時搬回 own 資料夾、家目錄清空。
//     同一份憑證只放一個地方，不留舊的那份（輪換過的 refresh token 再拿來用，可能整組被撤銷）。
//   - 登出（xbot.engines.logout）兩邊都清掉。OpenCode 用金鑰（xbot.engines.login_key），寫成它的 auth.json。
//
// 登入完開一次 agent（initialize＋session/new，不送訊息）確認登入有效，順便記下這個帳號能用的模型。

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"xbot/internal/engine"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// Own 是 Bot 自己登入的一個訂閱 CLI（setting：engine.own.<botID>，引擎 → Own）。
type Own struct {
	At int64 `json:"at"`
	// Revoked：已登出，但 Bot 電腦裡的憑證還沒確定清掉（電腦連不上），下次開機前清。
	Revoked bool `json:"revoked,omitempty"`
}

func ownKey(botID string) string { return "engine.own." + botID }

// ownDir 是 Bot 自己登入的憑證不用時放的地方（只有 root 讀得到）；登入時是 CLI 的 HOME。
func ownDir(engine string) string { return path.Join(CredDir, "own", engine) }

func (s Spec) ownPath() string  { return path.Join(ownDir(s.Engine), s.Dir, s.File) }
func (s Spec) homePath() string { return path.Join(BoxHome, s.Dir, s.File) }

// 怎麼登入一個 CLI（LoginKind）。
const (
	// LoginWeb：到網頁輸入 CLI 給的代碼（裝置碼登入：Codex、Grok）。
	LoginWeb = "web"
	// LoginCode：到網頁登入，再把網頁給的授權碼貼回來（Claude Code）。
	LoginCode = "code"
	// LoginKey：貼上金鑰（OpenCode）。
	LoginKey = "key"
	// LoginHost：只能用這台電腦上的登入（Gemini CLI 的登入要終端機，網頁上做不到）。
	LoginHost = "host"
)

// LoginKind 回傳一個引擎怎麼登入。
func LoginKind(engine string) string {
	switch engine {
	case acp.EngineClaude:
		return LoginCode
	case acp.EngineCodex, acp.EngineGrok:
		return LoginWeb
	case acp.EngineOpenCode:
		return LoginKey
	}
	return LoginHost
}

// loginCmd 是一個 CLI 在 Bot 電腦裡的登入指令（sh；$HOME 是 own 資料夾）。
type loginCmd struct {
	Script string
	// Kill 是取消時結束登入指令用的 pkill -f 樣式。
	Kill string
}

// loginPrelude：映像裡沒有另外裝 claude、codex，用 ACP 套件裡附的執行檔（找不到時用 PATH 上的）。
const loginPrelude = `umask 077; mkdir -p "$HOME"; chmod 700 "$HOME"; cd "$HOME" || exit 1
xbot_bin() { for p in /usr/local/lib/node_modules/@agentclientprotocol/*/node_modules/$1; do [ -x "$p" ] && { echo "$p"; return; }; done; echo "$2"; }
`

// exitMarker 讓 XBot 知道登入指令結束了、結束碼是多少（不配 TTY 的 docker exec 串流裡沒有結束碼）。
const exitMarker = "__XBOT_LOGIN_EXIT="

var loginCmds = map[string]loginCmd{
	acp.EngineClaude: {Script: `"$(xbot_bin '@anthropic-ai/claude-agent-sdk-linux-*/claude' claude)" auth login --claudeai`, Kill: "auth login --claudeai"},
	acp.EngineCodex:  {Script: `"$(xbot_bin '@openai/codex-linux-*/vendor/*/bin/codex' codex)" login --device-auth`, Kill: "login --device-auth"},
	acp.EngineGrok:   {Script: `grok login --device-auth`, Kill: "grok login"},
}

// loginEnv：不開瀏覽器（Bot 電腦的桌面上不要跳出登入頁）、不要顏色碼。
var loginEnv = []string{"BROWSER=/bin/true", "DISPLAY=", "NO_COLOR=1", "TERM=dumb"}

// emptyCred 是清掉以後的憑證內容（CLI 讀到會當成沒有登入）。
var emptyCred = []byte("{}\n")

func hasCred(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && !bytes.Equal(t, []byte("{}"))
}

// credFile 是容器裡一個憑證檔的樣子。
type credFile struct {
	data []byte
	// link：是連結（用這台電腦的登入時，家目錄裡是指向 /opt/xbot/cred/<引擎>/ 的連結）。
	link bool
}

func (f credFile) filled() bool { return !f.link && hasCred(f.data) }

// readCred 讀容器裡的一個憑證檔（沒開機也可以）；不存在時是空的。連結不跟過去。
func readCred(ctx context.Context, c Container, p string) (credFile, error) {
	res, err := c.API.CopyFromContainer(ctx, c.ID, client.CopyFromContainerOptions{SourcePath: p})
	if cerrdefs.IsNotFound(err) {
		return credFile{}, nil
	}
	if err != nil {
		return credFile{}, fmt.Errorf("讀不到 Bot 電腦裡的 %s：%w", p, err)
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	h, err := tr.Next()
	if errors.Is(err, io.EOF) {
		return credFile{}, nil
	}
	if err != nil {
		return credFile{}, err
	}
	switch h.Typeflag {
	case tar.TypeSymlink:
		return credFile{link: true}, nil
	case tar.TypeReg:
		data, err := io.ReadAll(io.LimitReader(tr, maxCredBytes+1))
		if err != nil {
			return credFile{}, err
		}
		if len(data) > maxCredBytes {
			return credFile{}, fmt.Errorf("%s 太大，不像憑證檔", p)
		}
		return credFile{data: data}, nil
	}
	return credFile{}, nil
}

// dirHeaders 是 rel 的每一層資料夾（最後一層用 last 的權限）。
func dirHeaders(rel string, uid, gid int, mode, last int64, now time.Time) []*tar.Header {
	var out []*tar.Header
	parts := strings.Split(rel, "/")
	for i := range parts {
		m := mode
		if i == len(parts)-1 {
			m = last
		}
		out = append(out, &tar.Header{Name: strings.Join(parts[:i+1], "/") + "/", Typeflag: tar.TypeDir, Mode: m, Uid: uid, Gid: gid, ModTime: now})
	}
	return out
}

// writeHome 把憑證寫成家目錄裡的一般檔案（Bot 擁有、0600；每一層資料夾都由 Bot 擁有）。
func (s *Service) writeHome(ctx context.Context, c Container, sp Spec, data []byte) error {
	now := time.Now()
	hs := append(dirHeaders(sp.Dir, s.UID, s.GID, 0o755, 0o700, now),
		&tar.Header{Name: path.Join(sp.Dir, sp.File), Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data)), Uid: s.UID, Gid: s.GID, ModTime: now})
	tb, err := tarball(hs, data)
	if err != nil {
		return err
	}
	if _, err := c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: BoxHome, Content: tb}); err != nil {
		return fmt.Errorf("寫入 Bot 電腦的 %s 登入失敗：%w", sp.Label, err)
	}
	return nil
}

// writeOwn 寫進 own 資料夾（root 擁有：資料夾 0700、檔案 0600，Bot 讀不到）。
func (s *Service) writeOwn(ctx context.Context, c Container, sp Spec, data []byte) error {
	now := time.Now()
	hs := append(dirHeaders(path.Join("own", sp.Engine, sp.Dir), 0, 0, 0o700, 0o700, now),
		&tar.Header{Name: path.Join("own", sp.Engine, sp.Dir, sp.File), Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data)), ModTime: now})
	tb, err := tarball(hs, data)
	if err != nil {
		return err
	}
	if _, err := c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: CredDir, Content: tb}); err != nil {
		return fmt.Errorf("收好 Bot 電腦裡的 %s 登入失敗：%w", sp.Label, err)
	}
	return nil
}

// activate 把 Bot 自己登入的憑證搬進家目錄（Bot 現在用這個 CLI）。own 資料夾裡沒有時不動家目錄（CLI 正在用、可能換新過）；
// 有的話是剛登入或剛換回來的，比家目錄裡的新。
func (s *Service) activate(ctx context.Context, c Container, sp Spec) error {
	own, err := readCred(ctx, c, sp.ownPath())
	if err != nil || !own.filled() {
		return err
	}
	if err := s.writeHome(ctx, c, sp, own.data); err != nil {
		return err
	}
	return s.writeOwn(ctx, c, sp, emptyCred)
}

// park 把家目錄裡的憑證搬回 own 資料夾（Bot 換成別的引擎，EN-2a），家目錄清空。連結（用這台電腦的登入）不動。
func (s *Service) park(ctx context.Context, c Container, sp Spec) error {
	home, err := readCred(ctx, c, sp.homePath())
	if err != nil || !home.filled() {
		return err
	}
	if err := s.writeOwn(ctx, c, sp, home.data); err != nil {
		return err
	}
	return s.writeHome(ctx, c, sp, emptyCred)
}

// wipe 清掉 Bot 自己登入的憑證（登出）：own 資料夾與家目錄的一般檔案。連結（用這台電腦的登入）不動。
func (s *Service) wipe(ctx context.Context, c Container, sp Spec) error {
	if err := s.writeOwn(ctx, c, sp, emptyCred); err != nil {
		return err
	}
	home, err := readCred(ctx, c, sp.homePath())
	if err != nil || home.link || len(home.data) == 0 {
		return err
	}
	return s.writeHome(ctx, c, sp, emptyCred)
}

// owns 讀 Bot 自己登入過的訂閱 CLI（含已登出、還沒清掉的）。
func (s *Service) owns(ctx context.Context, botID string) (map[string]Own, error) {
	m := map[string]Own{}
	_, err := s.DB.GetSettingInto(ctx, ownKey(botID), &m)
	if m == nil {
		m = map[string]Own{}
	}
	return m, err
}

func (s *Service) saveOwns(ctx context.Context, botID string, m map[string]Own) error {
	if len(m) == 0 {
		return s.DB.DeleteSetting(ctx, ownKey(botID))
	}
	return s.DB.SetSetting(ctx, ownKey(botID), m)
}

// HasOwn 回答 Bot 有沒有自己登入這個 CLI（沒登出）。
func (s *Service) HasOwn(ctx context.Context, botID, engine string) (bool, error) {
	m, err := s.owns(ctx, botID)
	if err != nil {
		return false, err
	}
	o, ok := m[engine]
	return ok && !o.Revoked, nil
}

func engineOf(b store.Bot) string {
	if b.Engine == "" {
		return hermes.Name
	}
	return b.Engine
}

// Sync 照 Bot 現在的引擎搬 Bot 自己登入的憑證：用哪個 CLI 就把那份放進家目錄，其他的收回 own 資料夾。
// 換引擎（xbot.bots.update、xbot.engines.switch）後馬上做，不等下次開機（Hermes 的 Bot 不能留著別人的憑證，EN-2a）。
// 還沒有電腦時不用做（開機前 Prepare 會做）。
func (s *Service) Sync(ctx context.Context, botID string) error {
	if s.Locate == nil {
		return nil
	}
	b, err := s.DB.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	c, err := s.Locate(ctx, botID)
	if err != nil || c.ID == "" {
		return err
	}
	return s.syncOwn(ctx, c, b)
}

func (s *Service) syncOwn(ctx context.Context, c Container, b store.Bot) error {
	m, err := s.owns(ctx, b.ID)
	if err != nil || len(m) == 0 {
		return err
	}
	cur, changed := engineOf(b), false
	for _, e := range slices.Sorted(maps.Keys(m)) {
		sp, ok := Specs[e]
		if !ok {
			continue
		}
		// Lock each engine's credentials to prevent concurrent refresh conflicts
		err := s.ensureCredLock().withLock(ctx, sp.ownPath(), func() error {
			switch {
			case m[e].Revoked:
				if err := s.wipe(ctx, c, sp); err != nil {
					return err
				}
				delete(m, e)
				changed = true
			case e == cur:
				if err := s.activate(ctx, c, sp); err != nil {
					return err
				}
			default:
				if err := s.park(ctx, c, sp); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if changed {
		return s.saveOwns(ctx, b.ID, m)
	}
	return nil
}

// syncOwnNoLock is like syncOwn but assumes the lock is already held (used by adopt)
func (s *Service) syncOwnNoLock(ctx context.Context, c Container, b store.Bot) error {
	m, err := s.owns(ctx, b.ID)
	if err != nil || len(m) == 0 {
		return err
	}
	cur, changed := engineOf(b), false
	for _, e := range slices.Sorted(maps.Keys(m)) {
		sp, ok := Specs[e]
		if !ok {
			continue
		}
		switch {
		case m[e].Revoked:
			if err := s.wipe(ctx, c, sp); err != nil {
				return err
			}
			delete(m, e)
			changed = true
		case e == cur:
			if err := s.activate(ctx, c, sp); err != nil {
				return err
			}
		default:
			if err := s.park(ctx, c, sp); err != nil {
				return err
			}
		}
	}
	if changed {
		return s.saveOwns(ctx, b.ID, m)
	}
	return nil
}

// Logout 登出 Bot 自己登入的一個 CLI：記成已登出（驅動立刻拒絕）、結束 agent 行程、清掉憑證；
// 電腦連不上時下次開機前清。回傳的 cleared 表示電腦裡已確定沒有這份憑證。
func (s *Service) Logout(ctx context.Context, botID, engine string) (cleared bool, err error) {
	sp := Specs[engine]
	// Lock to prevent concurrent credential refresh conflicts
	return false, s.ensureCredLock().withLock(ctx, sp.ownPath(), func() error {
		cleared, err = s.logoutNoLock(ctx, botID, engine)
		return err
	})
}

// logoutNoLock is the unlocked version of Logout, for use when already holding the lock.
func (s *Service) logoutNoLock(ctx context.Context, botID, engine string) (cleared bool, err error) {
	sp := Specs[engine]
	m, err := s.owns(ctx, botID)
	if err != nil {
		return false, err
	}
	if _, ok := m[engine]; !ok {
		return true, nil
	}
	m[engine] = Own{At: m[engine].At, Revoked: true}
	if err := s.saveOwns(ctx, botID, m); err != nil {
		return false, err
	}
	if s.Close != nil {
		s.Close(botID)
	}
	if s.Locate == nil {
		return false, nil
	}
	c, err := s.Locate(ctx, botID)
	if err != nil {
		s.log().Warn("暫時無法清掉 Bot 電腦裡的登入，開機前再清", "bot", botID, "engine", engine, "err", err)
		return false, nil
	}
	if c.ID != "" {
		if err := s.wipe(ctx, c, sp); err != nil {
			s.log().Warn("暫時無法清掉 Bot 電腦裡的登入，開機前再清", "bot", botID, "engine", engine, "err", err)
			return false, nil
		}
	}
	delete(m, engine)
	return true, s.saveOwns(ctx, botID, m)
}

// adopt 記下 Bot 自己登入了這個 CLI：同一個 CLI 原本用這台電腦的登入就不再用（那份清掉），
// Bot 正在用這個 CLI 時把憑證搬進家目錄、重開 agent。
func (s *Service) adopt(ctx context.Context, c Container, botID, engine string) error {
	sp := Specs[engine]
	// Lock to prevent concurrent credential refresh conflicts
	return s.ensureCredLock().withLock(ctx, sp.ownPath(), func() error {
		return s.adoptNoLock(ctx, c, botID, engine)
	})
}

// adoptNoLock is the unlocked version of adopt, for use when already holding the lock.
func (s *Service) adoptNoLock(ctx context.Context, c Container, botID, engine string) error {
	sp := Specs[engine]
	m, err := s.owns(ctx, botID)
	if err != nil {
		return err
	}
	m[engine] = Own{At: store.NowMS()}
	if err := s.saveOwns(ctx, botID, m); err != nil {
		return err
	}
	if l, ok, err := s.Get(ctx, botID); err != nil {
		return err
	} else if ok && l.Engine == engine {
		if err := s.DB.DeleteSetting(ctx, settingKey(botID)); err != nil {
			return err
		}
		if err := s.clearHostCopy(ctx, c, sp); err != nil {
			s.log().Warn("清掉原本用這台電腦的登入失敗", "bot", botID, "engine", engine, "err", err)
		}
	}
	b, err := s.DB.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	if err := s.syncOwnNoLock(ctx, c, b); err != nil {
		return err
	}
	if engineOf(b) == engine && s.Close != nil {
		s.Close(botID)
	}
	return nil
}

// clearHostCopy 清掉 /opt/xbot/cred/<引擎>/ 裡從這台電腦複製來的那份（家目錄不動）。
func (s *Service) clearHostCopy(ctx context.Context, c Container, sp Spec) error {
	now := time.Now()
	tb, err := tarball([]*tar.Header{
		{Name: sp.Engine + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: now},
		{Name: path.Join(sp.Engine, sp.File), Typeflag: tar.TypeReg, Mode: 0o400, Size: int64(len(emptyCred)), Uid: s.UID, Gid: s.GID, ModTime: now},
	}, emptyCred)
	if err != nil {
		return err
	}
	_, err = c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: CredDir, Content: tb})
	return err
}

// 登入的狀態。
const (
	LoginWaiting   = "waiting"
	LoginChecking  = "checking"
	LoginDone      = "done"
	LoginFailed    = "failed"
	LoginCancelled = "cancelled"
)

// LoginView 是一次登入給介面看的樣子（不含任何憑證）。
type LoginView struct {
	BotID  string `json:"bot_id"`
	Engine string `json:"engine"`
	Label  string `json:"label"`
	State  string `json:"state" desc:"waiting：等你到網頁登入；checking：登入好了，正在確認；done、failed、cancelled"`
	URL    string `json:"url,omitempty" desc:"到這個網址登入"`
	Code   string `json:"code,omitempty" desc:"在網頁上輸入這組代碼"`
	// NeedsCode：網頁登入完會給一段授權碼，要貼回來（xbot.engines.login_code）。
	NeedsCode bool   `json:"needs_code,omitempty" desc:"網頁登入完會給一段授權碼，要用 xbot.engines.login_code 貼回來"`
	Message   string `json:"message,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// login 是一次進行中（或剛結束）的登入。
type login struct {
	botID, engine string
	p             acp.Process
	expires       time.Time
	ready         chan struct{} // 有網址（和代碼）或結束時關上
	readyOnce     sync.Once

	mu       sync.Mutex
	state    string
	url      string
	code     string
	msg      string
	out      []byte
	codeSent bool
}

func (l *login) view() LoginView {
	l.mu.Lock()
	defer l.mu.Unlock()
	sp := Specs[l.engine]
	v := LoginView{BotID: l.botID, Engine: l.engine, Label: sp.Label, State: l.state, Message: l.msg}
	if l.state == LoginWaiting {
		v.URL, v.Code, v.ExpiresAt = l.url, l.code, l.expires.UnixMilli()
		v.NeedsCode = LoginKind(l.engine) == LoginCode && !l.codeSent
	}
	return v
}

func (l *login) markReady() { l.readyOnce.Do(func() { close(l.ready) }) }

// set 改狀態；已經結束（done、failed、cancelled）的不再改。回傳有沒有改。
func (l *login) set(state, msg string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state == LoginDone || l.state == LoginFailed || l.state == LoginCancelled {
		return false
	}
	l.state, l.msg = state, msg
	return true
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)
	urlRe  = regexp.MustCompile(`https://[^\s"'<>]+`)
	// codeRe 是裝置碼（Codex：78EM-T8MY9；Grok：8WHR-XMDR），自己一行。
	codeRe = regexp.MustCompile(`^[A-Z0-9]{4,5}-[A-Z0-9]{4,6}$`)
)

// ErrNoExec 表示這個 XBot 不能在 Bot 的電腦裡開行程（測試或還沒接上）。
var ErrNoExec = errors.New("不能在 Bot 的電腦裡登入")

// StartLogin 在 Bot 的電腦裡開 CLI 的登入，等到它給出網址（和代碼）再回來。同一個 Bot 同時只有一個登入，舊的先取消。
func (s *Service) StartLogin(ctx context.Context, botID, engine string) (LoginView, error) {
	cmd, ok := loginCmds[engine]
	if !ok {
		return LoginView{}, fmt.Errorf("%s 不能在網頁上登入", label(engine))
	}
	if s.Exec == nil {
		return LoginView{}, ErrNoExec
	}
	s.CancelLogin(ctx, botID)
	sp := Specs[engine]
	script := loginPrelude + `rm -f "$HOME/` + path.Join(sp.Dir, sp.File) + "\"\n" + cmd.Script + ` 2>&1; echo "` + exitMarker + `$?"`
	p, err := s.Exec(ctx, botID, ownDir(engine), acp.Agent{Label: sp.Label + " 登入", Command: []string{"sh", "-c", script}, Env: loginEnv})
	if err != nil {
		return LoginView{}, err
	}
	l := &login{botID: botID, engine: engine, p: p, state: LoginWaiting, expires: time.Now().Add(limits.EngineLoginTTL), ready: make(chan struct{})}
	s.loginMu.Lock()
	if s.logins == nil {
		s.logins = map[string]*login{}
	}
	s.logins[botID] = l
	s.loginMu.Unlock()
	go s.watchLogin(l)
	go func() {
		t := time.NewTimer(limits.EngineLoginTTL)
		defer t.Stop()
		<-t.C
		if l.set(LoginFailed, "登入逾時了，請重新開始") {
			s.stopLogin(l)
		}
	}()
	select {
	case <-l.ready:
	case <-time.After(limits.EngineLoginURLWait):
		if l.set(LoginFailed, sp.Label+" 一直沒有給登入網址") {
			s.stopLogin(l)
		}
	case <-ctx.Done():
		return l.view(), nil
	}
	return l.view(), nil
}

// watchLogin 讀登入指令的輸出：找網址與代碼，結束時看結束碼。
func (s *Service) watchLogin(l *login) {
	defer l.markReady()
	defer l.p.Close()
	kind := LoginKind(l.engine)
	r := bufio.NewReaderSize(l.p, 64<<10)
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(ansiRe.ReplaceAllString(strings.ReplaceAll(line, "\r", "\n"), ""))
		if rest, ok := strings.CutPrefix(line, exitMarker); ok {
			code, _ := strconv.Atoi(strings.TrimSpace(rest))
			s.finishLogin(l, code)
			return
		}
		if line != "" {
			l.mu.Lock()
			if len(l.out) < limits.EngineLoginOutputBytes {
				l.out = append(l.out, line+"\n"...)
			}
			if l.url == "" {
				l.url = urlRe.FindString(line)
			}
			if l.code == "" && codeRe.MatchString(line) {
				l.code = line
			}
			ok := l.url != "" && (kind == LoginCode || l.code != "")
			l.mu.Unlock()
			if ok {
				l.markReady()
			}
		}
		if err != nil {
			if l.set(LoginFailed, "登入中斷了，請重新開始") {
				s.log().Warn("登入指令沒有結束碼就停了", "bot", l.botID, "engine", l.engine, "err", err)
			}
			return
		}
	}
}

// finishLogin：結束碼 0 時確認 own 資料夾裡真的有憑證，記下 Bot 自己登入，再開一次 agent 確認、記下模型清單。
func (s *Service) finishLogin(l *login, exit int) {
	sp := Specs[l.engine]
	if exit != 0 {
		l.set(LoginFailed, "登入沒有完成："+lastLine(l))
		return
	}
	if !l.set(LoginChecking, "") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), limits.ACPInitTimeout+limits.ACPSessionTimeout)
	defer cancel()
	c, err := s.Locate(ctx, l.botID)
	if err == nil && c.ID == "" {
		err = errors.New("找不到 Bot 的電腦")
	}
	var f credFile
	if err == nil {
		f, err = readCred(ctx, c, sp.ownPath())
	}
	if err == nil && !f.filled() {
		err = fmt.Errorf("%s 說登入好了，但沒有留下登入資料", sp.Label)
	}
	if err == nil {
		err = s.adopt(ctx, c, l.botID, l.engine)
	}
	if err != nil {
		l.set(LoginFailed, err.Error())
		return
	}
	if err := s.Probe(ctx, l.botID, l.engine); err != nil {
		l.set(LoginDone, "已登入，但確認時出了問題："+err.Error())
		return
	}
	l.set(LoginDone, "已登入")
}

func lastLine(l *login) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(l.out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" && !urlRe.MatchString(t) {
			return t
		}
	}
	return "沒有說明"
}

// stopLogin 結束登入指令：關掉連線，再在電腦裡結束還在等的行程（關掉 stdin 不一定會讓它停）。
func (s *Service) stopLogin(l *login) {
	_ = l.p.Close()
	cmd, ok := loginCmds[l.engine]
	if !ok || s.Exec == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), limits.ACPCallTimeout)
	defer cancel()
	p, err := s.Exec(ctx, l.botID, ownDir(l.engine), acp.Agent{Label: "結束登入", Command: []string{"pkill", "-f", cmd.Kill}})
	if err != nil {
		s.log().Warn("結束登入指令失敗", "bot", l.botID, "engine", l.engine, "err", err)
		return
	}
	_, _ = io.Copy(io.Discard, p)
	_ = p.Close()
}

// LoginStatus 回傳 Bot 最近一次登入的狀態；沒有時 ok 是 false。
func (s *Service) LoginStatus(botID string) (LoginView, bool) {
	s.loginMu.Lock()
	l := s.logins[botID]
	s.loginMu.Unlock()
	if l == nil {
		return LoginView{}, false
	}
	return l.view(), true
}

// SubmitCode 把網頁給的授權碼交給還在等的登入指令（Claude Code）。
func (s *Service) SubmitCode(botID, code string) (LoginView, error) {
	s.loginMu.Lock()
	l := s.logins[botID]
	s.loginMu.Unlock()
	if l == nil {
		return LoginView{}, errors.New("沒有進行中的登入，請重新開始")
	}
	l.mu.Lock()
	waiting := l.state == LoginWaiting
	l.mu.Unlock()
	if !waiting || LoginKind(l.engine) != LoginCode {
		return l.view(), errors.New("這次登入不用貼授權碼")
	}
	if _, err := l.p.Write([]byte(code + "\n")); err != nil {
		return l.view(), fmt.Errorf("交不出授權碼：%w", err)
	}
	l.mu.Lock()
	l.codeSent = true
	l.mu.Unlock()
	return l.view(), nil
}

// CancelLogin 取消 Bot 進行中的登入。
func (s *Service) CancelLogin(_ context.Context, botID string) {
	s.loginMu.Lock()
	l := s.logins[botID]
	s.loginMu.Unlock()
	if l != nil && l.set(LoginCancelled, "已取消") {
		s.stopLogin(l)
	}
}

// opencodeProviders 是 OpenCode auth.json 裡金鑰登入的項目：OpenCode Go 訂閱與 OpenCode Zen（兩把不同的金鑰）。
var opencodeProviders = []string{"opencode-go", "opencode"}

// SetKeys 用金鑰登入 OpenCode：寫成它的 auth.json（{"opencode-go":{"type":"api","key":…}}），放進 own 資料夾，
// 記下 Bot 自己登入，再確認一次、記下模型清單。
func (s *Service) SetKeys(ctx context.Context, botID string, keys map[string]string) error {
	sp := Specs[acp.EngineOpenCode]
	auth := map[string]map[string]string{}
	for _, k := range opencodeProviders {
		if v := strings.TrimSpace(keys[k]); v != "" {
			auth[k] = map[string]string{"type": "api", "key": v}
		}
	}
	if len(auth) == 0 {
		return errors.New("沒有金鑰")
	}
	data, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	c, err := s.boot(ctx, botID)
	if err != nil {
		return err
	}

	// Lock to prevent concurrent credential refresh conflicts
	return s.ensureCredLock().withLock(ctx, sp.ownPath(), func() error {
		if err := s.writeOwn(ctx, c, sp, append(data, '\n')); err != nil {
			return err
		}
		if err := s.adoptNoLock(ctx, c, botID, sp.Engine); err != nil {
			return err
		}
		return s.Probe(ctx, botID, sp.Engine)
	})
}

// boot 找 Bot 的電腦；還沒有時開機建立（用 Exec 跑一個空指令）。
func (s *Service) boot(ctx context.Context, botID string) (Container, error) {
	if s.Locate == nil {
		return Container{}, ErrNoExec
	}
	c, err := s.Locate(ctx, botID)
	if err != nil || c.ID != "" {
		return c, err
	}
	if s.Exec == nil {
		return Container{}, ErrNoExec
	}
	p, err := s.Exec(ctx, botID, ownDir("boot"), acp.Agent{Label: "開機", Command: []string{"true"}})
	if err != nil {
		return Container{}, err
	}
	_, _ = io.Copy(io.Discard, p)
	_ = p.Close()
	if c, err = s.Locate(ctx, botID); err == nil && c.ID == "" {
		err = errors.New("找不到 Bot 的電腦")
	}
	return c, err
}

// Probe 開一次 agent（initialize＋session/new，不送訊息）確認這個 Bot 的這個 CLI 登入有效，並記下它列出的模型。
// Bot 正在用這個 CLI 時照平常的方式開（Bot 使用者、家目錄裡的憑證）；沒在用時以 root 用 own 資料夾裡的那份。
func (s *Service) Probe(ctx context.Context, botID, engineName string) error {
	a, ok := acp.Agents[engineName]
	if !ok {
		return fmt.Errorf("不支援 %s", engineName)
	}
	b, err := s.DB.GetBot(ctx, botID)
	if err != nil {
		return err
	}
	var launch acp.Launcher
	switch {
	case engineOf(b) == engineName && s.Launch != nil:
		launch = s.Launch
	case s.Exec != nil:
		launch = func(ctx context.Context, botID string, a acp.Agent) (acp.Process, error) {
			return s.Exec(ctx, botID, ownDir(engineName), a)
		}
	default:
		return ErrNoExec
	}
	d := acp.New(acp.Options{Agent: a, Launch: launch, Logger: s.log(),
		OnModels: func(_ string, current string, models []acp.ModelChoice) {
			s.RememberModels(engineName, current, models)
		}})
	defer d.Close(botID)
	_, err = d.Start(ctx, engine.StartOptions{BotID: botID, ThreadID: "xbot-login-check"})
	return err
}
